package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	api "github.com/inferal-oss/arm/internal/workspaceapi"
	"github.com/inferal-oss/arm/internal/workspacegraph"
)

func (s *Server) workspaceGraphRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var q api.GraphRead
	if len(raw) > 60<<10 || workspace.Decode(raw, &q) != nil || q.Workspace == "" || q.Offset < 0 || q.Limit < 1 || q.Limit > 40 || q.Section != "nodes" && q.Section != "edges" && q.Section != "paths" || q.Section == "paths" && q.Target == "" || q.Section != "paths" && q.Target != "" {
		return nil, model.Err("invalid_request", "graph requires exact workspace, section nodes/edges/paths, offset>=0, limit1..40; --to only with paths")
	}
	records, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return nil, e
	}
	root, e := workspaceRoot(records, q.Workspace)
	if e != nil {
		return nil, e
	}
	v, e := s.Manager.WorkspaceRoutingRead(ctx)
	if e != nil {
		return nil, e
	}
	var snapshot workspacegraph.Snapshot
	if v.Graph != nil {
		if root.Role == "directory" {
			for _, g := range v.Graph.Issuance {
				if g.Workspace == q.Workspace && g.LastIssue != nil {
					snapshot = g.LastIssue.Snapshot
				}
			}
		} else {
			for _, o := range v.Graph.Observations {
				if o.Workspace == q.Workspace {
					snapshot = o.Bundle.Snapshot
				}
			}
		}
	}
	if snapshot.Hash == "" {
		return nil, model.Err("graph_unavailable", "no complete retained graph; explicitly refresh exact workspace")
	}
	if q.Hash != "" && q.Hash != snapshot.Hash {
		return nil, model.Err("changed_revision", "graph observation changed; restart paging with its current exact hash")
	}
	out := api.GraphPage{Workspace: q.Workspace, Section: q.Section, Hash: snapshot.Hash, MembershipHash: snapshot.MembershipHash, Revision: snapshot.Revision, ObservedAt: snapshot.Issued, ValidUntil: snapshot.ValidUntil, Authority: "reachability_only", Detail: "Saved observation only; every hop and destination independently require current authorization."}
	target := q.Target
	if target == "" {
		if root.Role == "member" {
			target = root.Directory.Host
		} else {
			for _, n := range snapshot.Nodes {
				if n.Host != root.Self.Host {
					target = n.Host
					break
				}
			}
		}
	}
	if target != "" {
		hints, err := s.Manager.WorkspaceGraphHints(ctx, q.Workspace, target, target == root.Directory.Host)
		if q.Section == "paths" && target == root.Directory.Host && err == nil {
			for _, probe := range hints.DirectoryProbes {
				path := make([]string, len(probe.Nodes))
				for i, n := range probe.Nodes {
					path[i] = n.Host
				}
				out.HistoricalDirectoryPaths = append(out.HistoricalDirectoryPaths, path)
			}
		}
		if err == nil && hints.Snapshot.Hash == snapshot.Hash && !hints.ReachabilityOnly {
			out.Current = true
			out.Detail = "Current bounded reachability observation; it grants no resource, execution or delivery permission."
			if q.Section == "paths" {
				out.Paths = hints.Paths
			}
		} else if q.Section == "paths" && target == root.Directory.Host && err == nil && hints.Snapshot.Hash == snapshot.Hash {
			out.Paths = hints.Paths
			out.Detail = "Saved paths may probe only the exact pinned directory; they do not restore membership or permit messages."
		} else if q.Section == "paths" {
			return nil, model.Err("graph_stale", "current exact graph required for message/lookup paths; refresh pinned directory first")
		}
	}
	if !time.Now().Before(snapshot.ValidUntil) {
		out.Detail = "Graph observation has expired; retained directory-only paths remain reachability probes, never authorization."
	}
	switch q.Section {
	case "nodes":
		out.Total = len(snapshot.Nodes)
		start := min(q.Offset, out.Total)
		end := min(start+q.Limit, out.Total)
		out.Nodes = snapshot.Nodes[start:end]
	case "edges":
		out.Total = len(snapshot.Edges)
		start := min(q.Offset, out.Total)
		end := min(start+q.Limit, out.Total)
		out.Edges = snapshot.Edges[start:end]
	case "paths":
		out.Total = len(out.Paths)
		start := min(q.Offset, out.Total)
		end := min(start+q.Limit, out.Total)
		out.Paths = out.Paths[start:end]
	}
	out.Offset = min(q.Offset, out.Total)
	if end := min(out.Offset+q.Limit, out.Total); end < out.Total {
		out.Next = &end
	}
	return workspaceBounded(out)
}
