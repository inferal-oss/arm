package routing

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"getarm.org/internal/workspace"
	"getarm.org/internal/workspacegraph"
)

func TestActualSourceGraphReportFinalCommitUsesOriginalPendingRefresh(t *testing.T) {
	for _, advance := range []time.Duration{31 * time.Second, time.Minute + time.Second} {
		t.Run(advance.String(), func(t *testing.T) {
			id, _, b, _ := messageFixture(t)
			before, e := b.store.Read(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			q, e := b.runtime.Begin(t.Context(), id)
			if e != nil {
				t.Fatal(e)
			}
			e = b.runtime.RefreshTransaction(t.Context(), q, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart) (workspace.DirectoryDependent, error) {
				report, e := PrepareGraphReport(ctx, tx, a, start, []workspacegraph.Neighbor{})
				if e != nil {
					return workspace.DirectoryDependent{}, e
				}
				b.clock.advance(advance)
				return workspace.DirectoryDependent{Hash: GraphReportDigest(report), Issued: report.Issued, ValidUntil: report.ValidUntil}, nil
			})
			if e == nil {
				t.Fatal("source report/schema/counters committed after ORIGINAL refresh and report expiry")
			}
			after, e := b.store.Read(t.Context())
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("expired source report changed parent evidence", e)
			}
			if v := readMessageRecords(t, b); v.Initialized || v.Graph != nil {
				t.Fatal("expired source report initialized graph evidence", v)
			}
		})
	}

}

func TestActualSourceGraphReportConsumedTicketRollsBackBeforeConcurrentAccept(t *testing.T) {
	id, d, b, c := messageFixture(t)
	q, e := b.runtime.Begin(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := d.store.Issue(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	prepared := make(chan error, 1)
	go func() {
		prepared <- b.runtime.RefreshTransaction(t.Context(), q, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart) (workspace.DirectoryDependent, error) {
			report, e := PrepareGraphReport(ctx, tx, a, start, []workspacegraph.Neighbor{})
			if e != nil {
				return workspace.DirectoryDependent{}, e
			}
			close(entered)
			<-release
			return workspace.DirectoryDependent{Hash: GraphReportDigest(report), Issued: report.Issued, ValidUntil: report.ValidUntil}, nil
		})
	}()
	<-entered
	accepted := make(chan error, 1)
	go func() { accepted <- b.runtime.Accept(t.Context(), d.identity, snapshot) }()
	until := time.NewTimer(2 * time.Second)
	defer until.Stop()
	for {
		if _, e = b.runtime.RefreshStart(q); e != nil {
			break
		}
		select {
		case <-until.C:
			close(release)
			t.Fatal("concurrent accept did not consume exact ticket")
		case <-time.After(time.Millisecond):
		}
	}
	close(release)
	if e = <-prepared; e == nil {
		t.Fatal("source preparation committed after Accept consumed its ticket")
	}
	if e = <-accepted; e != nil {
		t.Fatal("independent valid legacy membership acceptance failed", e)
	}
	if v := readMessageRecords(t, b); v.Initialized || v.Graph != nil {
		t.Fatal("consumed source report retained graph schema/counters/events", v)
	}
	a, e := b.runtime.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage)
	if e != nil || a.GraphHash != "" {
		t.Fatal("partial graph activated alongside concurrent membership", a, e)
	}
}
func TestActualSourceGraphReportCloseCancelsAndJoinsRollback(t *testing.T) {
	id, _, b, _ := messageFixture(t)
	q, e := b.runtime.Begin(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	before, e := b.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	prepared := make(chan error, 1)
	go func() {
		prepared <- b.runtime.RefreshTransaction(t.Context(), q, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart) (workspace.DirectoryDependent, error) {
			if _, e := PrepareGraphReport(ctx, tx, a, start, []workspacegraph.Neighbor{}); e != nil {
				return workspace.DirectoryDependent{}, e
			}
			close(entered)
			<-ctx.Done()
			close(cancelled)
			<-release
			return workspace.DirectoryDependent{}, ctx.Err()
		})
	}()
	<-entered
	closed := make(chan struct{})
	go func() { b.runtime.Close(); close(closed) }()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Close failed to cancel source producer context")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before source callback joined")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not join source callback")
	}
	if e = <-prepared; e == nil {
		t.Fatal("source graph committed after runtime Close")
	}
	after, e := b.store.Read(t.Context())
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("source Close changed workspace evidence", e)
	}
	if v := readMessageRecords(t, b); v.Initialized || v.Graph != nil {
		t.Fatal("source Close committed graph schema/rows/events", v)
	}
}
