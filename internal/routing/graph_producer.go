package routing

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func graphMutation(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, role string) (Records, GraphIssuance, error) {
	v, r, e := mutation(ctx, tx, a, true)
	if e != nil {
		return v, GraphIssuance{}, e
	}
	if e = graphSchema(ctx, tx); e != nil {
		return v, GraphIssuance{}, e
	}
	if g, ok := graphIssuer(v, a.Workspace); ok {
		if g.Closed || g.Role != role || a.At.Before(g.WallFloor) {
			return v, g, model.Err("routing_clock_unavailable", "graph authority is closed, changed or ahead of current clock")
		}
		return v, g, nil
	}
	g := GraphIssuance{Schema: 1, Workspace: r.Workspace, Generation: r.Generation, Authority: r.Authority, LocalPin: r.LocalPin, Role: role, ReportRefs: []GraphReportRef{}, EnableRevision: r.Revision + 1, EnabledAt: a.At, Updated: a.At, WallFloor: a.At}
	if r.Revision == math.MaxInt64 {
		return v, g, model.Err("routing_capacity_exhausted", "graph enablement revision exhausted")
	}
	if e = writeGraphIssuer(ctx, tx, g); e != nil {
		return v, g, e
	}
	if e = advance(ctx, tx, r, a.At, Change{Kind: "graph_enabled", Actor: "uid:" + r.Authority.UID, Phase: "active"}); e != nil {
		return v, g, e
	}
	v, e = Read(ctx, tx)
	return v, g, e
}
func graphNext(n int64) (int64, error) {
	if n == math.MaxInt64 {
		return 0, model.Err("routing_capacity_exhausted", "graph sequence exhausted")
	}
	return n + 1, nil
}
func minGraphTime(v time.Time, others ...time.Time) time.Time {
	for _, n := range others {
		if n.Before(v) {
			v = n
		}
	}
	return v
}
func sortedNeighbors(v []workspacegraph.Neighbor) []workspacegraph.Neighbor {
	out := append([]workspacegraph.Neighbor{}, v...)
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// PrepareGraphReport persists one exact original-request report BEFORE network
// transmission. This Go-only callback belongs inside a current RouteTransaction
// and consumes the Runtime's original pending refresh start. It does not grant
// authority to a path, or let a caller assert a different report source.
func PrepareGraphReport(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart, neighbors []workspacegraph.Neighbor) (workspacegraph.Report, error) {
	q := start.Request
	if a.Local != a.Origin || a.Destination != a.Directory || q.Source.Workspace != a.Workspace || q.Source.Generation != a.Generation || q.Source.Host != a.Local.Host || q.Source.Fingerprint != a.Local.Fingerprint || q.Source.MembershipGeneration != a.OriginGeneration || !hexID(q.Nonce, 32) || start.Boot == "" || start.Wall.IsZero() || start.Wall.After(a.At) || start.Elapsed < 0 || start.Lifetime <= 0 || a.At.Sub(start.Wall) >= min(start.Lifetime, workspacegraph.MaxTTL) {
		return workspacegraph.Report{}, model.Err("stale_refresh", "exact current source and original refresh start required for graph report")
	}
	_, g, e := graphMutation(ctx, tx, a, "member")
	if e != nil {
		return workspacegraph.Report{}, e
	}
	if old := g.LastReport; old != nil && old.Nonce == q.Nonce {
		test := *old
		test.Neighbors = sortedNeighbors(neighbors)
		if hash(test) != g.ReportHash {
			return workspacegraph.Report{}, model.Err("idempotency_conflict", "refresh nonce already has another exact graph report")
		}
		if !old.ValidUntil.After(a.At) {
			return workspacegraph.Report{}, model.Err("stale_refresh", "original graph report already expired")
		}
		return *old, nil
	}
	seq, e := graphNext(g.ReportSequence)
	if e != nil {
		return workspacegraph.Report{}, e
	}
	report := workspacegraph.Report{Schema: 1, Workspace: a.Workspace, Generation: a.Generation, Source: a.Local.Host, SourceGeneration: a.OriginGeneration, SourcePin: a.Local.Fingerprint, Sequence: seq, Nonce: q.Nonce, Issued: start.Wall, ValidUntil: minGraphTime(start.Wall.Add(min(start.Lifetime, workspacegraph.MaxTTL)), a.Expires), Neighbors: sortedNeighbors(neighbors)}
	if e = report.Validate(a.At); e != nil {
		return workspacegraph.Report{}, model.Err("invalid_request", e.Error())
	}
	g.ReportSequence = seq
	g.LastReport = &report
	g.ReportHash = hash(report)
	g.Updated = a.At
	g.WallFloor = a.At
	if e = writeGraphIssuer(ctx, tx, g); e != nil {
		return workspacegraph.Report{}, e
	}
	if _, e = Read(ctx, tx); e != nil {
		return workspacegraph.Report{}, e
	}
	return report, nil
}

// IssueGraph runs ONLY as the Go callback of Store.IssueChecked. It commits the
// incoming source report, directory's own current adjacency and complete graph
// WITH that exact membership floor. Live witnesses must originate from earlier
// successful authenticated calls; retained JSON alone is not a live witness.
// Return values must be published by the caller ONLY AFTER IssueChecked commits.
func IssueGraph(ctx context.Context, tx *sql.Tx, b workspace.DirectoryIssue, incoming workspacegraph.Report, selfNeighbors []workspacegraph.Neighbor, live []workspacegraph.Observation) (workspacegraph.Snapshot, []workspacegraph.Observation, error) {
	a, m := b.Access, b.Snapshot
	if a.Local != a.Directory || m.Directory != a.Local || m.Requester != a.Origin.Host || m.Nonce != incoming.Nonce || incoming.Source != a.Origin.Host || incoming.SourcePin != a.Origin.Fingerprint || incoming.SourceGeneration != a.OriginGeneration || incoming.Workspace != a.Workspace || incoming.Generation != a.Generation || incoming.Validate(a.At) != nil || b.Boot == "" || b.Elapsed < 0 || len(live) > workspacegraph.MaxNodes {
		return workspacegraph.Snapshot{}, nil, model.Err("forbidden", "graph report differs from authenticated original source/current membership request")
	}
	v, g, e := graphMutation(ctx, tx, a, "directory")
	if e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	row := GraphReport{Schema: 1, Workspace: a.Workspace, Authority: g.Authority, DirectoryPin: g.LocalPin, Report: incoming, Hash: hash(incoming), Received: a.At, OriginalUntil: minGraphTime(incoming.ValidUntil, a.At.Add(workspacegraph.MaxTTL), a.Expires)}
	for _, old := range v.Graph.Reports {
		if old.Workspace == a.Workspace && old.Report.Source == incoming.Source {
			if incoming.Sequence < old.Report.Sequence {
				return workspacegraph.Snapshot{}, nil, model.Err("stale_revision", "source graph report sequence moved backwards")
			}
			if incoming.Sequence == old.Report.Sequence {
				if old.Hash != row.Hash {
					return workspacegraph.Snapshot{}, nil, model.Err("idempotency_conflict", "source graph sequence changed its immutable report")
				}
				row = old
			}
		}
	}
	if !row.OriginalUntil.After(a.At) {
		return workspacegraph.Snapshot{}, nil, model.Err("stale_refresh", "duplicate report cannot extend its ORIGINAL validity")
	}
	sourceSeq, e := graphNext(g.ReportSequence)
	if e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	own := workspacegraph.Report{Schema: 1, Workspace: a.Workspace, Generation: a.Generation, Source: a.Local.Host, SourceGeneration: a.DestinationGeneration, SourcePin: a.Local.Fingerprint, Sequence: sourceSeq, Nonce: m.Nonce, Issued: a.At, ValidUntil: minGraphTime(a.At.Add(workspacegraph.MaxTTL), m.ValidUntil), Neighbors: sortedNeighbors(selfNeighbors)}
	if e = own.Validate(a.At); e != nil {
		return workspacegraph.Snapshot{}, nil, model.Err("invalid_request", e.Error())
	}
	ownRow := GraphReport{Schema: 1, Workspace: a.Workspace, Authority: g.Authority, DirectoryPin: g.LocalPin, Report: own, Hash: hash(own), Received: a.At, OriginalUntil: own.ValidUntil}
	expected := map[string]string{}
	for _, r := range v.Graph.Reports {
		if r.Workspace == a.Workspace {
			expected[r.Report.Source] = r.Hash
		}
	}
	expected[incoming.Source] = row.Hash
	expected[a.Local.Host] = ownRow.Hash
	current := []workspacegraph.Observation{}
	for _, o := range live {
		if o.Boot == b.Boot && o.Deadline > b.Elapsed && expected[o.Report.Source] == hash(o.Report) {
			current = append(current, o)
		}
	}
	// Replace same-source witnesses, rather than refreshing an existing deadline.
	filtered := current[:0]
	for _, o := range current {
		if o.Report.Source != incoming.Source && o.Report.Source != a.Local.Host {
			filtered = append(filtered, o)
		}
	}
	deadline := b.Elapsed + row.OriginalUntil.Sub(a.At)
	for _, old := range live {
		if old.Boot == b.Boot && hash(old.Report) == row.Hash {
			deadline = min(deadline, old.Deadline)
		}
	}
	if deadline <= b.Elapsed {
		return workspacegraph.Snapshot{}, nil, model.Err("stale_refresh", "same report cannot renew an elapsed original process deadline")
	}
	current = filtered
	if incoming.Source != a.Local.Host {
		current = append(current, workspacegraph.Observation{Report: row.Report, Boot: b.Boot, Deadline: deadline})
	}
	current = append(current, workspacegraph.Observation{Report: own, Boot: b.Boot, Deadline: b.Elapsed + own.ValidUntil.Sub(a.At)})
	issueSeq, e := graphNext(g.IssuedCount)
	if e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	graph, e := workspacegraph.Build(GraphScope(m, issueSeq), current, b.Boot, b.Elapsed)
	if e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	bundle := GraphBundle{Membership: m, Snapshot: graph}
	if e = validateGraphBundle(bundle); e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	refs := map[string]GraphReportRef{}
	for _, r := range v.Graph.Reports {
		if r.Workspace == a.Workspace {
			refs[r.Report.Source] = GraphReportRef{Source: r.Report.Source, Sequence: r.Report.Sequence, Hash: r.Hash}
		}
	}
	for _, r := range []GraphReport{row, ownRow} {
		refs[r.Report.Source] = GraphReportRef{Source: r.Report.Source, Sequence: r.Report.Sequence, Hash: r.Hash}
	}
	g.ReportRefs = []GraphReportRef{}
	for _, ref := range refs {
		g.ReportRefs = append(g.ReportRefs, ref)
	}
	sort.Slice(g.ReportRefs, func(i, j int) bool { return g.ReportRefs[i].Source < g.ReportRefs[j].Source })
	g.ReportSequence = sourceSeq
	g.IssuedCount = issueSeq
	g.LastReport = &own
	g.ReportHash = ownRow.Hash
	g.LastIssue = &bundle
	g.Updated = a.At
	g.WallFloor = a.At
	if e = writeGraphIssuer(ctx, tx, g); e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	for _, r := range []GraphReport{row, ownRow} {
		if e = graphPut(ctx, tx, `INSERT INTO routing_graph_reports(workspace,source,data) VALUES(?,?,?) ON CONFLICT(workspace,source) DO UPDATE SET data=excluded.data`, r, r.Workspace, r.Report.Source); e != nil {
			return workspacegraph.Snapshot{}, nil, e
		}
	}
	if _, e = Read(ctx, tx); e != nil {
		return workspacegraph.Snapshot{}, nil, e
	}
	return graph, current, nil
}

// AcceptGraph is the Go-only Runtime.AcceptChecked callback. The exact complete
// graph/member bundle is durable before the Runtime publishes their joint hash.
func AcceptGraph(ctx context.Context, tx *sql.Tx, b workspace.DirectoryAcceptance, s workspacegraph.Snapshot) (workspace.DirectoryDependent, error) {
	m := b.Snapshot
	if e := s.ValidateFor(GraphScope(m, 1)); e != nil {
		return workspace.DirectoryDependent{}, model.Err("invalid_snapshot", e.Error())
	}
	if !s.ValidUntil.After(b.At) || b.Deadline <= b.Elapsed {
		return workspace.DirectoryDependent{}, model.Err("stale_refresh", "graph bundle already expired")
	}
	a := workspace.RouteAccess{At: b.At, Workspace: m.Workspace, Generation: m.Generation, Local: b.Local, Origin: b.Local, Destination: m.Directory, Directory: m.Directory, OriginGeneration: m.RequesterGeneration, DestinationGeneration: 1, Expires: m.ValidUntil, Remaining: b.Deadline - b.Elapsed}
	v, g, e := graphMutation(ctx, tx, a, "member")
	if e != nil {
		return workspace.DirectoryDependent{}, e
	}
	if g.Authority != b.Authority || g.LastReport == nil || g.LastReport.Nonce != m.Nonce || g.LastReport.SourceGeneration != m.RequesterGeneration {
		return workspace.DirectoryDependent{}, model.Err("invalid_snapshot", "graph response lacks exact previously persisted original source report")
	}
	for _, old := range v.Graph.Observations {
		if old.Workspace == m.Workspace {
			prior := old.Bundle.Snapshot
			if s.Revision < prior.Revision || s.Revision == prior.Revision && s.Hash != prior.Hash {
				return workspace.DirectoryDependent{}, model.Err("stale_revision", "graph directory highwater moved backwards or changed")
			}
		}
	}
	o := GraphObservation{Schema: 1, Workspace: m.Workspace, Authority: g.Authority, LocalPin: g.LocalPin, SourceReport: *g.LastReport, Bundle: GraphBundle{Membership: m, Snapshot: s}, Received: b.At, RequestStart: b.StartWall, WallFloor: b.At}
	for _, old := range v.Graph.Observations {
		if old.Workspace == o.Workspace {
			// Prior records were fully validated by graphMutation/Read. Changed
			// current remote lineage/policy may invalidate a historical hint;
			// discard just that hint before validating the NEW complete bundle.
			// This filtering never runs in raw reads or destructive proof.
			for _, probe := range old.DirectoryProbes {
				candidate := o
				candidate.DirectoryProbes = []DirectoryProbe{probe}
				if validateDirectoryProbes(candidate, g) == nil {
					o.DirectoryProbes = append(o.DirectoryProbes, probe)
				}
			}
		}
	}
	g.Updated = b.At
	g.WallFloor = b.At
	if e = writeGraphIssuer(ctx, tx, g); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	if e = graphPut(ctx, tx, `INSERT INTO routing_graph_observations(workspace,data) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET data=excluded.data`, o, o.Workspace); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	if _, e = Read(ctx, tx); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	return workspace.DirectoryDependent{Hash: s.Hash, Issued: s.Issued, ValidUntil: s.ValidUntil}, nil
}
