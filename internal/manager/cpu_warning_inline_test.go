package manager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/observe"
	"golang.org/x/crypto/ssh"
)

// These use the reviewed real charged-row/original-owner fixture. CPU values
// are controlled warning inputs; this is not native attribution of Go work.
func TestHostPressureInlineOwnerIsPossibleUseNeverNativeCredit(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .75)
	// Even an injected candidate cannot turn inline Go work into process credit.
	m.observationMu.Lock()
	m.cpuCredits = []cpuCreditSample{{id: h.lease, cores: 1}}
	at := m.cpuAvailability.At
	m.cpuCreditFrame = observe.CPUCreditFrame{Eligible: true, Evidence: observe.CPUCreditEvidence{
		PreviousHostStarted: at.Add(-time.Second - 2*time.Millisecond), PreviousHostFinished: at.Add(-time.Second + time.Millisecond),
		PreviousPostStarted: at.Add(-time.Second + 2*time.Millisecond), PreviousPostFinished: at.Add(-time.Second + 3*time.Millisecond),
		CurrentPreStarted: at.Add(-4 * time.Millisecond), CurrentPreFinished: at.Add(-3 * time.Millisecond),
		HostStarted: at.Add(-2 * time.Millisecond), HostFinished: at.Add(time.Millisecond),
	}}
	if !cpuWarningFrameMatches(m.cpuCreditFrame, m.cpuAvailability) {
		m.observationMu.Unlock()
		t.Fatal("controlled same-frame candidate is invalid")
	}
	m.observationMu.Unlock()
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != .25 || e.VerifiedManaged != 0 || e.MinimumCoverage != .75 {
		t.Fatal("inline promise minted measured use or was blamed", sample, e)
	}
	owners, _ := m.cpuOwners()
	for _, owner := range owners {
		if owner.Token == h.lease {
			t.Fatal("inline Go work invented a native owner")
		}
	}
	// With this scope fully occupied by compliant inline work, unsupported
	// attribution still withholds a warning without certifying recovery.
	setHostPressureCPU(m, now, tick, 0)
	m.observationMu.Lock()
	capacity, busy := .25, .25
	m.cpuAvailability.Capacity, m.cpuAvailability.Busy = &capacity, &busy
	m.observationMu.Unlock()
	sample, e = readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCoverage != .25 || e.VerifiedManaged != 0 || e.MinimumCoverage != 0 {
		t.Fatal("fully occupied compliant inline work was blamed or declared measured", sample, e)
	}
}

func TestHostPressureInlineOwnerDeclinedBeforeStartIsUncertain(t *testing.T) {
	m, h := reviewSyncHold(t)
	if err := h.Start(nil); err == nil {
		t.Fatal("nil worker started")
	}
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .75)
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
		t.Fatal("declined worker manufactured use", sample, e)
	}
}

func TestHostPressureInlineOwnerStartedAfterFrameIsUncertain(t *testing.T) {
	m, h := reviewSyncHold(t)
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .75)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
		t.Fatal("new owner was adopted into an earlier observation", sample, e)
	}
}

func TestHostPressureInlineOwnerRejectsStaleListedRowAndMultipleOwnerKinds(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	owners := m.captureCPUWarningOwners()
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(h.lease)
	if err != nil {
		t.Fatal(err)
	}
	if !m.currentCPUWarningOwner(l, owners[l.ID]) {
		t.Fatal("original exact owner unavailable")
	}
	stale := l
	stale.Request.Reason = "stale listed row"
	if m.currentCPUWarningOwner(stale, owners[l.ID]) {
		t.Fatal("listed row differs from original charged snapshot")
	}
	m.jobs[l.ID] = &job{}
	defer delete(m.jobs, l.ID)
	if m.currentCPUWarningOwner(l, owners[l.ID]) {
		t.Fatal("same lease borrowed two original owner kinds")
	}
}

