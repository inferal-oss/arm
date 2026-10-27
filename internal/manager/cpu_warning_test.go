package manager

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/pressure"
)

// This supplies an already normalized candidate and compatible interval as a
// CONTROLLED input. It never claims the sleeping helper actually used a CPU.
func setHostPressureCredit(m *Manager, l model.Lease, j *job, n float64) {
	m.observationMu.Lock()
	defer m.observationMu.Unlock()
	at := m.cpuAvailability.At
	m.cpuCredits = []cpuCreditSample{{id: l.ID, job: j, identity: *j.identity, cookie: j.cpuAnchor, cores: n}}
	m.cpuCreditFrame = observe.CPUCreditFrame{Eligible: true, Evidence: observe.CPUCreditEvidence{
		PreviousHostStarted: at.Add(-time.Second - 2*time.Millisecond), PreviousHostFinished: at.Add(-time.Second + time.Millisecond),
		PreviousPostStarted: at.Add(-time.Second + 2*time.Millisecond), PreviousPostFinished: at.Add(-time.Second + 3*time.Millisecond),
		CurrentPreStarted: at.Add(-4 * time.Millisecond), CurrentPreFinished: at.Add(-3 * time.Millisecond),
		HostStarted: at.Add(-2 * time.Millisecond), HostFinished: at.Add(time.Millisecond),
	}}
}

func readCPUWarning(t *testing.T, m *Manager, now time.Time, tick time.Duration) (pressure.Sample, model.CPUWarningEvidence) {
	t.Helper()
	m.mu.Lock()
	ls, err := m.list()
	sample, e := m.cpuWarningSample(now, tick, ls)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return sample, e
}

func TestHostPressureUnsupportedSupervisedUseIsUncertainNotBlamed(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	// This matches production Linux: no compatible native usage cookie/frame.
	j.cpuAnchor = ""
	base, tick := time.Now().UTC(), 100*time.Second
	var state pressure.State
	for _, sec := range []int{0, 6, 12, 18} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0)
		// Even a stale forged candidate cannot bypass the unsupported-frame gate.
		setCPUCredit(m, l, j, 1)
		sample, e := readCPUWarning(t, m, now, observed)
		if sample.Known || e.State != "uncertain" || e.VerifiedManaged != 0 || e.MinimumCoverage != 0 || e.PossibleCompliant != 1 || !e.NativeKnown {
			t.Fatal("unmeasured supervised promise became actual usage", sample, e)
		}
		m.mu.Lock()
		in, p, err := m.hostPressureInput(now, observed)
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		var actions []pressure.Action
		state, actions, err = pressure.Advance(state, in, p)
		if err != nil || len(actions) != 0 || state.Resources[pressure.CPU].Active {
			t.Fatal("unsupported compliant work blamed", err, actions, state)
		}
	}
}

