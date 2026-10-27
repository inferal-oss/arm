package server

import (
	"context"
	"slices"
	"sort"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/workspace"
	"github.com/inferal-oss/arm/internal/workspacegraph"
	route "github.com/inferal-oss/arm/internal/workspaceroute"
)

type graphRouteCandidate struct {
	path  []string
	graph string
	saved bool
	probe bool
}

// Candidate construction never grants transport: the exact current first-hop
// pair/pin and every intermediate's current workspace authority are rechecked.
func (s *Server) graphRouteCandidates(ctx context.Context, a workspace.RouteAccess, class string) []graphRouteCandidate {
	out := []graphRouteCandidate{}
	add := func(p []string, graph string, saved bool, probe bool) {
		if len(out) >= workspacegraph.MaxPaths || len(p) < 2 || len(p) > workspacegraph.MaxPathNodes || p[0] != a.Local.Host || p[len(p)-1] != a.Destination.Host {
			return
		}
		for _, old := range out {
			if slices.Equal(old.path, p) {
				return
			}
		}
		out = append(out, graphRouteCandidate{append([]string{}, p...), graph, saved, probe})
	}
	if service, e := s.workspaceService(); e == nil {
		if peer, e := service.store.Resolve(a.Destination.Host); e == nil && peer.State == "active" && peer.Proven && peer.Identity.Fingerprint == a.Destination.Fingerprint {
			add([]string{a.Local.Host, a.Destination.Host}, "", false, false)
		}
	}
	if hints, e := s.Manager.WorkspaceGraphHints(ctx, a.Workspace, a.Destination.Host, class == route.ClassDirectory); e == nil {
		for _, p := range hints.Paths {
			add(p, hints.Snapshot.Hash, hints.ReachabilityOnly, false)
		}
		if class == route.ClassDirectory {
			for _, p := range hints.DirectoryProbes {
				path := make([]string, len(p.Nodes))
				for i, n := range p.Nodes {
					path[i] = n.Host
				}
				add(path, "", true, true)
			}
		}
	}
	// Preserve the explicit D1 directory relay while graph observations converge.
	// It is not inferred from an expired graph and still requires current authority.
	if a.Directory.Host != a.Local.Host && a.Directory.Host != a.Destination.Host {
		add([]string{a.Local.Host, a.Directory.Host, a.Destination.Host}, "", false, false)
	}
	if class == route.ClassDirectory && a.Destination == a.Directory {
		if service, e := s.workspaceService(); e == nil {
			records, err := s.Manager.WorkspaceRead(ctx)
			if err != nil {
				return out
			}
			known := map[string]string{}
			denied := map[string]bool{}
			for _, d := range records.Denies {
				if d.Workspace == a.Workspace {
					denied[d.Host] = true
				}
			}
			for _, member := range records.Trusted {
				if member.Workspace == a.Workspace && member.State == "active" && member.MembershipGeneration > 0 && !denied[member.Identity.Host] {
					known[member.Identity.Host] = member.Identity.Fingerprint
				}
			}
			peers := service.store.Config().Peers
			sort.Slice(peers, func(i, j int) bool { return peers[i].Identity.Host < peers[j].Identity.Host })
			for _, peer := range peers {
				if len(out) >= workspacegraph.MaxPaths {
					break
				}
				if peer.State == "active" && peer.Proven && peer.Identity.Host != a.Directory.Host && known[peer.Identity.Host] == peer.Identity.Fingerprint {
					add([]string{a.Local.Host, peer.Identity.Host, a.Directory.Host}, "", true, false)
				}
			}
		}
	}
	return out
}
func (s *Server) graphCandidatePeer(ctx context.Context, m *meshService, a workspace.RouteAccess, c graphRouteCandidate, class string) (mesh.Peer, error) {
	var expectedGeneration int64
	var expectedPin string
	if c.probe {
		if class != route.ClassDirectory || a.Destination != a.Directory {
			return mesh.Peer{}, model.Err("forbidden", "historical probe cannot authorize messages or lookup")
		}
		hints, e := s.Manager.WorkspaceGraphHints(ctx, a.Workspace, a.Directory.Host, true)
		if e != nil {
			return mesh.Peer{}, e
		}
		found := false
		for _, p := range hints.DirectoryProbes {
			path := make([]string, len(p.Nodes))
			for i, n := range p.Nodes {
				path[i] = n.Host
			}
			if slices.Equal(path, c.path) && p.SourceGeneration == a.OriginGeneration && p.SourcePin == a.Local.Fingerprint && p.DirectoryPin == a.Directory.Fingerprint {
				found = true
				expectedGeneration = p.Nodes[1].Generation
				expectedPin = p.Nodes[1].Pin
			}
		}
		if !found {
			return mesh.Peer{}, model.Err("graph_stale", "exact historical directory probe changed")
		}
	}
	var identity workspace.Identity
	next := c.path[1]
	if next == a.Destination.Host {
		identity = a.Destination
	} else if class == route.ClassDirectory {
		var e error
		if expectedGeneration > 0 {
			identity, e = s.Manager.WorkspaceDirectoryNextHopPinned(ctx, a.Workspace, next, expectedGeneration, expectedPin)
		} else {
			identity, e = s.Manager.WorkspaceDirectoryNextHop(ctx, a.Workspace, next)
		}
		if e != nil {
			return mesh.Peer{}, e
		}
	} else {
		membershipClass := workspace.RouteMessage
		if class == "forward" {
			membershipClass = workspace.RouteForward
		}
		hop, e := s.Manager.WorkspaceRouteAccess(ctx, a.Workspace, a.Local.Host, next, membershipClass)
		if e != nil {
			return mesh.Peer{}, e
		}
		identity = hop.Destination
	}
	if c.graph != "" {
		hints, e := s.Manager.WorkspaceGraphHints(ctx, a.Workspace, a.Destination.Host, class == route.ClassDirectory)
		if e != nil {
			return mesh.Peer{}, e
		}
		if (class != route.ClassDirectory && (c.saved || hints.ReachabilityOnly)) || !graphPathPresent(hints.Snapshot, c.path) {
			return mesh.Peer{}, model.Err("graph_stale", "selected graph observation changed before application write")
		}
		found := false
		for _, n := range hints.Snapshot.Nodes {
			if n.Host == next && n.Pin == identity.Fingerprint {
				found = true
			}
		}
		if !found {
			return mesh.Peer{}, model.Err("identity_changed", "selected graph first-hop pin differs from current workspace binding")
		}
	}
	peer, e := m.store.Resolve(next)
	if e != nil {
		return mesh.Peer{}, e
	}
	if peer.State != "active" || !peer.Proven || peer.Identity.Fingerprint != identity.Fingerprint || peer.Identity.Host != identity.Host {
		return mesh.Peer{}, model.Err("route_unavailable", "no current exact proven first-hop pair")
	}
	return peer, nil
}

