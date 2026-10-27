package routing

import (
	"fmt"

	"getarm.org/internal/workspace"
	"getarm.org/internal/workspacegraph"
)

// GraphScope derives reachability constraints from complete membership. It is
// only a consistency helper: callers must supply freshly authenticated current
// membership themselves. No self-hash creates membership authority.
func GraphScope(m workspace.Snapshot, revision int64) workspacegraph.Scope {
	nodes := []workspacegraph.Node{}
	for _, c := range m.Members {
		if c.State == "active" {
			nodes = append(nodes, workspacegraph.Node{Host: c.Identity.Host, Generation: c.MembershipGeneration, Pin: c.Identity.Fingerprint, Relay: m.Policy.Relay})
		}
	}
	return workspacegraph.Scope{Workspace: m.Workspace, Generation: m.Generation, Directory: m.Directory.Host, DirectoryPin: m.Directory.Fingerprint, MembershipHash: m.Hash, MembershipRevision: m.Revision, Requester: m.Requester, RequesterGeneration: m.RequesterGeneration, Nonce: m.Nonce, Revision: revision, At: m.Issued, MembershipUntil: m.ValidUntil, Nodes: nodes}
}
func validateGraphBundle(b GraphBundle) error {
	if e := workspace.ValidateSnapshotStructure(b.Membership); e != nil {
		return e
	}
	s, m := b.Snapshot, b.Membership
	if e := s.ValidateStructure(); e != nil {
		return e
	}
	if s.Workspace != m.Workspace || s.Generation != m.Generation || s.Directory != m.Directory.Host || s.DirectoryPin != m.Directory.Fingerprint || s.MembershipHash != m.Hash || s.MembershipRevision != m.Revision || s.Requester != m.Requester || s.RequesterGeneration != m.RequesterGeneration || s.Nonce != m.Nonce || s.Issued.Before(m.Issued) || s.ValidUntil.After(m.ValidUntil) {
		return invalid("graph bundle differs from exact complete membership")
	}
	nodes := map[string]workspacegraph.Node{}
	for _, c := range m.Members {
		if c.State == "active" {
			nodes[c.Identity.Host] = workspacegraph.Node{Host: c.Identity.Host, Generation: c.MembershipGeneration, Pin: c.Identity.Fingerprint, Relay: m.Policy.Relay}
		}
	}
	if len(nodes) != len(s.Nodes) {
		return invalid("graph omits or adds an active member")
	}
	for _, n := range s.Nodes {
		old, ok := nodes[n.Host]
		if !ok || old.Generation != n.Generation || old.Pin != n.Pin || n.Relay && !old.Relay {
			return invalid("graph changed exact membership pin/generation/relay policy")
		}
	}
	return nil
}
func validateGraph(v Records) error {
	enable := map[string]Change{}
	for _, c := range v.Events {
		if c.Kind == "graph_enabled" {
			if _, ok := enable[c.Workspace]; ok {
				return invalid("graph enabled more than once")
			}
			enable[c.Workspace] = c
		}
	}
	if v.Graph == nil {
		if len(enable) > 0 {
			return invalid("graph-enabled history lost its mandatory optional group")
		}
		return nil
	}
	if len(v.Graph.Issuance) > workspace.MaxWorkspaces || len(v.Graph.Reports) > workspace.MaxWorkspaces*workspace.MaxMembers || len(v.Graph.Observations) > workspace.MaxWorkspaces {
		return invalid("graph bounded row counts exceeded")
	}
	issuers := map[string]GraphIssuance{}
	for _, g := range v.Graph.Issuance {
		r, ok := rootOf(v, g.Workspace)
		event, enabled := enable[g.Workspace]
		if _, seen := issuers[g.Workspace]; seen || !ok || !enabled || g.Schema != 1 || g.Generation != r.Generation || g.Authority != r.Authority || g.LocalPin != r.LocalPin || !known(g.Role, "directory", "member") || g.EnableRevision != event.After.Revision || g.EnabledAt != event.After.Updated || g.Updated.Before(g.EnabledAt) || g.WallFloor.Before(g.Updated) || g.ReportSequence < 0 || g.IssuedCount < 0 || g.Closed != (r.State == "retired") {
			return invalid("graph issuance lacks immutable enablement/authority/clock/closure")
		}
		if (g.LastReport == nil) != (g.ReportSequence == 0) || (g.LastReport == nil) != (g.ReportHash == "") {
			return invalid("graph source highwater is incomplete")
		}
		if q := g.LastReport; q != nil {
			if q.ValidateStructure() != nil || q.Workspace != g.Workspace || q.Generation != g.Generation || q.Source != g.Authority.Host || q.SourcePin != g.LocalPin || q.Sequence != g.ReportSequence || hash(q) != g.ReportHash || q.Issued.After(g.WallFloor) {
				return invalid("graph source report/highwater changed")
			}
		}
		if g.Role == "member" && (g.LastIssue != nil || g.IssuedCount != 0) {
			return invalid("member claims directory graph issuance")
		}
		if (g.LastIssue == nil) != (g.IssuedCount == 0) {
			return invalid("graph directory issuance highwater is incomplete")
		}
		if b := g.LastIssue; b != nil {
			if e := validateGraphBundle(*b); e != nil {
				return e
			}
			if b.Snapshot.Workspace != g.Workspace || b.Snapshot.Generation != g.Generation || b.Snapshot.Directory != g.Authority.Host || b.Snapshot.DirectoryPin != g.LocalPin || b.Snapshot.Revision != g.IssuedCount || b.Snapshot.Issued.After(g.WallFloor) {
				return invalid("graph issued bundle highwater/native authority differs")
			}
		}

		if g.ReportRefs == nil || len(g.ReportRefs) > workspace.MaxMembers || g.Role == "member" && len(g.ReportRefs) > 0 {
			return invalid("graph report inventory missing or outside bounded role")
		}
		previous := ""
		for _, ref := range g.ReportRefs {
			if !hexID(ref.Source, 16) || ref.Source <= previous || ref.Sequence < 1 || !hexID(ref.Hash, 32) {
				return invalid("graph source inventory is noncanonical or malformed")
			}
			previous = ref.Source
		}
		if g.Role == "directory" && g.IssuedCount > 0 && g.LastReport == nil {
			return invalid("directory issue lost its own source highwater")
		}
		issuers[g.Workspace] = g
	}
	if len(issuers) != len(enable) {
		return invalid("graph-enabled dependencies missing")
	}
	seen := map[string]bool{}
	for _, r := range v.Graph.Reports {
		g, ok := issuers[r.Workspace]
		q := r.Report
		key := r.Workspace + ":" + q.Source
		if !ok || seen[key] || g.Role != "directory" || r.Schema != 1 || r.Authority != g.Authority || r.DirectoryPin != g.LocalPin || q.ValidateStructure() != nil || q.Workspace != g.Workspace || q.Generation != g.Generation || hash(q) != r.Hash || r.Received.Before(g.EnabledAt) || r.Received.After(g.WallFloor) || !r.OriginalUntil.After(r.Received) || r.OriginalUntil.After(q.ValidUntil) || r.OriginalUntil.After(r.Received.Add(workspacegraph.MaxTTL)) {
			return invalid("invalid retained authenticated graph report")
		}
		if g.LastIssue == nil {
			return invalid("directory report has no complete issued membership evidence")
		}
		found := false
		for _, c := range g.LastIssue.Membership.Members {
			if c.Identity.Host == q.Source && c.Identity.Fingerprint == q.SourcePin && c.MembershipGeneration == q.SourceGeneration && !r.OriginalUntil.After(c.Identity.Expires) {
				found = true
			}
		}
		if !found {
			return invalid("graph source not linked to retained exact membership")
		}
		if q.Source == g.Authority.Host && (g.LastReport == nil || r.Hash != g.ReportHash) {
			return invalid("directory own report differs from source highwater")
		}
		seen[key] = true
	}

	for _, g := range issuers {
		refs := 0
		for _, ref := range g.ReportRefs {
			var stored *GraphReport
			for _, r := range v.Graph.Reports {
				if r.Workspace == g.Workspace && r.Report.Source == ref.Source {
					copy := r
					stored = &copy
					break
				}
			}
			if stored == nil || stored.Report.Sequence != ref.Sequence || stored.Hash != ref.Hash {
				return invalid("graph mandatory source highwater row was lost or changed")
			}
			refs++
		}
		count := 0
		for _, r := range v.Graph.Reports {
			if r.Workspace == g.Workspace {
				count++
			}
		}
		if refs != count {
			return invalid("graph complete source inventory differs from report rows")
		}
		if g.Role == "directory" && g.LastIssue != nil {
			own, requester := false, false
			m := g.LastIssue.Membership
			for _, r := range v.Graph.Reports {
				if r.Workspace != g.Workspace {
					continue
				}
				q := r.Report
				if q.Source == g.Authority.Host && q.Sequence == g.ReportSequence && r.Hash == g.ReportHash {
					own = true
				}
				if q.Source == m.Requester && q.SourceGeneration == m.RequesterGeneration && q.Nonce == m.Nonce {
					for _, c := range m.Members {
						if c.Identity.Host == m.Requester && c.Identity.Fingerprint == q.SourcePin {
							requester = true
						}
					}
				}
			}
			if !own || !requester {
				return invalid("graph directory issue lost exact own/requester report dependencies")
			}
		}
	}
	observed := map[string]bool{}
	for _, o := range v.Graph.Observations {
		g, ok := issuers[o.Workspace]
		if !ok || observed[o.Workspace] || g.Role != "member" || o.Schema != 1 || o.Authority != g.Authority || o.LocalPin != g.LocalPin || o.Received.Before(g.EnabledAt) || o.WallFloor.Before(o.Received) || o.WallFloor.After(g.WallFloor) || o.RequestStart.IsZero() || o.RequestStart.After(o.Received) {
			return invalid("invalid graph observation native authority/clock")
		}
		if e := validateGraphBundle(o.Bundle); e != nil {
			return e
		}
		s := o.Bundle.Snapshot
		if s.Workspace != g.Workspace || s.Generation != g.Generation || s.Requester != g.Authority.Host || !s.ValidUntil.After(o.Received) || s.Issued.Before(o.RequestStart.Add(-o.Bundle.Membership.Policy.ClockSkew)) {
			return invalid("graph observation original request/highwater mismatch")
		}
		q := o.SourceReport
		if q.ValidateStructure() != nil || q.Workspace != g.Workspace || q.Generation != g.Generation || q.Source != g.Authority.Host || q.SourcePin != g.LocalPin || q.SourceGeneration != s.RequesterGeneration || q.Nonce != s.Nonce || q.Issued != o.RequestStart || q.Issued.After(o.Received) || !q.ValidUntil.After(o.Received) || q.Sequence > g.ReportSequence || q.Sequence == g.ReportSequence && hash(q) != g.ReportHash {
			return invalid("graph observation lost exact original source report")
		}

		if e := validateDirectoryProbes(o, g); e != nil {
			return e
		}
		observed[o.Workspace] = true
	}
	return nil
}
func checkGraphCapacity(v Records, add func(any), bytes *int) error {
	if v.Graph == nil {
		return nil
	}
	for _, g := range v.Graph.Issuance {
		add(g)
		if !g.Closed {
			*bytes += 8192
		}
	}
	for _, r := range v.Graph.Reports {
		add(r)
	}
	for _, o := range v.Graph.Observations {
		add(o)
	}
	if *bytes > MaxLedgerBytes {
		return fmt.Errorf("graph ledger including final-close reserve exceeds capacity")
	}
	return nil
}
