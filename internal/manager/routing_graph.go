package manager

import (
	"context"
	"database/sql"

	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

// These typed internal adapters have no public dispatcher in this checkpoint.
// Neighbors come from the caller's current native direct registry, captured
// outside SQL/trust locks. Live witnesses may be published only after this
// method succeeds; a failed commit cannot seed an in-process topology cache.
func (m *Manager) WorkspaceGraphReport(ctx context.Context, q workspace.RefreshRequest, neighbors []workspacegraph.Neighbor) (workspacegraph.Report, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspacegraph.Report, error) {
		var out workspacegraph.Report
		e := w.runtime.RefreshTransaction(ctx, q, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, start workspace.RefreshStart) (workspace.DirectoryDependent, error) {
			var e error
			out, e = routing.PrepareGraphReport(ctx, tx, a, start, neighbors)
			return workspace.DirectoryDependent{Hash: routing.GraphReportDigest(out), Issued: out.Issued, ValidUntil: out.ValidUntil}, e
		})
		if e != nil {
			return workspacegraph.Report{}, e
		}
		return out, nil
	})
}

// WorkspaceIssueGraph is invoked only for a pinned inner-TLS original source.
// It returns a complete bundle and new witnesses only after the shared SQL
// transaction succeeds. No graph adjacency grants message/resource authority.
func (m *Manager) WorkspaceIssueGraph(ctx context.Context, q workspace.RefreshRequest, report workspacegraph.Report, neighbors []workspacegraph.Neighbor, live []workspacegraph.Observation) (routing.GraphBundle, []workspacegraph.Observation, error) {
	type issued struct {
		bundle routing.GraphBundle
		live   []workspacegraph.Observation
	}
	v, e := workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (issued, error) {
		var out issued
		membership, e := s.IssueChecked(ctx, q, func(ctx context.Context, tx *sql.Tx, b workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
			var e error
			out.bundle.Snapshot, out.live, e = routing.IssueGraph(ctx, tx, b, report, neighbors, live)
			return workspace.DirectoryDependent{Hash: out.bundle.Snapshot.Hash, Issued: out.bundle.Snapshot.Issued, ValidUntil: out.bundle.Snapshot.ValidUntil}, e
		})
		if e != nil {
			return issued{}, e
		}
		out.bundle.Membership = membership
		return out, nil
	})
	return v.bundle, v.live, e
}
func (m *Manager) WorkspaceAcceptGraph(ctx context.Context, directory workspace.Identity, b routing.GraphBundle) error {
	_, e := workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (bool, error) {
		e := w.runtime.AcceptChecked(ctx, directory, b.Membership, func(ctx context.Context, tx *sql.Tx, a workspace.DirectoryAcceptance) (workspace.DirectoryDependent, error) {
			return routing.AcceptGraph(ctx, tx, a, b.Snapshot)
		})
		return e == nil, e
	})
	return e
}
