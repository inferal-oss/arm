package manager

import (
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/model"
)

func preservePressureClockFixture(t *testing.T, m *Manager, l model.Lease) {
	t.Helper()
	m.mu.Lock()
	read, last, clockErr := m.leaseClockRead, m.leaseClockLast, m.leaseClockError
	m.mu.Unlock()
	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.leaseClockRead, m.leaseClockLast, m.leaseClockError = read, last, clockErr
		if err := m.put(l, "fixture.pressure-clock-restored"); err != nil {
			t.Error(err)
		}
	})
}

func TestHostPressureRosterUsesElapsedDeadlineAcrossAuditWallJump(t *testing.T) {
	for _, jump := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		t.Run(jump.String(), func(t *testing.T) {
			m, clock := clockManager(t)
			l := clockGrant(t, m, "pressure-elapsed-roster", time.Minute)
			preservePressureClockFixture(t, m, l)
			now := l.Expires.Add(jump)
			sample := clock.sample
			sample.Wall = now
			sample.Tick = l.Clock.Anchor + int64(30*time.Second)
			reads := 0
			m.mu.Lock()
			m.leaseClockRead = func() (leaseclock.Sample, error) {
				reads++
				return sample, nil
			}
			in, _, err := m.hostPressureInput(now, 100*time.Second)
			firstReads := reads
			m.mu.Unlock()
			if err != nil || firstReads != 1 || len(in.Jobs) != 1 || in.Jobs[0].LeaseID != l.ID || !in.Jobs[0].ExpiresAt.Equal(now.Add(30*time.Second)) {
				t.Fatal("audit wall jump changed elapsed roster", err, in.Jobs)
			}
			m.mu.Lock()
			sample.Tick = l.Clock.End
			in, _, err = m.hostPressureInput(now, 101*time.Second)
			m.mu.Unlock()
			if err != nil || len(in.Jobs) != 0 {
				t.Fatal("elapsed expiry kept a pressure recipient", err, in.Jobs)
			}
			if got := clockGet(t, m, l.ID); !consuming(got.State) {
				t.Fatal("pressure filtering released a retained lease", got)
			}
		})
	}
}

func TestHostPressureRosterRefusesInvalidClockAndNonactiveLease(t *testing.T) {
	for _, scenario := range []string{"changed-boot", "regressed-tick", "missing-deadline", "mismatched-audit", "expired", "ending", "needs_attention"} {
		t.Run(scenario, func(t *testing.T) {
			m, clock := clockManager(t)
			l := clockGrant(t, m, "pressure-elapsed-refusal", time.Minute)
			preservePressureClockFixture(t, m, l)
			sample := clock.sample
			now := l.Created
			changed := l
			switch scenario {
			case "changed-boot":
				sample.Boot = "foreign-boot"
			case "regressed-tick":
				sample.Tick = l.Clock.Anchor - 1
			case "missing-deadline":
				changed.Clock = nil
			case "mismatched-audit":
				altered := l.Expires.Add(time.Second)
				changed.Expires = &altered
			case "expired":
				sample.Tick = l.Clock.End
			case "ending", "needs_attention":
				changed.State = scenario
			}
			m.mu.Lock()
			if err := m.put(changed, "fixture.pressure-elapsed-refusal"); err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
			in, _, err := m.hostPressureInput(now, 100*time.Second)
			m.mu.Unlock()
			if err != nil || len(in.Jobs) != 0 {
				t.Fatal("invalid or nonactive lease entered warning roster", err, in.Jobs)
			}
			if got := clockGet(t, m, l.ID); !consuming(got.State) {
				t.Fatal("warning roster changed retained charge state", got)
			}
		})
	}
}

