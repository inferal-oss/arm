package workspace

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeCloseCancelsAndJoinsActualBlockedFinalization(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admit := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, m, admit)
	join, _, _ = m.s.Lookup(m.ctx, root.ID, join.Key)
	r := NewRuntime(m.s)
	q, e := r.Begin(m.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Accept(m.ctx, d.self, snap); e != nil {
		t.Fatal(e)
	}
	started, release := make(chan struct{}), make(chan struct{})
	m.s.fault = func(stage string) error {
		if stage == "before_commit" {
			close(started)
			<-release
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, e := r.Finalize(context.Background(), root.ID, join.Key, join.Revision); done <- e }()
	select {
	case <-started:
	case e := <-done:
		t.Fatal("finalization never reached commit barrier", e)
	case <-time.After(time.Second):
		t.Fatal("finalization never reached barrier")
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while actual binding transaction was still in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not join cancelled finalization")
	}
	if e = <-done; e == nil {
		t.Fatal("cancelled blocked transaction committed")
	}
	m.s.fault = nil
	v, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if v.Instances[0].State != "joining" {
		t.Fatal("binding committed after completed Close", v.Instances[0].State)
	}
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); code(e) != "membership_unknown" {
		t.Fatal("closed runtime retained authority", e)
	}
	if _, e = r.Finalize(m.ctx, root.ID, join.Key, join.Revision); code(e) != "membership_unknown" {
		t.Fatal("late finalize revived binding", e)
	}
}
func TestOperatorWithoutCurrentIdentityCanSettleButExposesNoApproval(t *testing.T) {
	f := newStoreFixture(t)
	root, _ := activateDirectory(t, f)
	operator, e := OpenOperator(f.ctx, f.db, f.authority, f.clock)
	if e != nil {
		t.Fatal(e)
	}
	f.clock.advance(48*time.Hour, 48*time.Hour)
	if _, e = operator.Read(f.ctx); e != nil {
		t.Fatal("expired historical identity blocked operator inspection", e)
	}
	v, _ := operator.Read(f.ctx)
	if _, e = operator.Retire(f.ctx, root.ID, MutationRequest{Key: "retire-with-expired-cert", Revision: v.Instances[0].Revision, Reason: "explicit authenticated local retirement only"}); e != nil {
		t.Fatal(e)
	}
	v, e = operator.Read(f.ctx)
	if e != nil || !Settled(v) {
		t.Fatal("retirement unavailable without current certificate", e)
	}
}
func TestWorkspaceDriverErrorsAreNotClassifiedAsOptionalEvidenceCorruption(t *testing.T) {
	f := newStoreFixture(t)
	f.db.Close()
	_, e := f.s.Read(f.ctx)
	if e == nil || IsInvalidState(e) {
		t.Fatal("arbitrary database failure mislabeled degradable evidence", e)
	}
	if !errors.Is(stateProblem(context.Canceled), context.Canceled) {
		t.Fatal("context error swallowed")
	}
}
