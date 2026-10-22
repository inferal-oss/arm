package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	route "github.com/yrashk/arm/internal/workspaceroute"
	direct "github.com/yrashk/arm/internal/workspacetransport"
)

func directRouteOuter(o route.Outer) direct.Outer {
	return direct.Outer{Source: o.Source, Target: o.Target, Pair: o.Pair, Certificate: o.Certificate, Revision: o.Revision, Expires: o.Expires}
}
func (m *meshService) routedAdmission(ctx context.Context, outer route.Outer, o route.Open) (route.Permit, error) {
	if e := m.recheckWorkspaceOuter(directRouteOuter(outer)); e != nil {
		return route.Permit{}, e
	}
	class := workspace.RouteMessage
	if o.Class == route.ClassDirectory {
		class = workspace.RouteDirectory
	}
	a, e := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, o.Target, class)
	if e != nil {
		return route.Permit{}, e
	}
	if a.Generation != o.Generation || a.Local.Host != outer.Target || a.Local.Host != o.ImmediateTarget() || a.OriginGeneration != o.SourceGeneration || a.DestinationGeneration != o.TargetGeneration || o.DirectoryRevision > a.DirectoryRevision {
		return route.Permit{}, model.Err("forbidden", "route differs from current original/final membership and local authority")
	}
	// A direct pair is transport reachability, not membership. The immediate
	// previous relay must independently remain an admitted member of this exact
	// workspace; its ordinary resource/SSH grant is irrelevant here.
	if outer.Source != o.Source {
		hop, e := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, outer.Source, workspace.RouteMessage)
		if e != nil {
			return route.Permit{}, e
		}
		if hop.Destination.Fingerprint != outer.Certificate {
			return route.Permit{}, model.Err("forbidden", "previous relay is not the currently pinned workspace member")
		}
	}
	p := route.Permit{Local: a.Local, Origin: a.Origin, Destination: a.Destination, Expires: minStamp(a.Expires, outer.Expires)}
	if o.Final() {
		p.Inner, e = m.store.RoutedWorkspaceTLS(a.Local, a.Origin, true)
		if e != nil {
			return route.Permit{}, e
		}
	}
	if e = m.recheckWorkspaceOuter(directRouteOuter(outer)); e != nil {
		return route.Permit{}, e
	}
	return p, nil
}
func (m *meshService) newRoutedHandler() *route.Handler {
	h := &route.Handler{}
	h.AuthorizeOuter = func(ctx context.Context, raw []byte) (route.Outer, error) {
		o, e := m.workspaceOuter(ctx, raw)
		return route.Outer{Source: o.Source, Target: o.Target, Pair: o.Pair, Certificate: o.Certificate, Revision: o.Revision, Expires: o.Expires}, e
	}
	h.Authorize = m.routedAdmission
	h.Revalidate = func(ctx context.Context, outer route.Outer, o route.Open, p route.Permit) error {
		actual, e := m.routedAdmission(ctx, outer, o)
		if e != nil {
			return e
		}
		if actual.Local != p.Local || actual.Origin != p.Origin || actual.Destination != p.Destination || !time.Now().Before(p.Expires) {
			return model.Err("forbidden", "current route membership changed")
		}
		return nil
	}
	h.Forward = func(ctx context.Context, outer route.Outer, o route.Open, p route.Permit, previous net.Conn) {
		nextHost := o.Path[o.Hop+1]
		next, e := m.store.Resolve(nextHost)
		if e != nil || next.State != "active" || !next.Proven {
			return
		}
		// This lookup proves a CURRENT admitted relay/final node, with exactly the
		// direct registry certificate. An arbitrary stable ID is not a dial grant.
		member, e := m.owner.Manager.WorkspaceRouteAccess(ctx, o.Workspace, o.Source, nextHost, workspace.RouteMessage)
		if e != nil || member.Destination.Fingerprint != next.Identity.Fingerprint {
			return
		}
		forwarded, e := o.Forward(next.Pair, 1)
		if e != nil {
			return
		}
		conn, e := m.routeHop(ctx, next, forwarded, func(ctx context.Context) error { return h.Revalidate(ctx, outer, o, p) })
		if e != nil {
			return
		}
		defer conn.Close()
		copied := make(chan struct{})
		go func() { defer close(copied); _, _ = io.Copy(conn, previous); _ = conn.Close(); _ = previous.Close() }()
		_, _ = io.Copy(previous, conn)
		_ = conn.Close()
		_ = previous.Close()
		<-copied
	}
	h.Final = func(ctx context.Context, outer route.Outer, o route.Open, p route.Permit, inner *tls.Conn) {
		q, e := route.ReadRequest(inner, o)
		if e != nil || h.Revalidate(ctx, outer, o, p) != nil {
			return
		}
		result := q.Response()
		switch q.Operation {
		case route.OperationMessage:
			in, err := m.owner.Manager.WorkspaceRoutingAccept(ctx, p.Origin, *q.Envelope)
			e = err
			if e == nil {
				result.Receipt = &in.Receipt
			}
		case route.OperationLookup:
			receipt, err := m.owner.Manager.WorkspaceRoutingLookup(ctx, p.Origin, *q.Envelope)
			e = err
			if e == nil {
				result.Lookup = &route.LookupResult{Found: receipt != nil, Receipt: receipt}
			}
		case route.OperationRefresh:
			if q.Refresh.Source.Host != p.Origin.Host || q.Refresh.Source.Fingerprint != p.Origin.Fingerprint || q.Refresh.Source.MembershipGeneration != o.SourceGeneration {
				e = model.Err("forbidden", "directory requester differs from authenticated original member")
				break
			}

			if q.Report != nil {
				replies, err := m.owner.graphDirectoryReplies(ctx, q)
				e = err
				if e == nil {
					for _, r := range replies {
						if h.Revalidate(ctx, outer, o, p) != nil {
							return
						}
						if route.WriteReply(inner, q, r) != nil {
							return
						}
					}
					return
				}
				break
			}
			snapshot, err := m.owner.Manager.WorkspaceIssue(ctx, *q.Refresh)
			e = err
			if e == nil {
				pages, err := workspace.Pages(snapshot, workspace.MaxPageBytes-1024)
				e = err
				if e == nil {
					total := 0
					for _, page := range pages {
						r := q.Response()
						r.Page = &page
						raw, _ := json.Marshal(r)
						total += len(raw) + 4
					}
					if total > route.MaxStreamBytes-32768 {
						e = model.Err("response_too_large", "complete directory exceeds routed stream limit; nothing truncated")
					} else {
						for _, page := range pages {
							if h.Revalidate(ctx, outer, o, p) != nil {
								return
							}
							r := q.Response()
							r.Page = &page
							if route.WriteReply(inner, q, r) != nil {
								return
							}
						}
						return
					}
				}
			}
		}
		if e != nil {
			code := "workspace_unavailable"
			var api *model.Error
			if errors.As(e, &api) {
				code = api.Code
			}
			result.Failure = &route.Failure{Code: code, Message: "Routed operation unavailable; inspect the retained original key. No delivery was retried."}
		}
		_ = route.WriteReply(inner, q, result)
	}
	m.owner.workspaceStreamsMu.Lock()
	if m.owner.workspaceCallsClosed {
		h.Stop()
	}
	if m.owner.routedStreams == nil {
		m.owner.routedStreams = map[*route.Handler]struct{}{}
	}
	for old := range m.owner.routedStreams {
		if old.Finished() {
			delete(m.owner.routedStreams, old)
		}
	}
	m.owner.routedStreams[h] = struct{}{}
	m.owner.workspaceStreamsMu.Unlock()
	return h
}