func TestHostPressureNativeWarningStartRechecksElapsedExpiry(t *testing.T) {
	for _, scenario := range []string{"expired", "changed-boot", "regressed-tick", "missing-deadline", "mismatched-audit", "ending", "needs_attention"} {
		t.Run(scenario, func(t *testing.T) {
			m := newManager(t)
			id, item, _, l := claimedHostPressureWarning(t, m)
			preservePressureClockFixture(t, m, l)
			m.mu.Lock()
			sample := m.leaseClockLast
			if sample.Boot != l.Clock.Boot || !l.Expires.After(time.Now()) {
				m.mu.Unlock()
				t.Fatal("fixture lacks a current native lease with future audit wall expiry")
			}
			changed := l
			switch scenario {
			case "expired":
				sample.Tick = l.Clock.End
			case "changed-boot":
				sample.Boot = "foreign-boot"
			case "regressed-tick":
				sample.Tick = l.Clock.Anchor - 1
			case "missing-deadline":
				changed.Clock = nil
			case "mismatched-audit":
				altered := l.Expires.Add(time.Second)
				changed.Expires = &altered
			case "ending", "needs_attention":
				changed.State = scenario
			}
			if err := m.put(changed, "fixture.warning-start-refusal"); err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
			m.mu.Unlock()
			started := false
			allowed, err := m.hostPressureStartFence(id, item, func() error { started = true; return nil })
			if err != nil || allowed || started {
				t.Fatal("invalid elapsed entitlement launched a stale native warning", allowed, started, err)
			}
			if got := clockGet(t, m, l.ID); !consuming(got.State) {
				t.Fatal("warning refusal released retained lease charge", got)
			}
		})
	}
}

func TestCPUWarningAttributionUsesElapsedDeadlineAndRefusesUnknown(t *testing.T) {
	m, l, _ := cpuCreditFixture(t)
	preservePressureClockFixture(t, m, l)
	now := l.Expires.Add(time.Hour)
	tick := 100 * time.Second
	setHostPressureCPU(m, now, tick, 0)
	m.mu.Lock()
	sample := m.leaseClockLast
	sample.Wall = now
	m.leaseClockRead = func() (leaseclock.Sample, error) { return sample, nil }
	m.mu.Unlock()
	pressureSample, evidence := readCPUWarning(t, m, now, tick)
	if !evidence.NativeKnown || evidence.PossibleCompliant <= 0 || evidence.State != "uncertain" || pressureSample.Known {
		t.Fatal("audit wall jump discarded possible original CPU use", pressureSample, evidence)
	}
	m.mu.Lock()
	invalid := l
	invalid.Clock = nil
	err := m.put(invalid, "fixture.pressure-clock-unknown")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	pressureSample, evidence = readCPUWarning(t, m, now, tick)
	if pressureSample.Known || evidence.State != "unavailable" || evidence.VerifiedManaged != 0 || evidence.PossibleCompliant != 0 {
		t.Fatal("unknown elapsed provenance produced CPU attribution", pressureSample, evidence)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != l.Request.Resources.CPU {
		t.Fatal("unknown attribution released the CPU promise", status.Reserved, err)
	}
}

func TestCPUWarningRetainedNonactiveWorkCannotLendCredit(t *testing.T) {
	for _, scenario := range []struct {
		name, state string
		invalid     bool
	}{{"ending", "ending", false}, {"needs_attention", "needs_attention", false}, {"ending-unknown", "ending", true}} {
		t.Run(scenario.name, func(t *testing.T) {
			m, l, _ := cpuCreditFixture(t)
			preservePressureClockFixture(t, m, l)
			now, tick := time.Now().UTC(), 100*time.Second
			setHostPressureCPU(m, now, tick, 0)
			changed := l
			changed.State = scenario.state
			if scenario.invalid {
				changed.Clock = nil
			}
			m.mu.Lock()
			err := m.put(changed, "fixture.pressure-nonactive")
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			sample, evidence := readCPUWarning(t, m, now, tick)
			wantState := "uncertain"
			wantNative := true
			if scenario.invalid {
				wantState = "unavailable"
				wantNative = false
			}
			if sample.Known || evidence.NativeKnown != wantNative || evidence.VerifiedManaged != 0 || evidence.PossibleCompliant != 0 || evidence.State != wantState {
				t.Fatal("nonactive retained owner lent warning credit", sample, evidence)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.CPU != l.Request.Resources.CPU {
				t.Fatal("nonactive work lost its retained charge", status.Reserved, err)
			}
		})
	}
}
