package workspace

import (
	"testing"
	"time"
)

func joinRecovery(t *testing.T, m fixture, root Instance, original Operation, key string) RecoveryRequest {
	t.Helper()
	v, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	op, _ := operationOf(&v, root.ID, nativeKey(m.authority), original.Key)
	return RecoveryRequest{Key: key, Workspace: root.ID, Generation: root.Generation, Original: OperationRef{op.Principal, op.Key, op.RequestHash, op.Revision}, Attempt: 1, Consent: original.ReviewHash, DirectoryPin: root.Directory.Fingerprint, CandidatePin: m.self.Fingerprint, PolicyRevision: root.PolicyRevision, Introducer: original.Review.Introducer, HostInbox: original.Review.HostInbox, PermitProof: true, Reason: "fresh explicit candidate review after restart"}
}
func TestRecoveryReviewRestartsWithoutReusingOriginalApproval(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, original, _ := joinedCandidate(t, d, root)
	before, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	old, _ := operationOf(&before, root.ID, nativeKey(m.authority), original.Key)
	m.s, e = Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	q := joinRecovery(t, m, root, original, "resume1")
	child, e := m.s.PrepareRecovery(m.ctx, "resume_join", q)
	if e != nil {
		t.Fatal(e)
	}
	if child.Phase != "awaiting_review" || child.Private != nil {
		t.Fatal(child)
	}
	approved, e := m.s.DecideRecovery(m.ctx, root.ID, child.Key, child.Revision, child.ReviewHash, true)
	if e != nil || approved.Phase != "approved" {
		t.Fatal(approved, e)
	}
	v, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	parent, _ := operationOf(&v, root.ID, nativeKey(m.authority), original.Key)
	if parent.RequestHash != old.RequestHash || string(parent.Request) != string(old.Request) || parent.ReviewHash != old.ReviewHash || *parent.Private != *old.Private || parent.Phase != old.Phase {
		t.Fatal("recovery rewrote original consent/request/worker", parent, old)
	}
	if !v.RecoveryEnabled {
		t.Fatal("recovery producer did not mark schema2")
	}
	if _, e = m.s.PrepareRecovery(m.ctx, "resume_join", q); e != nil {
		t.Fatal("exact retry", e)
	}
	q.Reason = "changed reason"
	if _, e = m.s.PrepareRecovery(m.ctx, "resume_join", q); code(e) != "idempotency_conflict" {
		t.Fatal(e)
	}
	m.s, e = Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	// An approved old child is not a fresh proof permit after another reboot.
	if _, e = m.s.DecideRecovery(m.ctx, root.ID, child.Key, approved.Revision, child.ReviewHash, true); e != nil {
		t.Fatal("idempotent decision lookup", e)
	}
	if _, e = m.s.CancelRecovery(m.ctx, root.ID, child.Key, approved.Revision); e != nil {
		t.Fatal("identity-independent monotone cancellation", e)
	}
	q = joinRecovery(t, m, root, original, "resume2")
	q.Attempt = 2
	q.Previous = child.Key
	child, e = m.s.PrepareRecovery(m.ctx, "resume_join", q)
	if e != nil {
		t.Fatal(e)
	}
	m.clock.advance(6*time.Minute, 6*time.Minute)
	if _, e = m.s.DecideRecovery(m.ctx, root.ID, child.Key, child.Revision, child.ReviewHash, true); code(e) != "review_expired" {
		t.Fatal(e)
	}
}

