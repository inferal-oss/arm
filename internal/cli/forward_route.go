package cli

import (
	"context"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
	api "getarm.org/internal/workspaceapi"
)

func (a *app) forwardContext(ctx context.Context, workspaceID, source, target string) (workspace.RouteAccess, error) {
	var out workspace.RouteAccess
	err := a.routedSSHCall(ctx, "mesh_forward_route_context", struct {
		Workspace string `json:"workspace"`
		Source    string `json:"source"`
		Target    string `json:"target"`
	}{workspaceID, source, target}, &out)
	return out, err
}

// selectForwardRoute uses fresh graph hints only for reachability. The final
// service grant is checked independently after pinned end-to-end TLS.
func (a *app) selectForwardRoute(ctx context.Context, store *mesh.Store, target, workspaceID, service string) (sshRouteSelection, error) {
	local := store.Config().Identity.Host
	if workspaceID == "" || target == "" || target == local || service == "" {
		return sshRouteSelection{}, model.Err("invalid_request", "exact workspace, distinct target ID and service required")
	}
	v, err := a.forwardContext(ctx, workspaceID, local, target)
	if err != nil {
		return sshRouteSelection{}, err
	}
	if v.Local.Host != local || v.Origin.Host != local || v.Destination.Host != target || v.Origin.Fingerprint != store.Config().Identity.Fingerprint {
		return sshRouteSelection{}, model.Err("forbidden", "TCP route original endpoint changed")
	}
	var graph api.GraphPage
	if err = a.routedSSHCall(ctx, "workspace_graph_read", api.GraphRead{Workspace: workspaceID, Section: "paths", Target: target, Offset: 0, Limit: 40}, &graph); err != nil {
		return sshRouteSelection{}, err
	}
	if !graph.Current || !graph.ValidUntil.After(time.Now()) {
		return sshRouteSelection{}, model.Err("route_unavailable", "fresh workspace graph required")
	}
	for _, path := range graph.Paths {
		if len(path) < 2 || len(path) > meshsshrelay.MaxNodes || path[0] != local || path[len(path)-1] != target {
			continue
		}
		peer, e := store.Resolve(path[1])
		if e != nil || peer.State != "active" || !peer.Proven {
			continue
		}
		next, e := a.forwardContext(ctx, workspaceID, local, path[1])
		if e != nil || next.Destination.Fingerprint != peer.Identity.Fingerprint {
			continue
		}
		return sshRouteSelection{workspace: workspaceID, service: service, access: v, path: append([]string(nil), path...), first: peer, store: store, app: a}, nil
	}
	return sshRouteSelection{}, model.Err("route_unavailable", "no fresh path begins with exact active direct peer")
}