func TestHostPressureCreditRequiresCurrentOwnerAndSameFreshFrame(t *testing.T) {
	for _, change := range []string{"current", "replaced-job", "cookie", "exited", "released", "expired", "foreign-owner", "foreign-host", "lease-process", "stale", "future", "misaligned-frame", "misaligned-interval", "unsupported-frame", "nan"} {
		t.Run(change, func(t *testing.T) {
			m, l, j := cpuCreditFixture(t)
			if change == "expired" {
				r := request("other-positive-promise")
				r.Resources.CPU = .5
				if _, err := m.Acquire(r, nil); err != nil {
					t.Fatal(err)
				}
			}
			now, tick := time.Now().UTC(), 100*time.Second
			setHostPressureCPU(m, now, tick, 0)
			setHostPressureCredit(m, l, j, 1)
			m.mu.Lock()
			switch change {
			case "replaced-job":
				m.jobs[l.ID] = &job{cmd: j.cmd, done: make(chan struct{}), identity: j.identity, cpuAnchor: j.cpuAnchor}
			case "cookie":
				j.cpuAnchor += "-changed"
			case "exited":
				j.cpuExited.Store(true)
			case "released", "expired", "foreign-owner", "foreign-host", "lease-process":
				changed := l
				if change == "released" {
					changed.State = "terminal"
					ended := now
					changed.Ended = &ended
				}
				if change == "expired" {
					ended := now.Add(-time.Second)
					changed.Expires = &ended
				}
				if change == "foreign-owner" {
					changed.Owner = "foreign"
				}
				if change == "foreign-host" {
					changed.Host = "foreign-host"
				}
				if change == "lease-process" {
					p := *l.Process
					p.Start += "foreign"
					changed.Process = &p
				}
				if err := m.put(changed, "fixture.warning-change"); err != nil {
					m.mu.Unlock()
					t.Fatal(err)
				}
			}
			m.mu.Unlock()
			m.observationMu.Lock()
			switch change {
			case "stale":
				m.cpuObserved = now.Add(-5 * time.Second)
			case "future":
				m.cpuPressureTick = tick + time.Second
			case "misaligned-frame":
				m.cpuCreditFrame.Evidence.HostStarted = now.Add(time.Second)
			case "misaligned-interval":
				m.cpuAvailability.Interval = 2 * time.Second
			case "unsupported-frame":
				m.cpuCreditFrame.Eligible = false
			case "nan":
				m.cpuCredits[0].cores = math.NaN()
			}
			m.observationMu.Unlock()
			sample, e := readCPUWarning(t, m, now, tick)
			if change == "current" {
				if !sample.Known || e.State != "recovery" || e.VerifiedManaged <= .99 || e.VerifiedManaged > 1 {
					t.Fatal("current normalized credit lost", sample, e)
				}
			} else if e.VerifiedManaged != 0 || e.State == "recovery" {
				t.Fatal("invalid evidence raised confidence", change, sample, e)
			}
			// Restore exact original state before normal fixture cleanup.
			m.mu.Lock()
			m.jobs[l.ID] = j
			j.cpuExited.Store(false)
			j.cpuAnchor = m.cpuWarningOwners[l.ID].cookie
			if err := m.put(l, "fixture.warning-restore"); err != nil {
				m.mu.Unlock()
				t.Fatal(err)
			}
			m.mu.Unlock()
		})
	}
}

func TestHostPressureUncertainSampleCannotRecoverIncident(t *testing.T) {
	m, _, j := cpuCreditFixture(t)
	j.cpuAnchor = ""
	base, tick := time.Now().UTC(), 100*time.Second
	var state pressure.State
	for _, sec := range []int{0, 6, 12, 18, 24} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0)
		m.mu.Lock()
		in, p, err := m.hostPressureInput(now, observed)
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if sec <= 6 {
			in.Samples[pressure.CPU] = hostSample(now, observed, observed, now, m.pressureEpoch, 0, true)
		}
		state, _, err = pressure.Advance(state, in, p)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !state.Resources[pressure.CPU].Active {
		t.Fatal("unknown managed use manufactured CPU recovery", state)
	}
}

func TestHostPressureCoverageRoundingDoesNotCrossWarningThreshold(t *testing.T) {
	m := newManager(t)
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, 0.7501)
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" {
		t.Fatal("rounding invented a pressure crossing", sample, e)
	}
	setHostPressureCPU(m, now, tick, 0.9)
	sample, e = readCPUWarning(t, m, now, tick)
	if !sample.Known || sample.Available != 900 || e.State != "recovery" {
		t.Fatal(sample, e)
	}
}

func TestHostPressureBusyJobCannotBorrowOtherIdlePromises(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	r := request("bare-other-CPU-promise")
	r.Resources.CPU = 2
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .5)
	m.observationMu.Lock()
	eligible, capacity, busy := 4, 4.0, 3.5
	m.cpuAvailability.EligibleCPUs = &eligible
	m.cpuAvailability.Capacity = &capacity
	m.cpuAvailability.Busy = &busy
	m.observationMu.Unlock()
	setHostPressureCredit(m, l, j, 3)
	sample, e := readCPUWarning(t, m, now, tick)
	if e.VerifiedManaged > 1 || e.VerifiedManaged < .99 || e.PossibleCompliant != 1 || e.PossibleCoverage > 1.501 || !sample.Known || e.State != "shortfall" {
		t.Fatal("busy job borrowed unused bare promises", sample, e)
	}
}