// graphPathPresent accepts a superseding COMPLETE observation only if every
// selected edge still exists. A graph hash is not ongoing membership authority.
func graphPathPresent(snapshot workspacegraph.Snapshot, path []string) bool {
	if len(path) < 2 || len(path) > workspacegraph.MaxPathNodes {
		return false
	}
	for _, host := range path {
		found := false
		for _, node := range snapshot.Nodes {
			if node.Host == host {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for i := 1; i < len(path); i++ {
		found := false
		for _, edge := range snapshot.Edges {
			if edge.A == path[i-1] && edge.B == path[i] || edge.B == path[i-1] && edge.A == path[i] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// graphHopGeneration captures/rechecks immutable selected-hop identity scope.
// Retained pins are usable only for this node's own directory reachability;
// ordinary message/lookup streams require fresh membership at every check.
func (s *Server) graphHopGeneration(ctx context.Context, a workspace.RouteAccess, c graphRouteCandidate, class, pin string) (int64, error) {
	next := c.path[1]
	if next == a.Destination.Host {
		if a.Destination.Fingerprint != pin {
			return 0, model.Err("identity_changed", "selected final pin changed")
		}
		return a.DestinationGeneration, nil
	}
	if class != route.ClassDirectory {
		hop, err := s.Manager.WorkspaceRouteAccess(ctx, a.Workspace, a.Local.Host, next, workspace.RouteMessage)
		if err != nil {
			return 0, err
		}
		if hop.Destination.Fingerprint != pin {
			return 0, model.Err("identity_changed", "selected first-hop pin changed")
		}
		return hop.DestinationGeneration, nil
	}
	records, err := s.Manager.WorkspaceRead(ctx)
	if err != nil {
		return 0, err
	}
	for _, member := range records.Trusted {
		if member.Workspace == a.Workspace && member.Identity.Host == next && member.State == "active" && member.Identity.Fingerprint == pin {
			_, err := s.Manager.WorkspaceDirectoryNextHopPinned(ctx, a.Workspace, next, member.MembershipGeneration, pin)
			return member.MembershipGeneration, err
		}
	}
	return 0, model.Err("identity_changed", "selected directory first-hop pin/generation is unavailable")
}
