package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

// forwardSourceDial selects a current graph path without treating reachability
// as service authority. A failure before request bytes permits another path;
// the caller never retries a request after bytes may have crossed this stream.
type forwardSourceAuthority struct {
	Workspace, Generation                   string
	Local, Origin, Destination, Directory   workspace.Identity
	OriginGeneration, DestinationGeneration int64
}

func sourceAuthority(a workspace.RouteAccess) forwardSourceAuthority {
	return forwardSourceAuthority{a.Workspace, a.Generation, a.Local, a.Origin, a.Destination, a.Directory, a.OriginGeneration, a.DestinationGeneration}
}

func (m *meshService) forwardSourceDial(ctx context.Context, workspaceID, target, service string, original forwardSourceAuthority, holdCheck func(context.Context) error) (*tls.Conn, meshsshrelay.Open, error) {
	if holdCheck == nil {
		return nil, meshsshrelay.Open{}, model.Err("lease_inactive", "original listener check required")
	}
	if err := holdCheck(ctx); err != nil {
		return nil, meshsshrelay.Open{}, err
	}
	localHost := m.store.Config().Identity.Host
	if workspaceID == "" || target == localHost || len(target) != 32 || service == "" {
		return nil, meshsshrelay.Open{}, model.Err("invalid_request", "exact workspace, distinct destination and service required")
	}
	a, err := m.owner.Manager.WorkspaceRouteAccess(ctx, workspaceID, localHost, target, workspace.RouteForward)
	if err != nil {
		return nil, meshsshrelay.Open{}, err
	}
	if sourceAuthority(a) != original {
		return nil, meshsshrelay.Open{}, model.Err("forbidden", "original TCP listener endpoint membership changed; start a newly reviewed listener")
	}
	if a.Local.Host != localHost || a.Origin.Host != localHost || a.Destination.Host != target || a.Origin.Fingerprint != m.store.Config().Identity.Fingerprint {
		return nil, meshsshrelay.Open{}, model.Err("forbidden", "current original TCP endpoint changed")
	}
	return forwardSourceDialCandidates(m.owner.graphRouteCandidates(ctx, a, "forward"), func(candidate graphRouteCandidate) forwardCandidateOutcome {
		if candidate.saved || candidate.probe || len(candidate.path) < 2 || len(candidate.path) > meshsshrelay.MaxNodes {
			return forwardCandidateOutcome{}
		}
		if err := holdCheck(ctx); err != nil {
			return forwardCandidateOutcome{err: err, stop: true}
		}
		peer, e := m.owner.graphCandidatePeer(ctx, m, a, candidate, "forward")
		if e != nil {
			return forwardCandidateOutcome{err: e}
		}
		raw, e := m.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
		if e != nil {
			return forwardCandidateOutcome{err: e}
		}
		var scope model.NodeAccessScope
		if e = mesh.Decode(raw, &scope); e != nil || scope.Source != localHost || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 {
			return forwardCandidateOutcome{err: model.Err("forbidden", "first forward pair changed")}
		}
		until := time.Now().Add(meshsshrelay.MaxLifetime)
		for _, stamp := range []time.Time{a.Expires, a.Origin.Expires, a.Destination.Expires} {
			if stamp.Before(until) {
				until = stamp
			}
		}
		o := meshsshrelay.Open{Workspace: workspaceID, Generation: a.Generation, Route: local.ID(), Source: localHost, Target: target, SourceGeneration: a.OriginGeneration, TargetGeneration: a.DestinationGeneration, DirectoryRevision: a.DirectoryRevision,
			DirectoryPin: a.Directory.Fingerprint, DirectorySPKI: a.Directory.SPKI, SourcePin: a.Origin.Fingerprint, TargetPin: a.Destination.Fingerprint, SSHHostKeyPin: strings.Repeat("0", 64), Service: service,
			Path: append([]string(nil), candidate.path...), Hop: 1, Class: meshsshrelay.ClassForward, Expires: until, Pair: peer.Pair, EnrollmentRevision: scope.Revision}
		if e = o.Validate(time.Now()); e != nil {
			return forwardCandidateOutcome{err: e}
		}
		outer, e := m.store.ClientTLS(peer)
		if e != nil {
			return forwardCandidateOutcome{err: e}
		}
		inner, e := m.store.RoutedForwardTLS(a.Local, a.Destination, false)
		if e != nil {
			return forwardCandidateOutcome{err: e}
		}
		check := func(c context.Context) error {
			if err := holdCheck(c); err != nil {
				return err
			}
			current, e := m.store.Resolve(peer.Identity.Host)
			if e != nil {
				return e
			}
			if current.State != "active" || !current.Proven || current.Pair != peer.Pair || current.Revision != peer.Revision || current.Identity != peer.Identity {
				return model.Err("forbidden", "first forward enrollment changed")
			}
			fresh, e := m.owner.Manager.WorkspaceRouteAccess(c, workspaceID, localHost, target, workspace.RouteForward)
			if e != nil {
				return e
			}
			if fresh.Generation != a.Generation || fresh.Origin != a.Origin || fresh.Destination != a.Destination || fresh.Directory != a.Directory || fresh.OriginGeneration != a.OriginGeneration || fresh.DestinationGeneration != a.DestinationGeneration {
				return model.Err("forbidden", "original TCP route membership changed")
			}
			if err := m.store.WorkspaceIdentityCurrent(a.Local); err != nil {
				return err
			}
			return holdCheck(c)
		}
		conn, e := meshsshrelay.Dial(ctx, peer.Identity.Address, outer, inner, o, check)
		if e == nil {
			return forwardCandidateOutcome{conn: conn, route: o}
		}
		return forwardCandidateOutcome{err: e}
	})

}

type forwardCandidateOutcome struct {
	conn  *tls.Conn
	route meshsshrelay.Open
	err   error
	stop  bool
}

// This is the production candidate loop. A native close failure ends route
// selection before another original transport can be opened.
func forwardSourceDialCandidates(candidates []graphRouteCandidate, attempt func(graphRouteCandidate) forwardCandidateOutcome) (*tls.Conn, meshsshrelay.Open, error) {
	var last error
	for _, candidate := range candidates {
		result := attempt(candidate)
		if result.conn != nil && result.err == nil {
			return result.conn, result.route, nil
		}
		if result.stop || forwardSourceDialNativeDebt(result.err) {
			return nil, meshsshrelay.Open{}, result.err
		}
		if result.err != nil {
			last = result.err
		}
	}
	if last == nil {
		last = model.Err("route_unavailable", "no current exact TCP path begins with a pinned direct peer")
	}
	if errors.Is(last, context.Canceled) || errors.Is(last, context.DeadlineExceeded) {
		return nil, meshsshrelay.Open{}, last
	}
	return nil, meshsshrelay.Open{}, last
}

func forwardSourceDialNativeDebt(err error) bool {
	var nativeUncertain *meshsshrelay.NativeCloseUncertain
	return errors.As(err, &nativeUncertain)
}
