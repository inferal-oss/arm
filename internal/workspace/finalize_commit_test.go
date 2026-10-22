package workspace

import (
	"testing"
	"time"
)

func TestFinalizeRechecksFreshAuthorityAtFinalCommit(t *testing.T) {
	for _, change := range []string{"expiry", "clock-rollback", "closing"} {
		t.Run(change, func(t *testing.T) {
			d := newStoreFixture(t)
			root, _ := activateDirectory(t, d)
			m, join, admission := joinedCandidate(t, d, root)
			completeCandidate(t, d, root, m, admission)
			r := NewRuntime(m.s)
			defer r.Close()
			q, e := r.Begin(m.ctx, root.ID)
			if e != nil {
				t.Fatal(e)
			}
			snapshot, e := d.s.Issue(d.ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			if e = r.Accept(m.ctx, d.self, snapshot); e != nil {
				t.Fatal(e)
			}
			join, _, e = m.s.Lookup(m.ctx, root.ID, join.Key)
			if e != nil {
				t.Fatal(e)
			}
			var closeDone chan struct{}
			m.s.fault = func(stage string) error {
				if stage != "before_commit" {
					return nil
				}
				switch change {
				case "expiry":
					m.clock.advance(root.Policy.MembershipTTL+time.Second, root.Policy.MembershipTTL+time.Second)
				case "clock-rollback":
					m.clock.advance(-time.Second, 0)
				case "closing":
					closeDone = make(chan struct{})
					go func() { r.Close(); close(closeDone) }()
					for {
						r.mu.Lock()
						closed := r.closed
						r.mu.Unlock()
						if closed {
							break
						}
						time.Sleep(time.Millisecond)
					}
				}
				return nil
			}
			_, e = r.Finalize(m.ctx, root.ID, join.Key, join.Revision)
			m.s.fault = nil
			if e == nil {
				t.Fatal("expired/changed/closing authority finalized after final validation gate")
			}
			if closeDone != nil {
				select {
				case <-closeDone:
				case <-time.After(time.Second):
					t.Fatal("Close failed to join rejected finalizer")
				}
			}
			v, e := m.s.Read(m.ctx)
			if e != nil {
				t.Fatal(e)
			}
			saved, e := rootOf(&v, root.ID)
			if e != nil || saved.State != "joining" || saved.SelfGeneration != 0 {
				t.Fatal("rejected finalization persisted local membership", saved.State, e)
			}
			old, ok := operationOf(&v, root.ID, join.Principal, join.Key)
			if !ok || old.Phase != "approved" || old.Revision != join.Revision {
				t.Fatal("rejected finalization changed original intent")
			}
		})
	}
}

func TestCompletedFinalizeRetryDoesNotReapplyExpiredMembership(t *testing.T) {
	d, m, root, r := activePair(t)
	_ = d
	defer r.Close()
	before, ok, e := m.s.Lookup(m.ctx, root.ID, "join")
	if e != nil || !ok {
		t.Fatal(e)
	}
	m.clock.advance(root.Policy.MembershipTTL+time.Second, root.Policy.MembershipTTL+time.Second)
	got, e := r.Finalize(m.ctx, root.ID, before.Key, before.Revision)
	if e != nil || got.Revision != before.Revision || got.Phase != before.Phase {
		t.Fatal("readonly completed retry changed original finalization", e)
	}
}
