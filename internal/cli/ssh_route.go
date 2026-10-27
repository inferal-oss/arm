package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
	api "getarm.org/internal/workspaceapi"
)

type sshRouteSelection struct {
	workspace string
	service   string
	access    workspace.RouteAccess
	path      []string
	first     mesh.Peer
	store     *mesh.Store
	app       *app
	identity  model.SSHDestinationIdentity
}

func (a *app) routedSSHCall(ctx context.Context, operation string, request, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return client.Call(callCtx, a.home, operation, request, result)
}

func (a *app) routedSSHContext(ctx context.Context, workspaceID, source, target string) (meshsshrelay.Context, error) {
	var out meshsshrelay.Context
	e := a.routedSSHCall(ctx, "mesh_ssh_route_context", struct {
		Workspace string `json:"workspace"`
		Source    string `json:"source"`
		Target    string `json:"target"`
	}{workspaceID, source, target}, &out)
	return out, e
}

func (a *app) selectSSHRoute(ctx context.Context, store *mesh.Store, target, workspaceID string) (sshRouteSelection, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	localHost := store.Config().Identity.Host
	if len(target) != 32 || target == localHost {
		return sshRouteSelection{}, model.Err("invalid_request", "routed SSH requires exact distinct destination stable ID")
	}
	if workspaceID == "" {
		var list api.List
		if e := a.routedSSHCall(ctx, "workspace_list", struct{}{}, &list); e != nil {
			return sshRouteSelection{}, e
		}
		for _, w := range list.Workspaces {
			if err := ctx.Err(); err != nil {
				return sshRouteSelection{}, err
			}
			if w.State != "active" {
				continue
			}
			if _, e := a.routedSSHContext(ctx, w.Workspace, localHost, target); e == nil {
				if workspaceID != "" {
					return sshRouteSelection{}, model.Err("ambiguous_workspace", "multiple current workspaces reach destination; specify --workspace")
				}
				workspaceID = w.Workspace
			}
		}
		if workspaceID == "" {
			if err := ctx.Err(); err != nil {
				return sshRouteSelection{}, err
			}
			return sshRouteSelection{}, model.Err("route_unavailable", "no current routed SSH workspace for exact destination; specify --workspace or refresh membership")
		}
	}
	current, e := a.routedSSHContext(ctx, workspaceID, localHost, target)
	if e != nil {
		return sshRouteSelection{}, e
	}
	v := current.Access
	if v.Local.Host != localHost || v.Origin.Host != localHost || v.Destination.Host != target || v.Generation == "" ||
		v.Origin.Fingerprint != store.Config().Identity.Fingerprint {
		return sshRouteSelection{}, model.Err("forbidden", "routed SSH source or destination identity changed")
	}
	var graph api.GraphPage
	if e = a.routedSSHCall(ctx, "workspace_graph_read", api.GraphRead{Workspace: workspaceID, Section: "paths", Target: target, Offset: 0, Limit: 40}, &graph); e != nil {
		return sshRouteSelection{}, e
	}
	if !graph.Current || !graph.ValidUntil.After(time.Now()) || len(graph.Paths) == 0 {
		return sshRouteSelection{}, model.Err("route_unavailable", "fresh exact workspace graph path required")
	}
	var lastError error
	for _, path := range graph.Paths {
		if err := ctx.Err(); err != nil {
			return sshRouteSelection{}, err
		}
		if len(path) < 2 || len(path) > meshsshrelay.MaxNodes || path[0] != localHost || path[len(path)-1] != target {
			continue
		}
		peer, e := store.Resolve(path[1])
		if e != nil || peer.State != "active" || !peer.Proven {
			continue
		}
		next, e := a.routedSSHContext(ctx, workspaceID, localHost, path[1])
		if e != nil || next.Access.Destination.Fingerprint != peer.Identity.Fingerprint {
			continue
		}
		candidate := sshRouteSelection{workspace: workspaceID, access: v, path: append([]string(nil), path...), first: peer, store: store, app: a}
		response, conn, err := candidate.request(ctx, meshsshrelay.ClassSSHIdentity, strings.Repeat("0", 64),
			meshsshrelay.Request{Version: meshsshrelay.Version, Operation: meshsshrelay.OperationIdentity})
		if conn != nil {
			_ = conn.Close()
		}
		if err != nil {
			lastError = err
			continue
		}
		if response.Identity == nil || response.Identity.Schema != 1 || response.Identity.Source != localHost ||
			response.Identity.Target != target || response.Identity.Certificate != v.Destination.Fingerprint ||
			response.Identity.SPKI != v.Destination.SPKI {
			lastError = model.Err("ssh_identity_changed", "routed destination differs from pinned workspace endpoint")
			continue
		}
		candidate.identity = *response.Identity
		return candidate, nil
	}
	if err := ctx.Err(); err != nil {
		return sshRouteSelection{}, err
	}
	if lastError != nil {
		return sshRouteSelection{}, fmt.Errorf("no current SSH route admitted by its exact destination: %w", lastError)
	}
	return sshRouteSelection{}, model.Err("route_unavailable", "no current graph path begins with an exact active direct peer")
}

