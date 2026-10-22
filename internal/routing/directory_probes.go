package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

// DirectoryProbe is a historical native observation of an actually completed
// pinned-directory refresh path. It has NO current membership/delivery authority.
// Every future hop must independently reauthorize current pins, denies and time.
type DirectoryProbe struct {
	Schema             int                   `json:"schema_version"`
	Workspace          string                `json:"workspace"`
	Generation         string                `json:"generation"`
	Source             string                `json:"source_host"`
	SourceGeneration   int64                 `json:"source_generation"`
	SourcePin          string                `json:"source_pin"`
	Directory          string                `json:"directory_host"`
	DirectoryPin       string                `json:"directory_pin"`
	MembershipRevision int64                 `json:"membership_revision"`
	SourceSequence     int64                 `json:"source_report_sequence"`
	Nonce              string                `json:"request_nonce"`
	Observed           time.Time             `json:"observed_at"`
	Nodes              []workspacegraph.Node `json:"nodes"`
}

func probePath(p DirectoryProbe) []string {
	out := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		out[i] = n.Host
	}
	return out
}
func probeKey(p DirectoryProbe) string { raw, _ := json.Marshal(p.Nodes); return string(raw) }
func validateDirectoryProbes(o GraphObservation, g GraphIssuance) error {
	if o.DirectoryProbes == nil {
		return nil
	}
	if len(o.DirectoryProbes) < 1 || len(o.DirectoryProbes) > 4 {
		return invalid("bounded nonempty historical directory probes required")
	}
	m := o.Bundle.Membership
	cards := map[string]workspace.MemberCard{}
	for _, c := range m.Members {
		cards[c.Identity.Host] = c
	}
	seen := map[string]bool{}
	for i, p := range o.DirectoryProbes {
		if p.Schema != 1 || p.Workspace != g.Workspace || p.Generation != g.Generation || p.Source != g.Authority.Host || p.SourceGeneration != m.RequesterGeneration || p.SourcePin != g.LocalPin || p.Directory != m.Directory.Host || p.DirectoryPin != m.Directory.Fingerprint || p.MembershipRevision < 1 || p.MembershipRevision > m.Revision || p.SourceSequence < 1 || p.SourceSequence > o.SourceReport.Sequence || p.SourceSequence == o.SourceReport.Sequence && p.Nonce != o.SourceReport.Nonce || !hexID(p.Nonce, 32) || p.Observed.Before(g.EnabledAt) || p.Observed.After(o.Received) || len(p.Nodes) < 2 || len(p.Nodes) > workspacegraph.MaxPathNodes {
			return invalid("directory probe original source/directory/sequence binding differs")
		}
		pathSeen := map[string]bool{}
		for index, n := range p.Nodes {
			c, ok := cards[n.Host]
			if !ok || pathSeen[n.Host] || n.Pin != c.Identity.Fingerprint || n.Generation != c.MembershipGeneration || n.Relay && !m.Policy.Relay || index > 0 && index < len(p.Nodes)-1 && !n.Relay {
				return invalid("directory probe node differs from retained exact membership/relay lineage")
			}
			pathSeen[n.Host] = true
		}
		if p.Nodes[0].Host != p.Source || p.Nodes[0].Pin != p.SourcePin || p.Nodes[0].Generation != p.SourceGeneration || p.Nodes[len(p.Nodes)-1].Host != p.Directory || p.Nodes[len(p.Nodes)-1].Pin != p.DirectoryPin || p.Nodes[len(p.Nodes)-1].Generation != 1 {
			return invalid("directory probe endpoints changed")
		}
		key := probeKey(p)
		if seen[key] || i > 0 && (o.DirectoryProbes[i-1].Observed.Before(p.Observed) || o.DirectoryProbes[i-1].Observed == p.Observed && probeKey(o.DirectoryProbes[i-1]) >= key) {
			return invalid("directory probe order/unique path differs")
		}
		seen[key] = true
	}
	return nil
}

// AcceptGraphDirectoryPath is Go-only: path comes from the actual authenticated
// source Open.Path AFTER complete reception, never any forwarded request field.
// It shares Runtime.AcceptChecked's membership+graph transaction and rollback.
func AcceptGraphDirectoryPath(ctx context.Context, tx *sql.Tx, b workspace.DirectoryAcceptance, s workspacegraph.Snapshot, path []string) (workspace.DirectoryDependent, error) {
	if len(path) < 2 || len(path) > workspacegraph.MaxPathNodes {
		return workspace.DirectoryDependent{}, fmt.Errorf("actual bounded directory path required")
	}
	dependent, e := AcceptGraph(ctx, tx, b, s)
	if e != nil {
		return workspace.DirectoryDependent{}, e
	}
	after, e := Read(ctx, tx)
	if e != nil {
		return workspace.DirectoryDependent{}, e
	}
	var o GraphObservation
	var g GraphIssuance
	for _, candidate := range after.Graph.Observations {
		if candidate.Workspace == b.Snapshot.Workspace {
			o = candidate
		}
	}
	g, _ = graphIssuer(after, b.Snapshot.Workspace)
	previous := append([]DirectoryProbe(nil), o.DirectoryProbes...)
	p := DirectoryProbe{Schema: 1, Workspace: o.Workspace, Generation: s.Generation, Source: b.Local.Host, SourceGeneration: b.Snapshot.RequesterGeneration, SourcePin: b.Local.Fingerprint, Directory: b.Snapshot.Directory.Host, DirectoryPin: b.Snapshot.Directory.Fingerprint, MembershipRevision: b.Snapshot.Revision, SourceSequence: o.SourceReport.Sequence, Nonce: o.SourceReport.Nonce, Observed: b.At, Nodes: []workspacegraph.Node{}}
	for _, host := range path {
		found := false
		for _, c := range b.Snapshot.Members {
			if c.Identity.Host == host {
				p.Nodes = append(p.Nodes, workspacegraph.Node{Host: host, Generation: c.MembershipGeneration, Pin: c.Identity.Fingerprint, Relay: b.Snapshot.Policy.Relay})
				found = true
				break
			}
		}
		if !found {
			return workspace.DirectoryDependent{}, invalid("actual directory path has unknown retained member")
		}
	}
	o.DirectoryProbes = []DirectoryProbe{p}
	for _, old := range previous {
		if probeKey(old) != probeKey(p) {
			o.DirectoryProbes = append(o.DirectoryProbes, old)
		}
	}
	sort.Slice(o.DirectoryProbes, func(i, j int) bool {
		a, b := o.DirectoryProbes[i], o.DirectoryProbes[j]
		if a.Observed != b.Observed {
			return a.Observed.After(b.Observed)
		}
		return probeKey(a) < probeKey(b)
	})
	if len(o.DirectoryProbes) > 4 {
		o.DirectoryProbes = o.DirectoryProbes[:4]
	}
	if e = validateDirectoryProbes(o, g); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	if e = graphPut(ctx, tx, `INSERT INTO routing_graph_observations(workspace,data) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET data=excluded.data`, o, o.Workspace); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	if _, e = Read(ctx, tx); e != nil {
		return workspace.DirectoryDependent{}, e
	}
	return dependent, nil
}
