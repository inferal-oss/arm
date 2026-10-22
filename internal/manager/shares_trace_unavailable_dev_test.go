//go:build arm_devtrace

package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The model omits a runtime no-start observation after an uncertain worker-start
// commit. That omission is conservative: it must never become no-start proof
// authorizing terminal credits/replacement while the exact boundary is fenced.
func TestShareFormalUnavailableSkipRetainsObligations(t *testing.T) {
	f := newShareFixture(t)
	var native atomic.Int32
	f.m.shares.beforeNativeBuild = func(context.Context) { native.Add(1) }
	o := &shareFormalObserver{key: "formal-unavailable-skip", storage: f.storage.ID, native: "never", events: make(chan shareFormalState, 256), gates: map[string]chan struct{}{"worker_start": make(chan struct{}), "settle": make(chan struct{})}}
	shareFormalObservers.Store(f.m.home, o)
	openGate := func(name string) {
		select {
		case <-o.gates[name]:
		default:
			close(o.gates[name])
		}
	}
	t.Cleanup(func() { shareFormalObservers.Delete(f.m.home); openGate("worker_start"); openGate("settle") })
	f.m.mu.Lock()
	f.m.traceShareLocked("init", "")
	f.m.mu.Unlock()
	q := shareRequest(f.storage, o.key)
	done := make(chan struct{})
	go func() { _, _ = f.m.PrepareShare(t.Context(), q); close(done) }()
	for _, action := range []string{"init", "admit"} {
		select {
		case row := <-o.events:
			if row.Action != action {
				t.Fatal("wrong actual prefix", row.Action)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing admission observation")
		}
	}
	shareLoseNextCommitAcknowledgment(f.m)
	openGate("worker_start")
	select {
	case row := <-o.events:
		if row.Action != "worker_start_unknown" || !row.Charged || !row.Pin || row.Boundary != "unavailable" {
			t.Fatal("wrong actual uncertainty", row)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing worker-start uncertainty")
	}
	openGate("settle")
	shareWait(t, func() bool { f.m.mu.Lock(); defer f.m.mu.Unlock(); return len(f.m.shares.workers) == 0 })
	<-done
	if native.Load() != 0 {
		t.Fatal("uncertain pre-entry phase launched native work")
	}
	o.mu.Lock()
	rows := append([]shareFormalState(nil), o.records...)
	err := o.err
	o.mu.Unlock()
	if err != nil || len(rows) != 3 || rows[2].Native != "never" || rows[2].SkippedProved || rows[2].JoinKnown || rows[2].Launches != 0 {
		t.Fatal("unavailable skip invented settlement proof", rows, err)
	}
	p := formalPublication(t, f.m, q.Key)
	l, err := f.m.Inspect(p.Lease)
	if err != nil || l.State != "active" || l.Ended != nil || p.Retired != nil {
		t.Fatal("worker exit returned uncertain credit", l.State, err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != q.Resources.CPU || status.Reserved.Memory != q.Resources.Memory {
		t.Fatal("unavailable no-start freed resources", status.Reserved, err)
	}
	shareAssertUnavailable(t, f.m, q.Key)
	for _, key := range []string{q.Key, "replacement-unavailable-skip"} {
		next := q
		next.Key = key
		if _, err = f.m.PrepareShare(t.Context(), next); errorCode(err) != "share_evidence_unavailable" {
			t.Fatal("unavailable skip permitted replacement", err)
		}
	}
	if _, err = f.m.End(p.Lease, "released"); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("unavailable skip permitted release", err)
	}
}
