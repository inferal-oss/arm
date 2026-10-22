package server

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	route "github.com/yrashk/arm/internal/workspaceroute"
)

// workspaceRouteDial selects only stable IDs from current local authority and
// current direct peer registry. The initial star route is source→directory→
// destination when no exact direct destination exists. Graph alternatives are
// a separate bounded directory observation, never arbitrary caller path/URL.
func (s *Server) workspaceRouteDial(ctx context.Context, id, target, class string) (*tls.Conn, route.Open, workspace.RouteAccess, error) {
	m, e := s.workspaceService()
	if e != nil {
		return nil, route.Open{}, workspace.RouteAccess{}, e
	}
	source := m.store.Config().Identity.Host
	ledgerClass := workspace.RouteMessage
	if class == route.ClassDirectory {
		ledgerClass = workspace.RouteDirectory
	}
	a, e := s.Manager.WorkspaceRouteAccess(ctx, id, source, target, ledgerClass)
	if e != nil {
		return nil, route.Open{}, a, e
	}
	var peer mesh.Peer
	path := []string{source, target}
	peer, e = m.store.Resolve(target)
	if e != nil || peer.State != "active" || !peer.Proven || peer.Identity.Fingerprint != a.Destination.Fingerprint {
		if a.Directory.Host == source || a.Directory.Host == target {
			return nil, route.Open{}, a, model.Err("route_unavailable", "no current exact direct workspace transport; no action was retried")
		}
		peer, e = m.store.Resolve(a.Directory.Host)
		if e != nil || peer.State != "active" || !peer.Proven || peer.Identity.Fingerprint != a.Directory.Fingerprint {
			return nil, route.Open{}, a, model.Err("route_unavailable", "no current directory relay; no discovered member was promoted to a direct peer")
		}
		path = []string{source, a.Directory.Host, target}
	}
	o := route.Open{Version: route.Version, Workspace: id, Generation: a.Generation, Route: local.ID(), Source: source, Target: target, SourceGeneration: a.OriginGeneration, TargetGeneration: a.DestinationGeneration, DirectoryRevision: a.DirectoryRevision, Path: path, Hop: 1, Class: class, Expires: minStamp(time.Now().Add(route.MaxLifetime), a.Expires), Pair: peer.Pair, Revision: 1}
	check := func(ctx context.Context) error {
		current, e := s.Manager.WorkspaceRouteAccess(ctx, id, source, target, ledgerClass)
		if e != nil {
			return e
		}
		if current.Generation != a.Generation || current.Local != a.Local || current.Origin != a.Origin || current.Destination != a.Destination || current.OriginGeneration != a.OriginGeneration || current.DestinationGeneration != a.DestinationGeneration {
			return model.Err("forbidden", "origin/final route authority changed")
		}
		return m.store.WorkspaceIdentityCurrent(a.Local)
	}
	raw, e := m.routeHop(ctx, peer, o, check)
	if e != nil {
		return nil, o, a, e
	}
	config, e := m.store.RoutedWorkspaceTLS(a.Local, a.Destination, false)
	if e != nil {
		raw.Close()
		return nil, o, a, e
	}
	conn := tls.Client(raw, config)
	handshake, cancel := context.WithTimeout(ctx, 5*time.Second)
	e = conn.HandshakeContext(handshake)
	cancel()
	if e != nil {
		raw.Close()
		return nil, o, a, e
	}
	if e = check(ctx); e != nil {
		raw.Close()
		return nil, o, a, e
	}
	return conn, o, a, nil
}
func routedWireRequest(o route.Open, operation string) route.Request {
	return route.Request{Version: route.Version, Route: o.Route, Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target, Operation: operation}
}
