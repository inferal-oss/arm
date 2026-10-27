package routing

import (
	"context"
	"database/sql"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

// GraphHints is reachability information, never hop or endpoint authorization.
// ReachabilityOnly is mandatory for saved observations after cache expiry or
// restart, and those candidates may target ONLY the exact pinned directory.
type GraphHints struct {
	Snapshot         workspacegraph.Snapshot
	Paths            [][]string
	ReachabilityOnly bool
	DirectoryProbes  []DirectoryProbe
}

func ReadGraphHints(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, directoryProbe bool) (GraphHints, error) {
	if e := requireScope(a); e != nil {
		return GraphHints{}, e
	}
	if a.Local != a.Origin {
		return GraphHints{}, model.Err("forbidden", "only native original source may select a graph path")
	}
	v, e := Read(ctx, tx)
	if e != nil {
		return GraphHints{}, e
	}
	g, ok := graphIssuer(v, a.Workspace)
	if !ok || g.Closed || g.Authority != authority(a) || g.Generation != a.Generation || g.LocalPin != a.Local.Fingerprint {
		return GraphHints{}, model.Err("graph_unavailable", "no matching local graph observation")
	}
	var s workspacegraph.Snapshot
	var probes []DirectoryProbe
	stale := false
	if g.Role == "directory" {
		if g.LastIssue == nil || g.LastIssue.Membership.Revision != a.DirectoryRevision || a.GraphHash == "" || a.GraphHash != g.LastIssue.Snapshot.Hash {
			return GraphHints{}, model.Err("graph_unavailable", "directory graph needs a current process-bound complete issue")
		}
		s = g.LastIssue.Snapshot
		if e = s.Validate(a.At); e != nil {
			return GraphHints{}, model.Err("graph_unavailable", "directory graph is expired")
		}
	} else {
		for _, o := range v.Graph.Observations {
			if o.Workspace == a.Workspace {
				s = o.Bundle.Snapshot
				if directoryProbe && a.Destination == a.Directory {
					probes = append(probes, o.DirectoryProbes...)
				}
				break
			}
		}
		if s.Hash == "" {
			return GraphHints{}, model.Err("graph_unavailable", "no complete saved directory graph")
		}
		current := a.GraphHash != "" && a.GraphHash == s.Hash && s.Validate(a.At) == nil
		if !current {
			if !directoryProbe || a.Destination != a.Directory {
				return GraphHints{}, model.Err("graph_stale", "fresh process-bound graph required for message/lookup paths")
			}
			stale = true
		}
	}
	if directoryProbe && a.Destination != a.Directory {
		return GraphHints{}, model.Err("forbidden", "saved graph probe may target only exact pinned directory")
	}
	own, dir := false, false
	for _, n := range s.Nodes {
		if n.Host == a.Local.Host && n.Pin == a.Local.Fingerprint && n.Generation == a.OriginGeneration {
			own = true
		}
		if n.Host == a.Directory.Host && n.Pin == a.Directory.Fingerprint {
			dir = true
		}
	}
	if !own || !dir || s.Workspace != a.Workspace || s.Generation != a.Generation || s.Directory != a.Directory.Host || s.DirectoryPin != a.Directory.Fingerprint {
		return GraphHints{}, model.Err("identity_changed", "saved graph differs from exact current self/directory binding")
	}
	at := a.At
	if stale {
		at = s.Issued
	} // Historical topology only, NOT renewed freshness.
	paths, e := s.Paths(at, a.Local.Host, a.Destination.Host)
	if e != nil {
		return GraphHints{}, e
	}
	return GraphHints{Snapshot: s, Paths: paths, ReachabilityOnly: stale, DirectoryProbes: probes}, nil
}
