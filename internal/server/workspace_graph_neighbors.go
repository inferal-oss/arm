package server

import (
	"sort"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

// Capture current registry OUTSIDE SQL and trust callbacks. A reported edge is
// only a hint; directory mutual confirmation filters exact current membership,
// and every eventual relay still checks its own live enrollment/authority.
func graphNeighbors(config mesh.Config, records workspace.Records, root workspace.Instance) []workspacegraph.Neighbor {
	type known struct {
		pin        string
		generation int64
	}
	nodes := map[string]known{root.Directory.Host: {root.Directory.Fingerprint, 1}}
	if root.Role == "directory" {
		for _, m := range records.Members {
			if m.Workspace == root.ID && m.State == "active" {
				nodes[m.Identity.Host] = known{m.Identity.Fingerprint, m.MembershipGeneration}
			}
		}
	} else {
		for _, m := range records.Trusted {
			if m.Workspace == root.ID && m.State == "active" {
				nodes[m.Identity.Host] = known{m.Identity.Fingerprint, m.MembershipGeneration}
			}
		}
	}
	out := []workspacegraph.Neighbor{}
	for _, p := range config.Peers {
		n, ok := nodes[p.Identity.Host]
		if !ok || p.Identity.Host == root.Self.Host || p.State != "active" || !p.Proven || n.pin != p.Identity.Fingerprint {
			continue
		}
		if _, e := mesh.ValidateIdentity(p.Identity); e != nil {
			continue
		}
		out = append(out, workspacegraph.Neighbor{Host: p.Identity.Host, Generation: n.generation, Pin: n.pin, Pair: p.Pair})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}