func TestHostPressureInlineOwnerRequiresSameOriginalFrameAndLease(t *testing.T) {
	for _, change := range []string{"pointer", "runtime", "context", "done", "started", "lease-bytes", "lease-row", "volume", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			m, h := reviewSyncHold(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
				t.Fatal(err)
			}
			<-entered
			now, tick := time.Now().UTC(), 100*time.Second
			setHostPressureCPU(m, now, tick, .75)
			originalRuntime, originalDone, originalRecord := h.runtime, h.done, h.leaseRecord
			var raw string
			if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, h.lease).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			originalContext := h.runtime.ctx
			restore := func() {}
			switch change {
			case "pointer":
				m.mu.Lock()
				m.syncWorkers[h.lease] = &SyncWorkerHold{manager: m, lease: h.lease, runtime: h.runtime, done: h.done, leaseRecord: h.leaseRecord}
				m.mu.Unlock()
				restore = func() { m.mu.Lock(); m.syncWorkers[h.lease] = h; m.mu.Unlock() }
			case "runtime":
				m.mu.Lock()
				h.runtime = &sshRuntime{ctx: originalContext}
				m.mu.Unlock()
				restore = func() { m.mu.Lock(); h.runtime = originalRuntime; m.mu.Unlock() }
			case "context":
				ctx, cancel := context.WithCancel(context.Background())
				m.mu.Lock()
				h.runtime.ctx = ctx
				m.mu.Unlock()
				restore = func() { m.mu.Lock(); h.runtime.ctx = originalContext; m.mu.Unlock(); cancel() }
			case "done":
				m.mu.Lock()
				h.done = make(chan struct{})
				m.mu.Unlock()
				restore = func() { m.mu.Lock(); h.done = originalDone; m.mu.Unlock() }
			case "started":
				h.mu.Lock()
				h.started = false
				h.mu.Unlock()
				restore = func() { h.mu.Lock(); h.started = true; h.mu.Unlock() }
			case "lease-bytes":
				m.mu.Lock()
				h.leaseRecord = append(append([]byte(nil), h.leaseRecord...), ' ')
				m.mu.Unlock()
				restore = func() { m.mu.Lock(); h.leaseRecord = originalRecord; m.mu.Unlock() }
			case "lease-row":
				m.mu.Lock()
				l, err := m.get(h.lease)
				if err == nil {
					l.Request.Reason = "same revision replacement"
					var changed []byte
					changed, err = json.Marshal(l)
					if err == nil {
						_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changed), h.lease)
					}
				}
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, raw, h.lease); err != nil {
						t.Error(err)
					}
				}
			case "volume":
				if _, err := m.db.Exec(`UPDATE sync_lease_bindings SET volume='replaced' WHERE lease=?`, h.lease); err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if _, err := m.db.Exec(`UPDATE sync_lease_bindings SET volume=? WHERE lease=?`, h.volume, h.lease); err != nil {
						t.Error(err)
					}
				}
			case "cancelled":
				h.cancel()
			}
			defer restore()
			sample, e := readCPUWarning(t, m, now, tick)
			if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
				t.Fatal("changed inline owner retained warning confidence", change, sample, e)
			}
		})
	}
}

func TestHostPressureInlineOwnerCompletionCannotLendPossibleUse(t *testing.T) {
	m, h := reviewSyncHold(t)
	entered, release := make(chan struct{}), make(chan struct{})
	if err := h.Start(func(context.Context, ssh.Channel) error { close(entered); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	<-entered
	now, tick := time.Now().UTC(), 100*time.Second
	setHostPressureCPU(m, now, tick, .75)
	close(release)
	select {
	case <-h.done:
	case <-time.After(3 * time.Second):
		t.Fatal("original inline body did not join")
	}
	sample, e := readCPUWarning(t, m, now, tick)
	if sample.Known || e.State != "uncertain" || e.PossibleCompliant != 0 || e.VerifiedManaged != 0 {
		t.Fatal("joined inline work retained use", sample, e)
	}
}
