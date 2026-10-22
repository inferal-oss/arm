package workspace

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func routingFixtures(t *testing.T) (fixture, Instance, fixture, fixture, *Runtime, *Runtime, *Runtime) {
	t.Helper()
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	b, _, ba := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, b, ba)
	c, _, ca := joinedCandidate(t, d, root)
	completeCandidate(t, d, root, c, ca)
	br, cr := refreshAndFinalize(t, d, b, root), refreshAndFinalize(t, d, c, root)
	dr := NewRuntime(d.s)
	t.Cleanup(br.Close)
	t.Cleanup(cr.Close)
	t.Cleanup(dr.Close)
	return d, root, b, c, dr, br, cr
}
func TestRoutedCurrentMemberGateRolesAndRestartFreshness(t *testing.T) {
	d, root, b, c, dr, br, cr := routingFixtures(t)
	for _, r := range []*Runtime{dr, br, cr} {
		access, e := r.RouteAccess(d.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage)
		if e != nil || access.Origin != b.self || access.Destination != c.self || access.OriginGeneration != 1 || access.DestinationGeneration != 1 || access.Remaining <= 0 {
			t.Fatal(access, e)
		}
	}
	empty := NewRuntime(c.s)
	defer empty.Close()
	if _, e := empty.RouteAccess(c.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); code(e) != "membership_stale" {
		t.Fatal("restart observation authorized message", e)
	}
	if _, e := empty.RouteAccess(c.ctx, root.ID, c.self.Host, d.self.Host, RouteDirectory); e != nil {
		t.Fatal("durable exact directory refresh could not restore freshness", e)
	}
	if _, e := empty.RouteAccess(c.ctx, root.ID, b.self.Host, d.self.Host, RouteDirectory); code(e) != "membership_stale" {
		t.Fatal("unfresh member relayed another origin", e)
	}
	for _, class := range []string{"run", "status", "acquire", "ssh-exec", "native", "lease-own"} {
		if _, e := br.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, class); e == nil {
			t.Fatal("foreign class acquired workspace route", class)
		}
	}
}
func TestRoutedDestinationDenyAtomicAndExpiredObservation(t *testing.T) {
	d, root, b, c, _, _, cr := routingFixtures(t)
	records, e := c.s.Read(c.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Deny(c.ctx, root.ID, MutationRequest{Key: "deny-origin", Host: b.self.Host, Revision: records.Instances[0].Revision, Reason: "stop original source locally"}); e != nil {
		t.Fatal(e)
	}
	called := false
	if _, e = cr.RouteTransaction(c.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage, func(*sql.Tx, RouteAccess) error { called = true; return nil }); code(e) != "forbidden" || called {
		t.Fatal("denied source reached destination transaction", e, called)
	}
	// Another live member is still governed by its ORIGINAL request-start expiry,
	// never an extension from the receipt or a new read after sleep.
	fresh := NewRuntime(b.s)
	defer fresh.Close()
	q, e := fresh.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if e = fresh.Accept(b.ctx, d.self, snap); e != nil {
		t.Fatal(e)
	}
	b.clock.advance(root.Policy.MembershipTTL+time.Second, root.Policy.MembershipTTL+time.Second)
	if _, e = fresh.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); code(e) != "membership_stale" {
		t.Fatal("expired route observation accepted", e)
	}
}
func TestRoutedTransactionCloseJoinsAndRollsBackSameSQLAdmission(t *testing.T) {
	_, root, b, c, _, _, cr := routingFixtures(t)
	if _, e := c.db.Exec(`CREATE TABLE route_test_effect(value TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, e := cr.RouteTransaction(c.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage, func(tx *sql.Tx, access RouteAccess) error {
			if access.Origin != b.self {
				return problem("forbidden", "source changed")
			}
			if _, e := tx.Exec(`INSERT INTO route_test_effect(value) VALUES('must rollback')`); e != nil {
				return e
			}
			close(entered)
			<-release
			return nil
		})
		result <- e
	}()
	<-entered
	closed := make(chan struct{})
	go func() { cr.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while current route transaction callback was admitted")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not join callback")
	}
	if e := <-result; e == nil {
		t.Fatal("cancelled route admission committed")
	}
	var n int
	if e := c.db.QueryRow(`SELECT count(*) FROM route_test_effect`).Scan(&n); e != nil || n != 0 {
		t.Fatal("cancelled message side effect escaped atomic transaction", n, e)
	}
	if _, e := cr.RouteAccess(context.Background(), root.ID, b.self.Host, c.self.Host, RouteMessage); code(e) != "backend_stopping" {
		t.Fatal("closed runtime authorized new route", e)
	}
}