func TestRecoveryProofAfterBothRestartsCompletesSameOriginalMembership(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admit := joinedCandidate(t, d, root)
	originalRecords, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	original, _ := operationOf(&originalRecords, root.ID, rootPrincipalKey(root), admit.Key)
	c, started, e := d.s.BeginProof(d.ctx, rootPrincipal(root), admit.Key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.s.ProveJoin(m.ctx, d.self, c); e != nil {
		t.Fatal(e)
	}
	// The candidate persisted proof, but the directory lost the response.
	if e = d.s.RecordProofFailure(d.ctx, rootPrincipal(root), admit.Key, started.Revision); e != nil {
		t.Fatal(e)
	}
	d.s, e = Open(d.ctx, d.db, d.authority, d.self, d.clock)
	if e != nil {
		t.Fatal(e)
	}
	m.s, e = Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	jq := joinRecovery(t, m, root, join, "join-recovery")
	jchild, e := m.s.PrepareRecovery(m.ctx, "resume_join", jq)
	if e != nil {
		t.Fatal(e)
	}
	jchild, e = m.s.DecideRecovery(m.ctx, root.ID, jchild.Key, jchild.Revision, jchild.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	parent, _ := operationOf(&v, root.ID, rootPrincipalKey(root), admit.Key)
	aq := RecoveryRequest{Key: "admission-recovery", Workspace: root.ID, Generation: root.Generation, Original: OperationRef{parent.Principal, parent.Key, parent.RequestHash, parent.Revision}, Attempt: 1, Consent: join.ReviewHash, DirectoryPin: d.self.Fingerprint, CandidatePin: m.self.Fingerprint, PolicyRevision: root.PolicyRevision, HostInbox: root.Policy.HostInbox, CandidateAuthorization: jchild.ReviewHash, PermitProof: true, Reason: "fresh reviewed exact attempt after lost candidate reply"}
	achild, e := d.s.PrepareRecovery(d.ctx, "resume_admission", aq)
	if e != nil {
		t.Fatal(e)
	}
	achild, e = d.s.DecideRecovery(d.ctx, root.ID, achild.Key, achild.Revision, achild.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	c, achild, e = d.s.BeginRecoveryProof(d.ctx, root.ID, achild.Key)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := m.s.ProveJoin(m.ctx, d.self, c)
	if e != nil {
		t.Fatal(e)
	}
	done, e := d.s.CompleteRecoveryProof(d.ctx, root.ID, achild.Key, achild.Revision, proof)
	if e != nil {
		t.Fatal(e)
	}
	if done.Result == nil || done.Result.MembershipGeneration != 1 || done.Result.AdmissionKey != admit.Key {
		t.Fatal(done)
	}
	if _, e = d.s.CompleteRecoveryProof(d.ctx, root.ID, achild.Key, achild.Revision, proof); e != nil {
		t.Fatal("exact completed retry", e)
	}
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	parent, _ = operationOf(&v, root.ID, rootPrincipalKey(root), admit.Key)
	if len(v.Members) != 2 || parent.Phase != "completed" || parent.RequestHash != original.RequestHash || parent.Private.Nonce != original.Private.Nonce || parent.Private.Boot != original.Private.Boot {
		t.Fatal("recovery replaced member or historical intent", v.Members, parent, original)
	}
	if e = d.s.RecordProofFailure(d.ctx, rootPrincipal(root), admit.Key, started.Revision); code(e) != "stale_revision" {
		t.Fatal("late old callback changed success", e)
	}
}
func rootPrincipalKey(root Instance) string { return principalKey(rootPrincipal(root)) }

func TestRecoveryReconcilesCompletedAdmissionReadOnlyBeforeAnyReproof(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admit := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, m, admit)
	dirBefore, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	memberBefore, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	original, _ := operationOf(&memberBefore, root.ID, nativeKey(m.authority), join.Key)
	m.s, e = Open(m.ctx, m.db, m.authority, m.self, m.clock)
	if e != nil {
		t.Fatal(e)
	}
	q := joinRecovery(t, m, root, join, "reconcile-only")
	q.PermitProof = false
	child, e := m.s.PrepareRecovery(m.ctx, "resume_join", q)
	if e != nil {
		t.Fatal(e)
	}
	child, e = m.s.DecideRecovery(m.ctx, root.ID, child.Key, child.Revision, child.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	r := NewRuntime(m.s)
	defer r.Close()
	statusReq, e := r.BeginRecoveryStatus(m.ctx, root.ID, child.Key)
	if e != nil {
		t.Fatal(e)
	}
	status, e := d.s.OwnAdmissionStatus(d.ctx, m.self, statusReq)
	if e != nil {
		t.Fatal(e)
	}
	if status.State != "active" || status.CompletingAttempt != admit.Key {
		t.Fatal(status)
	}
	if e = r.AcceptRecoveryStatus(m.ctx, d.self, status); e != nil {
		t.Fatal(e)
	}
	refresh, e := r.Begin(m.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, refresh)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Accept(m.ctx, d.self, snapshot); e != nil {
		t.Fatal(e)
	}
	done, e := r.FinalizeRecovery(m.ctx, root.ID, child.Key, child.Revision)
	if e != nil || done.Phase != "completed" {
		t.Fatal(done, e)
	}
	if _, e = r.FinalizeRecovery(m.ctx, root.ID, child.Key, child.Revision); e != nil {
		t.Fatal("completed child retry", e)
	}
	after, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	parent, _ := operationOf(&after, root.ID, nativeKey(m.authority), join.Key)
	if parent.Phase != "completed" || after.Instances[0].State != "active" || parent.Private.Boot != original.Private.Boot || parent.Private.LastChallenge != original.Private.LastChallenge || parent.ReviewHash != original.ReviewHash || parent.RequestHash != original.RequestHash {
		t.Fatal("reconciliation replaced original evidence", parent)
	}
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); e != nil {
		t.Fatal(e)
	}
	dirAfter, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(dirAfter.Operations) != len(dirBefore.Operations) || len(dirAfter.Changes) != len(dirBefore.Changes) || len(dirAfter.Members) != len(dirBefore.Members) {
		t.Fatal("read-only reconciliation reproved or changed directory membership")
	}
}
