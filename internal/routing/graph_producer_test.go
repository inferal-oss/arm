package routing

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func ownGraphReport(t *testing.T, id string, n, d *msgNode, neighbors []workspacegraph.Neighbor) (workspace.RefreshRequest, workspacegraph.Report) {
	t.Helper()
	q, e := n.runtime.Begin(t.Context(), id)
	if e != nil {
		t.Fatal(e)
	}
	var report workspacegraph.Report
	e = n.runtime.RefreshTransaction(t.Context(), q, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart) (workspace.DirectoryDependent, error) {
		var e error
		report, e = PrepareGraphReport(ctx, tx, a, start, neighbors)
		return workspace.DirectoryDependent{Hash: GraphReportDigest(report), Issued: report.Issued, ValidUntil: report.ValidUntil}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	return q, report
}
func issueGraphReport(t *testing.T, d *msgNode, q workspace.RefreshRequest, report workspacegraph.Report, neighbors []workspacegraph.Neighbor, live []workspacegraph.Observation) (workspace.Snapshot, workspacegraph.Snapshot, []workspacegraph.Observation) {
	t.Helper()
	var graph workspacegraph.Snapshot
	var next []workspacegraph.Observation
	m, e := d.store.IssueChecked(t.Context(), q, func(ctx context.Context, tx *sql.Tx, b workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
		var e error
		graph, next, e = IssueGraph(ctx, tx, b, report, neighbors, live)
		return workspace.DirectoryDependent{Hash: graph.Hash, Issued: graph.Issued, ValidUntil: graph.ValidUntil}, e
	})
	if e != nil {
		t.Fatal(e)
	}
	return m, graph, next
}
func acceptGraphReport(t *testing.T, n, d *msgNode, m workspace.Snapshot, g workspacegraph.Snapshot) {
	t.Helper()
	if e := n.runtime.AcceptChecked(t.Context(), d.identity, m, func(ctx context.Context, tx *sql.Tx, b workspace.DirectoryAcceptance) (workspace.DirectoryDependent, error) {
		return AcceptGraph(ctx, tx, b, g)
	}); e != nil {
		t.Fatal(e)
	}
}
func graphNeighbor(n *msgNode, pair string) workspacegraph.Neighbor {
	return workspacegraph.Neighbor{Host: n.identity.Host, Generation: 1, Pin: n.identity.Fingerprint, Pair: pair}
}

func TestActualGraphProducerAtomicBundleAndRestartDisplayOnly(t *testing.T) {
	id, d, b, c := messageFixture(t)
	ab, ac := strings.Repeat("a", 64), strings.Repeat("c", 64)
	q, r := ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{graphNeighbor(d, ab)})
	m, g, live := issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{graphNeighbor(b, ab), graphNeighbor(c, ac)}, nil)
	acceptGraphReport(t, b, d, m, g)
	q, r = ownGraphReport(t, id, c, d, []workspacegraph.Neighbor{graphNeighbor(d, ac)})
	m, g, live = issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{graphNeighbor(b, ab), graphNeighbor(c, ac)}, live)
	acceptGraphReport(t, c, d, m, g)
	q, r = ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{graphNeighbor(d, ab)})
	m, g, _ = issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{graphNeighbor(b, ab), graphNeighbor(c, ac)}, live)
	acceptGraphReport(t, b, d, m, g)
	if len(g.Edges) != 2 {
		t.Fatal("mutual adjacency incomplete", g)
	}
	paths, e := g.Paths(time.Now().UTC(), b.identity.Host, c.identity.Host)
	if e != nil || len(paths) != 1 || !reflect.DeepEqual(paths[0], []string{b.identity.Host, d.identity.Host, c.identity.Host}) {
		t.Fatal(paths, e)
	}
	a, e := b.runtime.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage)
	if e != nil || a.GraphHash != g.Hash {
		t.Fatal("graph/member hashes did not activate together", a, e)
	}
	v := readMessageRecords(t, b)
	if v.Graph == nil || len(v.Graph.Observations) != 1 || v.Graph.Observations[0].SourceReport.Nonce != q.Nonce {
		t.Fatal("historical source report missing", v.Graph)
	}
	b.runtime.Close()
	replacement := workspace.NewRuntime(b.store)
	defer replacement.Close()
	if _, e = replacement.RouteAccess(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage); e == nil {
		t.Fatal("restart promoted persisted graph/membership")
	}
	if _, e = replacement.RouteAccess(t.Context(), id, b.identity.Host, d.identity.Host, workspace.RouteDirectory); e != nil {
		t.Fatal("restart lost reachability-only directory refresh", e)
	}
}
func TestActualGraphProducerCallbackFailureRollsBackBothLedgers(t *testing.T) {
	id, d, b, _ := messageFixture(t)
	q, r := ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{})
	before, e := d.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	sentinel := errors.New("reject whole graph after typed producer")
	_, e = d.store.IssueChecked(t.Context(), q, func(ctx context.Context, tx *sql.Tx, bundle workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
		if _, _, e := IssueGraph(ctx, tx, bundle, r, []workspacegraph.Neighbor{}, nil); e != nil {
			return workspace.DirectoryDependent{}, e
		}
		return workspace.DirectoryDependent{}, sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	after, e := d.store.Read(t.Context())
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("graph error changed membership floors", e)
	}
	records := readMessageRecords(t, d)
	if records.Initialized || records.Graph != nil {
		t.Fatal("failed graph issue initialized dependent ledger", records)
	}
}
func TestActualGraphProducerHighwaterAndOriginalDeadline(t *testing.T) {
	id, d, b, _ := messageFixture(t)
	q, r := ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{})
	_, _, live := issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{}, nil)
	before := readMessageRecords(t, d)
	r.Sequence--
	if r.Sequence == 0 {
		r.Sequence = 1
		r.Neighbors = []workspacegraph.Neighbor{{Host: d.identity.Host, Generation: 1, Pin: d.identity.Fingerprint, Pair: strings.Repeat("f", 64)}}
	}
	_, e := d.store.IssueChecked(t.Context(), q, func(ctx context.Context, tx *sql.Tx, bundle workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
		_, _, e := IssueGraph(ctx, tx, bundle, r, []workspacegraph.Neighbor{}, live)
		return workspace.DirectoryDependent{}, e
	})
	if e == nil {
		t.Fatal("changed same-sequence report accepted")
	}
	if !reflect.DeepEqual(before, readMessageRecords(t, d)) {
		t.Fatal("rejected report changed graph highwater")
	}
	// An exact replay may not rejuvenate a process witness whose original
	// monotonic deadline has passed while the absolute wall time is unchanged.
	r = before.Graph.Reports[0].Report
	if r.Source != b.identity.Host {
		r = before.Graph.Reports[1].Report
	}
	for i := range live {
		if live[i].Report.Source == b.identity.Host {
			live[i].Deadline = d.clock.Elapsed()
		}
	}
	_, e = d.store.IssueChecked(t.Context(), q, func(ctx context.Context, tx *sql.Tx, bundle workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
		_, _, e := IssueGraph(ctx, tx, bundle, r, []workspacegraph.Neighbor{}, live)
		return workspace.DirectoryDependent{}, e
	})
	if e == nil {
		t.Fatal("same report renewed original monotonic deadline")
	}
	if !reflect.DeepEqual(before, readMessageRecords(t, d)) {
		t.Fatal("expired replay changed graph highwater")
	}
}