func (s sshRouteSelection) dial(ctx context.Context, class, hostKeyPin string) (*tls.Conn, meshsshrelay.Open, error) {
	a := s.access
	now := time.Now()
	until := now.Add(meshsshrelay.MaxLifetime)
	for _, stamp := range []time.Time{a.Origin.Expires, a.Destination.Expires} {
		if stamp.Before(until) {
			until = stamp
		}
	}
	peer := s.first
	raw, e := s.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: []byte(`{}`)})
	if e != nil {
		return nil, meshsshrelay.Open{}, e
	}
	var scope model.NodeAccessScope
	if e = mesh.Decode(raw, &scope); e != nil || scope.Source != a.Local.Host || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 {
		return nil, meshsshrelay.Open{}, model.Err("forbidden", "routed SSH first-hop enrollment changed")
	}
	o := meshsshrelay.Open{Workspace: s.workspace, Generation: a.Generation, Route: local.ID(), Source: a.Origin.Host, Target: a.Destination.Host,
		SourceGeneration: a.OriginGeneration, TargetGeneration: a.DestinationGeneration, DirectoryRevision: a.DirectoryRevision,
		DirectoryPin: a.Directory.Fingerprint, DirectorySPKI: a.Directory.SPKI,
		SourcePin: a.Origin.Fingerprint, TargetPin: a.Destination.Fingerprint, SSHHostKeyPin: hostKeyPin, Path: append([]string(nil), s.path...),
		Service: s.service, Hop: 1, Class: class, Expires: until, Pair: peer.Pair, EnrollmentRevision: scope.Revision}
	if e = o.Validate(now); e != nil {
		return nil, o, e
	}
	outer, e := s.store.ClientTLS(peer)
	if e != nil {
		return nil, o, e
	}
	var inner *tls.Config
	if class == meshsshrelay.ClassForward {
		inner, e = s.store.RoutedForwardTLS(a.Local, a.Destination, false)
	} else {
		inner, e = s.store.RoutedSSHTLS(a.Local, a.Destination, false)
	}
	if e != nil {
		return nil, o, e
	}
	recheck := func(checkCtx context.Context) error {
		current, e := s.store.Resolve(peer.Identity.Host)
		if e != nil {
			return e
		}
		if current.State != "active" || !current.Proven || current.Pair != peer.Pair || current.Revision != peer.Revision || current.Identity != peer.Identity {
			return model.Err("forbidden", "routed SSH first-hop identity or grant changed")
		}
		var b workspace.RouteAccess
		var checkErr error
		if class == meshsshrelay.ClassForward {
			b, checkErr = s.app.forwardContext(checkCtx, s.workspace, a.Local.Host, a.Destination.Host)
		} else {
			var fresh meshsshrelay.Context
			fresh, checkErr = s.app.routedSSHContext(checkCtx, s.workspace, a.Local.Host, a.Destination.Host)
			b = fresh.Access
		}
		if checkErr != nil {
			return checkErr
		}
		if b.Generation != a.Generation || b.Origin != a.Origin || b.Destination != a.Destination ||
			b.Directory != a.Directory ||
			b.OriginGeneration != a.OriginGeneration || b.DestinationGeneration != a.DestinationGeneration {
			return model.Err("forbidden", "routed SSH current original/final membership changed")
		}
		return s.store.WorkspaceIdentityCurrent(a.Local)
	}
	conn, e := meshsshrelay.Dial(ctx, peer.Identity.Address, outer, inner, o, recheck)
	return conn, o, e
}

func (s sshRouteSelection) request(ctx context.Context, class, hostKeyPin string, q meshsshrelay.Request) (meshsshrelay.Response, *tls.Conn, error) {
	conn, _, e := s.dial(ctx, class, hostKeyPin)
	if e != nil {
		return meshsshrelay.Response{}, nil, e
	}
	fail := func(err error) (meshsshrelay.Response, *tls.Conn, error) {
		_ = conn.Close()
		return meshsshrelay.Response{}, nil, err
	}
	if e = conn.SetDeadline(time.Now().Add(meshsshrelay.RequestDeadline)); e != nil {
		return fail(e)
	}
	if e = meshsshrelay.WriteRequest(conn, q); e != nil {
		return fail(e)
	}
	response, e := meshsshrelay.ReadResponse(conn, q)
	if e != nil {
		return fail(e)
	}
	if response.Failure != nil {
		return fail(fmt.Errorf("routed SSH %s: %s", response.Failure.Code, response.Failure.Message))
	}
	if e = conn.SetDeadline(time.Time{}); e != nil {
		return fail(e)
	}
	return response, conn, nil
}
