package workspace

import (
	"sync"
	"testing"
)

func reviewedDocumentFixture(t *testing.T, d fixture, root Instance, key string) (fixture, Consent) {
	t.Helper()
	m := newStoreFixture(t)
	join, e := m.s.PrepareJoin(m.ctx, JoinRequest{Key: key, Workspace: root.ID, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Directory.Host, Reason: "document-specific review"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.s.Decide(m.ctx, root.ID, key, join.Revision, join.ReviewHash, true); e != nil {
		t.Fatal(e)
	}
	consent, e := m.s.Consent(m.ctx, root.ID, key)
	if e != nil {
		t.Fatal(e)
	}
	return m, consent
}
func TestNativeAdmissionReviewTransactionalRaceAndKnownKey(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	_, a := reviewedDocumentFixture(t, d, root, "join-a")
	_, b := reviewedDocumentFixture(t, d, root, "join-b")
	qa := NativeAdmissionRequest{Workspace: root.ID, Key: "admit-a", Consent: a, Reason: "review one candidate"}
	qb := NativeAdmissionRequest{Workspace: root.ID, Key: "admit-b", Consent: b, Reason: "review other candidate"}
	ra, e := d.s.ReviewNativeAdmission(d.ctx, qa)
	if e != nil {
		t.Fatal(e)
	}
	rb, e := d.s.ReviewNativeAdmission(d.ctx, qb)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	type result struct {
		op      Operation
		created bool
		err     error
	}
	results := make(chan result, 2)
	for _, q := range []struct {
		request NativeAdmissionRequest
		review  string
	}{{qa, ra.Hash}, {qb, rb.Hash}} {
		go func() {
			defer wg.Done()
			op, created, e := d.s.PrepareNativeAdmission(d.ctx, q.request, q.review)
			results <- result{op, created, e}
		}()
	}
	wg.Wait()
	close(results)
	created, stale := 0, 0
	var known result
	for r := range results {
		if r.err == nil && r.created {
			created++
			known = r
		} else if code(r.err) == "stale_review" {
			stale++
		} else {
			t.Fatal(r)
		}
	}
	if created != 1 || stale != 1 {
		t.Fatal("review did not serialize against current workspace revision", created, stale)
	}
	q, review := qa, ra.Hash
	if known.op.Key == qb.Key {
		q, review = qb, rb.Hash
	}
	// Exact retry after the root revision changed returns the durable operation,
	// not another dispatch. The created bit is the caller's no-reprobe fence.
	old, createdAgain, e := d.s.PrepareNativeAdmission(d.ctx, q, review)
	if e != nil || createdAgain || old.Revision != known.op.Revision {
		t.Fatal(old, createdAgain, e)
	}
	q.Reason = "different request"
	if _, _, e = d.s.PrepareNativeAdmission(d.ctx, q, review); code(e) != "idempotency_conflict" {
		t.Fatal(e)
	}
}
func TestPublicWorkspaceDocumentsAreDigestsNotCredentialsAndRespectDeny(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	invite, e := d.s.Invitation(d.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = invite.Validate(d.clock.Wall()); e != nil {
		t.Fatal(e)
	}
	altered := invite
	altered.Policy.MaxMembers--
	if e = altered.Validate(d.clock.Wall()); e == nil {
		t.Fatal("changed invitation accepted")
	}
	m, consent := reviewedDocumentFixture(t, d, root, "join")
	if e = consent.Validate(m.clock.Wall()); e != nil {
		t.Fatal(e)
	}
	v, e := m.s.Read(m.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.s.Deny(m.ctx, root.ID, MutationRequest{Key: "deny", Host: d.self.Host, Revision: v.Instances[0].Revision, Reason: "stop candidate authorization"}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.s.Consent(m.ctx, root.ID, "join"); code(e) != "forbidden" {
		t.Fatal("denied authority exported fresh approval", e)
	}
	if _, e = m.s.AuthorizeTransport(m.ctx, root.ID, root.Generation, d.self.Host, m.self.Host, TransportProof, consent.Consent); code(e) != "forbidden" {
		t.Fatal("denied authority admitted a stream", e)
	}
}
func TestWorkspaceTransportGateRequiresDurableCurrentProofDispatch(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	m, join, admission := joinedCandidate(t, d, root)
	if _, e := d.s.AuthorizeTransport(d.ctx, root.ID, root.Generation, d.self.Host, m.self.Host, TransportProof, join.ReviewHash); e == nil {
		t.Fatal("merely approved admission admitted proof stream")
	}
	if _, _, e := d.s.BeginProof(d.ctx, rootPrincipal(root), admission.Key); e != nil {
		t.Fatal(e)
	}
	for _, f := range []struct {
		s   *Store
		ctx fixture
	}{{d.s, d}, {m.s, m}} {
		permit, e := f.s.AuthorizeTransport(f.ctx.ctx, root.ID, root.Generation, d.self.Host, m.self.Host, TransportProof, join.ReviewHash)
		if e != nil || permit.Local != f.ctx.self {
			t.Fatal(permit, e)
		}
	}
	for _, class := range []string{"run", "ssh-exec", "lease-own", "native", "message_send"} {
		if _, e := m.s.AuthorizeTransport(m.ctx, root.ID, root.Generation, d.self.Host, m.self.Host, class, join.ReviewHash); e == nil {
			t.Fatal("foreign operation class admitted", class)
		}
	}
	if e := d.s.RecordProofFailure(d.ctx, rootPrincipal(root), admission.Key, admission.Revision+1); e != nil {
		t.Fatal(e)
	}
	if _, e := d.s.AuthorizeTransport(d.ctx, root.ID, root.Generation, d.self.Host, m.self.Host, TransportProof, join.ReviewHash); e == nil {
		t.Fatal("unknown admission re-authorized proof")
	}
}