func TestActualGraphIssueExpiryRollsBackMembershipAndTypedGraphRecords(t *testing.T) {
	id, d, b, _ := messageFixture(t)
	pair := strings.Repeat("a", 64)
	q, r := ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{graphNeighbor(d, pair)})
	before, e := d.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.store.IssueChecked(t.Context(), q, func(ctx context.Context, tx *sql.Tx, bundle workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
		graph, _, e := IssueGraph(ctx, tx, bundle, r, []workspacegraph.Neighbor{graphNeighbor(b, pair)}, nil)
		if e != nil {
			return workspace.DirectoryDependent{}, e
		}
		d.clock.advance(31 * time.Second)
		return workspace.DirectoryDependent{Hash: graph.Hash, Issued: graph.Issued, ValidUntil: graph.ValidUntil}, nil
	})
	if e == nil {
		t.Fatal("expired typed graph bundle committed")
	}
	after, e := d.store.Read(t.Context())
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("expired typed graph changed membership/floors", e)
	}
	if v := readMessageRecords(t, d); v.Initialized || v.Graph != nil {
		t.Fatal("expired typed graph initialized records", v)
	}
}

func TestActualDirectoryGraphReadRejectsLostEarlierSourceHighwater(t *testing.T) {
	id, d, b, c := messageFixture(t)
	q, r := ownGraphReport(t, id, b, d, []workspacegraph.Neighbor{})
	_, _, live := issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{}, nil)
	q, r = ownGraphReport(t, id, c, d, []workspacegraph.Neighbor{})
	issueGraphReport(t, d, q, r, []workspacegraph.Neighbor{}, live)
	v := readMessageRecords(t, d)
	if len(v.Graph.Issuance) != 1 || len(v.Graph.Issuance[0].ReportRefs) != 3 {
		t.Fatal("complete bounded source inventory missing", v.Graph)
	}
	if _, e := d.db.Exec(`DELETE FROM routing_graph_reports WHERE workspace=? AND source=?`, id, b.identity.Host); e != nil {
		t.Fatal(e)
	}
	tx, e := d.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = Read(t.Context(), tx); e == nil {
		t.Fatal("lost earlier source silently reset its retained highwater")
	}
}