// routeHop authenticates only this current direct hop. Its destination current
// revision is read THERE, not confused with the local inbound grant revision.
// No caller supplies a URL/address, retry, fallback or an ordinary RPC envelope.
func (m *meshService) routeHop(ctx context.Context, peer mesh.Peer, o route.Open, check func(context.Context) error) (net.Conn, error) {
	raw, e := m.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if e != nil {
		return nil, e
	}
	var scope model.NodeAccessScope
	if e = mesh.Decode(raw, &scope); e != nil || scope.Source != m.store.Config().Identity.Host || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 {
		return nil, model.Err("forbidden", "current hop destination scope differs")
	}
	o.Pair, o.Revision = peer.Pair, scope.Revision
	config, e := m.store.ClientTLS(peer)
	if e != nil {
		return nil, e
	}
	recheck := func(ctx context.Context) error {
		current, e := m.store.Resolve(peer.Identity.Host)
		if e != nil {
			return e
		}
		if current.State != "active" || !current.Proven || current.Pair != peer.Pair || current.Revision != peer.Revision || current.Identity != peer.Identity {
			return model.Err("forbidden", "outgoing direct route enrollment changed")
		}
		return check(ctx)
	}
	return route.DialCarrier(ctx, peer.Identity.Address, config, o, recheck)
}
func (s *Server) closeRoutedStreams() {
	s.workspaceStreamsMu.Lock()
	handlers := make([]*route.Handler, 0, len(s.routedStreams))
	for h := range s.routedStreams {
		handlers = append(handlers, h)
	}
	s.workspaceStreamsMu.Unlock()
	for _, h := range handlers {
		h.Stop()
	}
	for _, h := range handlers {
		h.Wait()
	}
}
