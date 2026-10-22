package workspace

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type proofFunc func(context.Context, Challenge) (Proof, error)

func (f proofFunc) Prove(ctx context.Context, c Challenge) (Proof, error) { return f(ctx, c) }
func rootPrincipal(root Instance) Principal {
	return Principal{root.ID, root.Generation, root.Self.Host, 1, root.Self.Fingerprint}
}
func joinedCandidate(t *testing.T, d fixture, root Instance) (fixture, Operation, Operation) {
	t.Helper()
	m := newStoreFixture(t)
	q := JoinRequest{Key: "join", Workspace: root.ID, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit candidate workspace review"}
	join, e := m.s.PrepareJoin(m.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	join, e = m.s.Decide(m.ctx, root.ID, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	admission, e := d.s.PrepareAdmission(d.ctx, rootPrincipal(root), AdmissionRequest{Key: "admit-" + m.self.Host, Candidate: m.self, ConsentHash: join.ReviewHash, HostInbox: root.Policy.HostInbox, PolicyRevision: root.PolicyRevision, Reason: "reviewed introducer proposes exact candidate"})
	if e != nil {
		t.Fatal(e)
	}
	return m, join, admission
}
func completeCandidate(t *testing.T, d fixture, root Instance, m fixture, admission Operation) Operation {
	t.Helper()
	service := NewService(d.ctx, d.s)
	defer service.Close()
	op, e := service.Activate(d.ctx, rootPrincipal(root), admission.Key, proofFunc(func(ctx context.Context, c Challenge) (Proof, error) { return m.s.ProveJoin(ctx, d.self, c) }))
	if e != nil {
		t.Fatal(e)
	}
	return op
}
func refreshAndFinalize(t *testing.T, d, m fixture, root Instance) *Runtime {
	t.Helper()
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
	op, ok, e := m.s.Lookup(m.ctx, root.ID, "join")
	if e != nil || !ok {
		t.Fatal(op, ok, e)
	}
	if _, e = r.Finalize(m.ctx, root.ID, op.Key, op.Revision); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestReviewedCandidateProofAndExplicitLocalFinalize(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admission := joinedCandidate(t, d, root)
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	member, _ := memberOf(&v, root.ID, m.self.Host)
	if member.State != "pending" {
		t.Fatal("unproved admission granted membership", member)
	}
	r := NewRuntime(m.s)
	if _, e = r.Check(m.ctx, root.ID, d.self.Host); code(e) != "membership_unknown" {
		t.Fatal(e)
	}
	result := completeCandidate(t, d, root, m, admission)
	if result.Result == nil || result.Result.State != "active" || result.Private != nil {
		t.Fatal(result)
	}
	v, e = m.s.Read(m.ctx)
	if e != nil || v.Instances[0].State != "joining" {
		t.Fatal("directory activation fabricated atomic local finalize", v, e)
	}
	r = refreshAndFinalize(t, d, m, root)
	access, e := r.Check(m.ctx, root.ID, d.self.Host)
	if e != nil || access.Source != m.self.Host || access.Target != d.self.Host || access.StreamTTL > 30*time.Second {
		t.Fatal(access, e)
	}
	if join.ReviewHash == "" {
		t.Fatal("candidate consent missing")
	}
	var calls atomic.Int32
	service := NewService(d.ctx, d.s)
	defer service.Close()
	again, e := service.Activate(d.ctx, rootPrincipal(root), admission.Key, proofFunc(func(context.Context, Challenge) (Proof, error) {
		calls.Add(1)
		return Proof{}, errors.New("must not replay")
	}))
	if e != nil || again.Phase != "completed" || calls.Load() != 0 {
		t.Fatal("known admission replayed proof", again, e, calls.Load())
	}
}
func TestAdmissionRechecksDenyAfterIOWithoutHoldingLedgerLock(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	service := NewService(d.ctx, d.s)
	defer service.Close()
	started := make(chan Challenge, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := service.Activate(d.ctx, rootPrincipal(root), admission.Key, proofFunc(func(ctx context.Context, c Challenge) (Proof, error) {
			started <- c
			select {
			case <-release:
				return m.s.ProveJoin(ctx, d.self, c)
			case <-ctx.Done():
				return Proof{}, ctx.Err()
			}
		}))
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("proof never started")
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	current := v.Instances[0]
	if _, e = d.s.Deny(d.ctx, root.ID, MutationRequest{Key: "deny", Host: m.self.Host, Revision: current.Revision, Reason: "withdraw candidate before proof settles"}); e != nil {
		t.Fatal("network proof held transaction or ignored denial", e)
	}
	close(release)
	select {
	case e := <-done:
		if code(e) != "admission_changed" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("late proof did not finish")
	}
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	candidate, _ := memberOf(&v, root.ID, m.self.Host)
	if candidate.State != "pending" {
		t.Fatal("late callback granted denied candidate", candidate)
	}
	op, found := operationOf(&v, root.ID, principalKey(rootPrincipal(root)), admission.Key)
	if !found {
		t.Fatal("lost admission intent")
	}
	if _, e = d.s.CancelAdmission(d.ctx, root.ID, d.self.Host, op.Key, op.Revision); e != nil {
		t.Fatal(e)
	}
	v, e = d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.s.Retire(d.ctx, root.ID, MutationRequest{Key: "retire", Revision: v.Instances[0].Revision, Reason: "explicit local retirement; remote cleanup not implied"}); e != nil {
		t.Fatal(e)
	}
	v, e = d.s.Read(d.ctx)
	if e != nil || !Settled(v) {
		t.Fatal("settled local retirement not inspectable", v, e)
	}
}
func TestInterruptedProofCannotReplayOrActivateAfterServiceClose(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	service := NewService(d.ctx, d.s)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := service.Activate(d.ctx, rootPrincipal(root), admission.Key, proofFunc(func(ctx context.Context, c Challenge) (Proof, error) {
			close(started)
			<-ctx.Done()
			return Proof{}, ctx.Err()
		}))
		done <- e
	}()
	<-started
	service.Close()
	select {
	case e := <-done:
		if code(e) != "workspace_outcome_unknown" {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("service close left proof caller blocked")
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	candidate, _ := memberOf(&v, root.ID, m.self.Host)
	if candidate.State != "pending" {
		t.Fatal(candidate)
	}
	op, _ := operationOf(&v, root.ID, principalKey(rootPrincipal(root)), admission.Key)
	if op.Phase != "unknown" {
		t.Fatal("interrupted proof not durable", op)
	}
	restarted, e := Open(d.ctx, d.db, d.authority, d.self, d.clock)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = restarted.BeginProof(d.ctx, rootPrincipal(root), admission.Key); code(e) != "admission_changed" {
		t.Fatal("restart replayed old worker", e)
	}
	if _, e = restarted.Retire(d.ctx, root.ID, MutationRequest{Key: "retire", Revision: v.Instances[0].Revision, Reason: "must not abandon pending admission"}); code(e) != "obligations_unresolved" {
		t.Fatal(e)
	}
}
func TestCandidateRejectsWrongDirectoryConsentNonceAndWidening(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	c, _, e := d.s.BeginProof(d.ctx, rootPrincipal(root), admission.Key)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"directory", "consent", "delegate", "endpoint", "nonce", "policy"} {
		t.Run(kind, func(t *testing.T) {
			bad := c
			authenticated := d.self
			switch kind {
			case "directory":
				authenticated = identityFixture(t, d.clock.Wall())
			case "consent":
				bad.ConsentHash = hash("forged")
			case "delegate":
				bad.CandidateIntroducer = true
			case "endpoint":
				bad.HostInbox = false
			case "nonce":
				bad.Nonce = "not a nonce"
			case "policy":
				bad.PolicyRevision++
			}
			if _, e = m.s.ProveJoin(m.ctx, authenticated, bad); code(e) != "forbidden" {
				t.Fatal("candidate accepted changed reviewed facts", e)
			}
		})
	}
}
func TestAdmissionDeadlineRecheckedAfterSuccessfulCandidateProof(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	c, proving, e := d.s.BeginProof(d.ctx, rootPrincipal(root), admission.Key)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := m.s.ProveJoin(m.ctx, d.self, c)
	if e != nil {
		t.Fatal(e)
	}
	d.clock.advance(5*time.Minute, 5*time.Minute)
	if _, e = d.s.CompleteProof(d.ctx, rootPrincipal(root), admission.Key, proving.Revision, proof); code(e) != "admission_changed" {
		t.Fatal("successful old proof extended missed admission window", e)
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	member, _ := memberOf(&v, root.ID, m.self.Host)
	if member.State != "pending" {
		t.Fatal("expired proof granted operational membership", member)
	}
}

func TestSuccessfulProofRacingServiceCloseIsUnknownAndCannotActivate(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, _, admission := joinedCandidate(t, d, root)
	service := NewService(d.ctx, d.s)
	_, e := service.Activate(d.ctx, rootPrincipal(root), admission.Key, proofFunc(func(ctx context.Context, c Challenge) (Proof, error) {
		proof, e := m.s.ProveJoin(ctx, d.self, c)
		service.Close()
		return proof, e
	}))
	if code(e) != "workspace_outcome_unknown" {
		t.Fatal("proof/close race lost original keyed uncertainty", e)
	}
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	member, _ := memberOf(&v, root.ID, m.self.Host)
	op, _ := operationOf(&v, root.ID, principalKey(rootPrincipal(root)), admission.Key)
	if member.State != "pending" || op.Phase != "unknown" {
		t.Fatal("close race activated or lost durable uncertainty", member, op)
	}
}
