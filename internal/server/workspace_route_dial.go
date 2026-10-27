package server

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	route "github.com/inferal-oss/arm/internal/workspaceroute"
)

// workspaceRouteDial selects only stable IDs from current local authority and
// current direct peer registry. The initial star route is source→directory→
// destination when no exact direct destination exists. Graph alternatives are
// a separate bounded directory observation, never arbitrary caller path/URL.
func (s *Server) workspaceRouteDial(ctx context.Context, id, target, class string) (*workspaceRouteConn, route.Open, workspace.RouteAccess, error) {
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

	// All alternatives share one original start deadline. No application request
	// has been written here; once the caller begins WriteRequest it never redials.
	expires := minStamp(time.Now().Add(route.MaxLifetime), a.Expires)
	attemptCtx, cancelAttempts := context.WithDeadline(ctx, expires)
	succeeded := false
	defer func() {
		if !succeeded {
			cancelAttempts()
		}
	}()
	// On success the carrier owns this bounded context until its original deadline;
	// cancelling it here would close the returned authenticated application stream.
	var last error
	var lastOpen route.Open
	for _, candidate := range s.graphRouteCandidates(attemptCtx, a, class) {
		if e = attemptCtx.Err(); e != nil {
			break
		}
		peer, err := s.graphCandidatePeer(attemptCtx, m, a, candidate, class)
		if err != nil {
			last = err
			continue
		}
		hopGeneration, err := s.graphHopGeneration(attemptCtx, a, candidate, class, peer.Identity.Fingerprint)
		if err != nil {
			last = err
			continue
		}
		o := route.Open{Version: route.Version, Workspace: id, Generation: a.Generation, Route: local.ID(), Source: source, Target: target, SourceGeneration: a.OriginGeneration, TargetGeneration: a.DestinationGeneration, DirectoryRevision: a.DirectoryRevision, Path: candidate.path, Hop: 1, Class: class, Expires: expires, Pair: peer.Pair, Revision: 1}
		lastOpen = o
		check := func(checkCtx context.Context) error {
			current, err := s.Manager.WorkspaceRouteAccess(checkCtx, id, source, target, ledgerClass)
			if err != nil {
				return err
			}
			if current.Generation != a.Generation || current.Local != a.Local || current.Origin != a.Origin || current.Destination != a.Destination || current.OriginGeneration != a.OriginGeneration || current.DestinationGeneration != a.DestinationGeneration {
				return model.Err("forbidden", "origin/final route authority changed")
			}
			// The chosen graph is a reachability observation, not a stream
			// grant. A new same-scope refresh must not tear down this original
			// request/receipt. Revalidate stable hop membership and native pair
			// instead, retaining the original generation and pin.
			liveCandidate := candidate
			liveCandidate.graph, liveCandidate.probe = "", false
			livePeer, err := s.graphCandidatePeer(checkCtx, m, current, liveCandidate, class)
			if err != nil {
				return err
			}
			if livePeer.Identity != peer.Identity {
				return model.Err("identity_changed", "selected first-hop identity changed")
			}
			generation, err := s.graphHopGeneration(checkCtx, current, candidate, class, peer.Identity.Fingerprint)
			if err != nil {
				return err
			}
			if generation != hopGeneration {
				return model.Err("identity_changed", "selected first-hop membership generation changed")
			}
			return m.store.WorkspaceIdentityCurrent(a.Local)
		}
		raw, err := m.routeHop(attemptCtx, peer, o, check)
		if err != nil {
			last = err
			continue
		}
		config, err := m.store.RoutedWorkspaceTLS(a.Local, a.Destination, false)
		if err != nil {
			raw.Close()
			last = err
			continue
		}
		conn := tls.Client(raw, config)
		handshake, stop := context.WithTimeout(attemptCtx, 5*time.Second)
		err = conn.HandshakeContext(handshake)
		stop()
		if err == nil {
			err = check(attemptCtx)
		}
		if err != nil {
			raw.Close()
			last = err
			continue
		}
		succeeded = true
		return &workspaceRouteConn{Conn: conn, ctx: attemptCtx, beforeWrite: func(ctx context.Context) error {
			if err := check(ctx); err != nil {
				return err
			}
			_, err := s.graphCandidatePeer(ctx, m, a, candidate, class)
			return err
		}}, o, a, nil
	}
	if last == nil {
		last = model.Err("route_unavailable", "no current bounded workspace path; application request was not retried")
	}
	if attemptCtx.Err() != nil {
		last = attemptCtx.Err()
	}
	return nil, lastOpen, a, last
}

func routedWireRequest(o route.Open, operation string) route.Request {
	return route.Request{Version: route.Version, Route: o.Route, Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target, Operation: operation}
}
