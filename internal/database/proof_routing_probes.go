package database

import (
	"encoding/json"
	"fmt"
)

// This independent mirror checks retained local reachability observations. It
// does not authorize a stream or infer current membership from historical pins.
func graphProofDirectoryProbes(o proofGraphObservation, g proofGraphIssuance) error {
	if o.DirectoryProbes == nil {
		return nil
	}
	if len(o.DirectoryProbes) < 1 || len(o.DirectoryProbes) > 4 {
		return fmt.Errorf("directory probe count invalid")
	}
	m := o.Bundle.Membership
	cards := map[string]proofWSMemberCard{}
	for _, card := range m.Members {
		cards[card.Identity.Host] = card
	}
	seen := map[string]bool{}
	for i, p := range o.DirectoryProbes {
		if p.Schema != 1 || p.Workspace != g.Workspace || p.Generation != g.Generation || p.Source != g.Authority.Host || p.SourceGeneration != m.RequesterGeneration || p.SourcePin != g.LocalPin || p.Directory != m.Directory.Host || p.DirectoryPin != m.Directory.Fingerprint || p.MembershipRevision < 1 || p.MembershipRevision > m.Revision || p.SourceSequence < 1 || p.SourceSequence > o.SourceReport.Sequence || p.SourceSequence == o.SourceReport.Sequence && p.Nonce != o.SourceReport.Nonce || !sshProofHex(p.Nonce, 32) || p.Observed.Before(g.EnabledAt) || p.Observed.After(o.Received) || len(p.Nodes) < 2 || len(p.Nodes) > 8 {
			return fmt.Errorf("directory probe source/directory/report lineage differs")
		}
		nodes := map[string]bool{}
		for j, n := range p.Nodes {
			card, ok := cards[n.Host]
			if !ok || nodes[n.Host] || n.Pin != card.Identity.Fingerprint || n.Generation != card.MembershipGeneration || n.Relay && !m.Policy.Relay || j > 0 && j < len(p.Nodes)-1 && !n.Relay {
				return fmt.Errorf("directory probe pin/generation/relay node differs")
			}
			nodes[n.Host] = true
		}
		first, last := p.Nodes[0], p.Nodes[len(p.Nodes)-1]
		if first.Host != p.Source || first.Pin != p.SourcePin || first.Generation != p.SourceGeneration || last.Host != p.Directory || last.Pin != p.DirectoryPin || last.Generation != 1 {
			return fmt.Errorf("directory probe endpoint differs")
		}
		key := workspaceProofCanonicalNodes(p.Nodes)
		if seen[key] || i > 0 && (o.DirectoryProbes[i-1].Observed.Before(p.Observed) || o.DirectoryProbes[i-1].Observed == p.Observed && workspaceProofCanonicalNodes(o.DirectoryProbes[i-1].Nodes) >= key) {
			return fmt.Errorf("directory probe path order/duplicate differs")
		}
		seen[key] = true
	}
	return nil
}

func workspaceProofCanonicalNodes(nodes []proofGraphNode) string {
	raw, _ := json.Marshal(nodes)
	return string(raw)
}
