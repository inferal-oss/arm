package manager

import (
	"context"
	"database/sql"
	"math"
	"sort"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

func (m *Manager) WorkspaceGraphHints(ctx context.Context, id, target string, directoryProbe bool) (routing.GraphHints, error) {
	class := workspace.RouteMessage
	if directoryProbe {
		class = workspace.RouteDirectory
	}
	return routeCall(ctx, m, id, m.host, target, class, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (routing.GraphHints, error) {
		return routing.ReadGraphHints(ctx, tx, a, directoryProbe)
	})
}

// WorkspaceIssueLocalGraph is native directory authority only. It does not
// pretend that a remote JSON principal authenticated as this node. The exact
// self principal is derived here; IssueChecked rechecks it in the write tx.
func (m *Manager) WorkspaceIssueLocalGraph(ctx context.Context, id string, neighbors []workspacegraph.Neighbor, live []workspacegraph.Observation) (routing.GraphBundle, []workspacegraph.Observation, error) {
	type issued struct {
		bundle routing.GraphBundle
		live   []workspacegraph.Observation
	}
	out, e := workspaceCall(ctx, m, true, func(ctx context.Context, _ *workspaceBoundary, s *workspace.Store) (issued, error) {
		records, e := s.Read(ctx)
		if e != nil {
			return issued{}, e
		}
		var root workspace.Instance
		for _, r := range records.Instances {
			if r.ID == id {
				root = r
			}
		}
		if root.ID == "" || root.Role != "directory" || root.Authority.Host != m.host {
			return issued{}, model.Err("forbidden", "only this native node's current directory may issue local graph")
		}
		q := workspace.RefreshRequest{Source: workspace.Principal{Workspace: id, Generation: root.Generation, Host: m.host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}, Nonce: local.ID() + local.ID()}
		var result issued
		snapshot, e := s.IssueChecked(ctx, q, func(ctx context.Context, tx *sql.Tx, b workspace.DirectoryIssue) (workspace.DirectoryDependent, error) {
			v, e := routing.Read(ctx, tx)
			if e != nil {
				return workspace.DirectoryDependent{}, e
			}
			sequence := int64(1)
			if v.Graph != nil {
				for _, g := range v.Graph.Issuance {
					if g.Workspace == id {
						if g.ReportSequence == math.MaxInt64 {
							return workspace.DirectoryDependent{}, model.Err("routing_capacity_exhausted", "graph source sequence exhausted")
						}
						sequence = g.ReportSequence + 1
					}
				}
			}
			report := workspacegraph.Report{Schema: 1, Workspace: id, Generation: b.Snapshot.Generation, Source: m.host, SourceGeneration: 1, SourcePin: b.Access.Local.Fingerprint, Sequence: sequence, Nonce: q.Nonce, Issued: b.Snapshot.Issued, ValidUntil: b.Snapshot.ValidUntil, Neighbors: append([]workspacegraph.Neighbor{}, neighbors...)}
			sort.Slice(report.Neighbors, func(i, j int) bool { return report.Neighbors[i].Host < report.Neighbors[j].Host })
			result.bundle.Snapshot, result.live, e = routing.IssueGraph(ctx, tx, b, report, neighbors, live)
			return workspace.DirectoryDependent{Hash: result.bundle.Snapshot.Hash, Issued: result.bundle.Snapshot.Issued, ValidUntil: result.bundle.Snapshot.ValidUntil}, e
		})
		if e != nil {
			return issued{}, e
		}
		result.bundle.Membership = snapshot
		return result, nil
	})
	return out.bundle, out.live, e
}

// WorkspaceDirectoryNextHop is internal native reachability inspection only.
func (m *Manager) WorkspaceDirectoryNextHop(ctx context.Context, id, next string) (workspace.Identity, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Identity, error) {
		return w.runtime.DirectoryNextHop(ctx, id, next)
	})
}

func (m *Manager) WorkspaceDirectoryNextHopPinned(ctx context.Context, id, next string, generation int64, pin string) (workspace.Identity, error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (workspace.Identity, error) {
		return w.runtime.DirectoryNextHopPinned(ctx, id, next, generation, pin)
	})
}
