package workspace

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDirectoryIssueCheckedRollsBackFloorAndDependentEvidence(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	before, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	q := RefreshRequest{Source: rootPrincipal(root), Nonce: hash("graph-atomic-issue")}
	sentinel := errors.New("dependent graph rejected")
	issued, e := d.s.IssueChecked(d.ctx, q, func(ctx context.Context, tx *sql.Tx, b DirectoryIssue) (DirectoryDependent, error) {
		if b.Snapshot.Nonce != q.Nonce || b.Access.Local != d.self || b.Access.Origin != d.self || b.Boot == "" {
			t.Fatal("issue callback lacks exact private authority", b)
		}
		if _, e := tx.ExecContext(ctx, `CREATE TABLE graph_issue_effect(value TEXT NOT NULL)`); e != nil {
			return DirectoryDependent{}, e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO graph_issue_effect(value) VALUES('must rollback')`); e != nil {
			return DirectoryDependent{}, e
		}
		return DirectoryDependent{}, sentinel
	})
	if !errors.Is(e, sentinel) || issued.Hash != "" {
		t.Fatal("dependent failure returned issued authority", issued, e)
	}
	after, e := d.s.Read(d.ctx)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed issue changed membership/floor/counters", e)
	}
	var n int
	if e = d.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='graph_issue_effect'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("dependent schema committed on failed issue", n, e)
	}
}
func TestDirectoryIssueCheckedFinalClockExpiryRollsBackBundle(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	before, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.s.IssueChecked(d.ctx, RefreshRequest{Source: rootPrincipal(root), Nonce: hash("late-graph")}, func(ctx context.Context, tx *sql.Tx, issue DirectoryIssue) (DirectoryDependent, error) {
		if _, e := tx.ExecContext(ctx, `CREATE TABLE graph_late_effect(value TEXT)`); e != nil {
			return DirectoryDependent{}, e
		}
		d.clock.advance(root.Policy.MembershipTTL+time.Second, root.Policy.MembershipTTL+time.Second)
		return DirectoryDependent{Hash: strings.Repeat("a", 64), Issued: issue.Snapshot.Issued, ValidUntil: issue.Snapshot.ValidUntil}, nil
	})
	if e == nil {
		t.Fatal("expired directory bundle committed")
	}
	after, e := d.s.Read(d.ctx)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("expired issue changed issuance evidence", e)
	}
}
func TestRuntimeAcceptCheckedNeverActivatesPartialGraphAndMembership(t *testing.T) {
	d, root, b, c, _, runtime, _ := routingFixtures(t)
	if _, e := b.db.Exec(`CREATE TABLE graph_accept_effect(hash TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	before, e := b.s.Read(b.ctx)
	if e != nil {
		t.Fatal(e)
	}
	q, e := runtime.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("partial graph rejected")
	e = runtime.AcceptChecked(b.ctx, d.self, snapshot, func(ctx context.Context, tx *sql.Tx, a DirectoryAcceptance) (DirectoryDependent, error) {
		if a.Snapshot.Hash != snapshot.Hash || a.Local != b.self || a.Deadline <= a.Elapsed || a.Boot == "" || a.StartWall.IsZero() {
			t.Fatal("callback lost original request-start binding", a)
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO graph_accept_effect(hash) VALUES(?)`, strings.Repeat("a", 64))
		if e != nil {
			return DirectoryDependent{}, e
		}
		return DirectoryDependent{}, sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	after, e := b.s.Read(b.ctx)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial accept changed membership/floor/observation", e)
	}
	if _, e = runtime.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage); e == nil {
		t.Fatal("partial graph activated membership alone")
	}
	var n int
	if e = b.db.QueryRow(`SELECT count(*) FROM graph_accept_effect`).Scan(&n); e != nil || n != 0 {
		t.Fatal("partial graph SQL escaped rollback", n, e)
	}
	q, e = runtime.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e = d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	graph := strings.Repeat("b", 64)
	e = runtime.AcceptChecked(b.ctx, d.self, snapshot, func(ctx context.Context, tx *sql.Tx, _ DirectoryAcceptance) (DirectoryDependent, error) {
		_, e := tx.ExecContext(ctx, `INSERT INTO graph_accept_effect(hash) VALUES(?)`, graph)
		return DirectoryDependent{Hash: graph, Issued: snapshot.Issued, ValidUntil: snapshot.ValidUntil}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	a, e := runtime.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage)
	if e != nil || a.GraphHash != graph {
		t.Fatal("committed graph/member hashes were not activated together", a, e)
	}
}
func TestRuntimeAcceptCheckedCloseCancelsJoinsAndRollsBackBundle(t *testing.T) {
	d, root, b, _, _, runtime, _ := routingFixtures(t)
	if _, e := b.db.Exec(`CREATE TABLE graph_close_effect(value TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	q, e := runtime.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runtime.AcceptChecked(b.ctx, d.self, snapshot, func(ctx context.Context, tx *sql.Tx, _ DirectoryAcceptance) (DirectoryDependent, error) {
			if _, e := tx.ExecContext(ctx, `INSERT INTO graph_close_effect(value) VALUES('rollback')`); e != nil {
				return DirectoryDependent{}, e
			}
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return DirectoryDependent{Hash: strings.Repeat("c", 64), Issued: snapshot.Issued, ValidUntil: snapshot.ValidUntil}, ctx.Err()
		})
	}()
	<-entered
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close failed to cancel actual accepted SQL callback")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before owned callback joined")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close failed to join accepted bundle callback")
	}
	if e = <-result; e == nil {
		t.Fatal("closed bundle committed")
	}
	var n int
	if e = b.db.QueryRow(`SELECT count(*) FROM graph_close_effect`).Scan(&n); e != nil || n != 0 {
		t.Fatal("cancelled dependent SQL escaped", n, e)
	}
}

func TestRuntimeGraphDeadlineExpiresBeforeStillFreshMembership(t *testing.T) {
	d, root, b, c, _, runtime, _ := routingFixtures(t)
	// Fixture creation consumes real time, but these clocks advance only when
	// instructed. Align them FORWARD before beginning the1s validity experiment.
	anchor := d.clock.Wall()
	for _, node := range []fixture{b, c} {
		if node.clock.Wall().After(anchor) {
			anchor = node.clock.Wall()
		}
	}
	anchor = anchor.Add(time.Second)
	for _, node := range []fixture{d, b, c} {
		delta := anchor.Sub(node.clock.Wall())
		node.clock.advance(delta, delta)
	}
	q, e := runtime.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	graph := DirectoryDependent{Hash: strings.Repeat("d", 64), Issued: snapshot.Issued, ValidUntil: snapshot.Issued.Add(time.Second)}
	e = runtime.AcceptChecked(b.ctx, d.self, snapshot, func(context.Context, *sql.Tx, DirectoryAcceptance) (DirectoryDependent, error) { return graph, nil })
	if e != nil {
		t.Fatal(e)
	}
	a, e := runtime.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage)
	if e != nil || a.GraphHash != graph.Hash {
		t.Fatal(a, e)
	}
	b.clock.advance(2*time.Second, 2*time.Second)
	a, e = runtime.RouteAccess(b.ctx, root.ID, b.self.Host, c.self.Host, RouteMessage)
	if e != nil || a.GraphHash != "" {
		t.Fatal("graph inherited still-fresh membership deadline", a, e)
	}
}
func TestRuntimeAcceptCheckedFinalGraphExpiryRollsBackBothLedgers(t *testing.T) {
	d, root, b, _, _, runtime, _ := routingFixtures(t)
	before, e := b.s.Read(b.ctx)
	if e != nil {
		t.Fatal(e)
	}
	q, e := runtime.Begin(b.ctx, root.ID)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.s.Issue(d.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	e = runtime.AcceptChecked(b.ctx, d.self, snapshot, func(ctx context.Context, tx *sql.Tx, _ DirectoryAcceptance) (DirectoryDependent, error) {
		if _, e := tx.ExecContext(ctx, `CREATE TABLE graph_expiring_bundle(value TEXT)`); e != nil {
			return DirectoryDependent{}, e
		}
		b.clock.advance(2*time.Second, 2*time.Second)
		return DirectoryDependent{Hash: strings.Repeat("e", 64), Issued: snapshot.Issued, ValidUntil: snapshot.Issued.Add(time.Second)}, nil
	})
	if e == nil {
		t.Fatal("graph expired inside callback but membership committed")
	}
	after, e := b.s.Read(b.ctx)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("expired graph changed membership/floor", e)
	}
	var n int
	if e = b.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='graph_expiring_bundle'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("expired graph SQL committed", n, e)
	}
}

func TestDirectoryIssueCheckedFinalGraphDeadlineShorterThanMembership(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	before, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	d.s.fault = func(stage string) error {
		if stage == "before_commit" {
			d.clock.advance(2*time.Second, 2*time.Second)
		}
		return nil
	}
	_, e = d.s.IssueChecked(d.ctx, RefreshRequest{Source: rootPrincipal(root), Nonce: hash("graph-final-short")}, func(ctx context.Context, tx *sql.Tx, b DirectoryIssue) (DirectoryDependent, error) {
		if _, e := tx.ExecContext(ctx, `CREATE TABLE graph_short_issue(value TEXT)`); e != nil {
			return DirectoryDependent{}, e
		}
		return DirectoryDependent{Hash: strings.Repeat("f", 64), Issued: b.Snapshot.Issued, ValidUntil: b.Snapshot.Issued.Add(time.Second)}, nil
	})
	d.s.fault = nil
	if e == nil {
		t.Fatal("final graph deadline expired while membership remained fresh")
	}
	after, e := d.s.Read(d.ctx)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("short graph final guard failed to roll back membership", e)
	}
	var n int
	if e = d.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='graph_short_issue'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("dependent SQL committed", n, e)
	}
}