func TestHostPressureCPUObservationAndPolicyRemainOneSnapshot(t *testing.T) {
	m := newManager(t)
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .95)
	m.observationMu.RLock()
	snapshot := m.cpuWarningSnapshotLocked()
	m.observationMu.RUnlock()
	// A later native publication belongs to a different capacity/policy cycle.
	setHostPressureCPU(m, now, tick, .1)
	m.observationMu.Lock()
	eligible, capacity := 4, 4.0
	m.cpuAvailability.EligibleCPUs = &eligible
	m.cpuAvailability.Capacity = &capacity
	m.observationMu.Unlock()
	m.mu.Lock()
	sample, e := m.cpuWarningSampleFrom(snapshot, now, tick, nil)
	policy := hostPressurePolicy(snapshot.cpu, model.MemoryAvailability{}, model.DiskAvailability{})
	m.mu.Unlock()
	// The representable .95 is just below the decimal value; an inward
	// scaled lower bound is 949m, while the original idle/policy stay frozen.
	if !sample.Known || sample.Available != 949 || e.Idle != .95 || e.State != "recovery" || policy.Resources[pressure.CPU].ExitAbove != 900 {
		t.Fatal("warning mixed CPU observation and policy cycles", sample, e, policy)
	}
}

func TestHostPressureStatusExposesUncertaintyWithoutChangingAdmission(t *testing.T) {
	m, _, j := cpuCreditFixture(t)
	j.cpuAnchor = ""
	now := time.Now().UTC()
	tick, err := pressureNativeTick()
	if err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, now, tick, .5)
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUWarning == nil || s.CPUWarning.State != "uncertain" || !s.CPUWarning.NativeKnown || s.CPUWarning.PossibleCompliant != 1 || s.CPUWarning.VerifiedManaged != 0 {
		t.Fatal("status hid unmeasured work", s.CPUWarning)
	}
	if s.CPU == nil || !s.CPU.Known || s.CPU.ManagedCredit != 0 || s.CPU.Capacity != .5 || s.CPU.Reserved != 1 || s.CPU.Deficit != .5 {
		t.Fatal("warning confidence changed actual admission", s.CPU)
	}
	wire, err := json.Marshal(s.CPUWarning)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), j.identity.Start) || strings.Contains(string(wire), j.cpuAnchor) && j.cpuAnchor != "" {
		t.Fatal("warning summary disclosed native ownership identity", string(wire))
	}
}

func TestHostPressureStatusUnreadableLedgerCannotReportRecovery(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("warning-unreadable-ledger"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, original, l.ID); err != nil {
			t.Error(err)
		}
	}()
	if _, err = m.db.Exec(`UPDATE leases SET data='not-json' WHERE id=?`, l.ID); err != nil {
		t.Fatal(err)
	}
	tick, err := pressureNativeTick()
	if err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, time.Now().UTC(), tick, 1)
	s, err := m.Status()
	if err == nil || s.CPUWarning == nil || s.CPUWarning.State != "unavailable" || s.CPUWarning.NativeKnown {
		t.Fatal("ledger failure manufactured recovery evidence", s.CPUWarning, err)
	}
}

func TestHostPressureInProcessOriginalOwnerRemainsUnmeasured(t *testing.T) {
	m := newManager(t)
	memoryReading(m, 1024, 1024, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(context.Background(), model.Request{Key: "warning-owned-transit", Reason: "controlled warning owner", TTL: time.Minute, Resources: model.Resources{CPU: .5, Memory: 1}}, joined)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		m.mu.Lock()
		m.relayTransits[h.id] = h
		m.mu.Unlock()
		close(joined)
		if _, err := h.FinishAfterJoin(); err != nil {
			t.Error(err)
		}
	}()
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .5)
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != .5 || e.VerifiedManaged != 0 {
		t.Fatal("owned in-process work became measured use or was blamed", sample, e)
	}
	replacement := *h
	m.mu.Lock()
	m.relayTransits[h.id] = &replacement
	m.mu.Unlock()
	sample, e = readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
		t.Fatal("replacement owner inherited confidence", sample, e)
	}
	m.mu.Lock()
	m.relayTransits[h.id] = h
	m.mu.Unlock()
	h.cancel()
	sample, e = readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
		t.Fatal("cancelled original owner retained confidence", sample, e)
	}
}
