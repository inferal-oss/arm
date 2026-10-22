package workspace

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestSavedDirectoryHopIsOnlyReachabilityAndLocallyDenied(t *testing.T) {
	d, root, b, c, _, _, _ := routingFixtures(t)
	restarted := NewRuntime(b.s)
	defer restarted.Close()
	for _, want := range []Identity{c.self, d.self} {
		got, e := restarted.DirectoryNextHop(b.ctx, root.ID, want.Host)
		if e != nil || got != want {
			t.Fatal("retained exact first hop unavailable", got, e)
		}
	}
	if _, e := restarted.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); code(e) != "membership_stale" {
		t.Fatal("reachability inspection activated member authority", e)
	}
	if _, e := restarted.DirectoryNextHop(b.ctx, root.ID, strings.Repeat("f", 32)); e == nil {
		t.Fatal("unknown first-hop pin accepted")
	}
	records, e := b.s.Read(b.ctx)
	if e != nil {
		t.Fatal(e)
	}
	current, e := rootOf(&records, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.s.Deny(b.ctx, root.ID, MutationRequest{Key: "deny-probe-relay", Host: c.self.Host, Revision: current.Revision, Reason: "do not probe this relay"}); e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.DirectoryNextHop(b.ctx, root.ID, c.self.Host); code(e) != "forbidden" {
		t.Fatal("locally denied relay was reachable through saved topology", e)
	}
	if got, e := restarted.DirectoryNextHop(b.ctx, root.ID, d.self.Host); e != nil || got != d.self {
		t.Fatal("unrelated deny removed exact directory", e)
	}
	restarted.Close()
	if _, e = restarted.DirectoryNextHop(b.ctx, root.ID, d.self.Host); code(e) != "backend_stopping" {
		t.Fatal("closed runtime returned a hop", e)
	}
}

func TestDirectoryGraphWitnessUsesOriginalMonotonicDeadlineAndNoRestartPromotion(t *testing.T) {
	d, root, b, _, dr, _, _ := routingFixtures(t)
	hash := strings.Repeat("a", 64)
	_, e := d.s.IssueChecked(d.ctx, RefreshRequest{Source: rootPrincipal(root), Nonce: strings.Repeat("b", 64)}, func(_ context.Context, _ *sql.Tx, b DirectoryIssue) (DirectoryDependent, error) {
		return DirectoryDependent{Hash: hash, Issued: b.Snapshot.Issued, ValidUntil: b.Snapshot.Issued.Add(time.Second)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := dr.RouteAccess(d.ctx, root.ID, d.self.Host, b.self.Host, RouteMessage)
	if e != nil || a.GraphHash != hash {
		t.Fatal("committed current directory graph missing", a, e)
	}
	// A tolerated2s wall/elapsed difference is not permission to rejuvenate a1s
	// original graph deadline while the directory's native membership stays valid.
	d.clock.advance(0, 2*time.Second)
	a, e = dr.RouteAccess(d.ctx, root.ID, d.self.Host, b.self.Host, RouteMessage)
	if e != nil || a.GraphHash != "" {
		t.Fatal("directory graph renewed from wall validity", a, e)
	}
	// Reopening the same actual ledger starts a different process epoch. Even an
	// unexpired saved graph cannot acquire the previous in-memory witness.
	reopened, e := Open(d.ctx, d.db, d.s.authority, d.self, d.clock)
	if e != nil {
		t.Fatal(e)
	}
	next := NewRuntime(reopened)
	defer next.Close()
	a, e = next.RouteAccess(d.ctx, root.ID, d.self.Host, b.self.Host, RouteMessage)
	if e != nil || a.GraphHash != "" {
		t.Fatal("reopen promoted saved directory graph", a, e)
	}
}
