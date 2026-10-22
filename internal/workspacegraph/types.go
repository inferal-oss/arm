// Package workspacegraph validates bounded reachability observations, NOT
// membership, authentication or resource grants. Callers must derive Scope and
// reports from current pinned workspace TLS/ledger authority. A path alone can
// never admit a hop, and discovered nodes never become direct mesh peers.
package workspacegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	Schema           = 1
	MaxNodes         = 128
	MaxNeighbors     = 64
	MaxEdges         = 4096
	MaxPaths         = 4
	MaxPathNodes     = 8
	MaxSnapshotBytes = 768 << 10
	MaxReportBytes   = 32 << 10
	MaxTTL           = time.Minute
	MaxSkew          = 5 * time.Second
)

type Node struct {
	Host       string `json:"host"`
	Generation int64  `json:"membership_generation"`
	Pin        string `json:"certificate_sha256"`
	Relay      bool   `json:"relay_enabled"`
}
type Neighbor struct {
	Host       string `json:"host"`
	Generation int64  `json:"membership_generation"`
	Pin        string `json:"certificate_sha256"`
	Pair       string `json:"pair"`
}
type Report struct {
	Schema           int        `json:"schema_version"`
	Workspace        string     `json:"workspace"`
	Generation       string     `json:"generation"`
	Source           string     `json:"source_host"`
	SourceGeneration int64      `json:"source_generation"`
	SourcePin        string     `json:"source_pin"`
	Sequence         int64      `json:"report_sequence"`
	Nonce            string     `json:"request_nonce"`
	Issued           time.Time  `json:"issued_at"`
	ValidUntil       time.Time  `json:"valid_until"`
	Neighbors        []Neighbor `json:"neighbors"`
}
type Edge struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Pair string `json:"pair"`
}
type Snapshot struct {
	Schema              int       `json:"schema_version"`
	Workspace           string    `json:"workspace"`
	Generation          string    `json:"generation"`
	Directory           string    `json:"directory_host"`
	DirectoryPin        string    `json:"directory_pin"`
	MembershipHash      string    `json:"membership_sha256"`
	MembershipRevision  int64     `json:"membership_revision"`
	Requester           string    `json:"requester"`
	RequesterGeneration int64     `json:"requester_generation"`
	Nonce               string    `json:"request_nonce"`
	Revision            int64     `json:"graph_revision"`
	Issued              time.Time `json:"issued_at"`
	ValidUntil          time.Time `json:"valid_until"`
	Nodes               []Node    `json:"nodes"`
	Edges               []Edge    `json:"edges"`
	Hash                string    `json:"sha256"`
}

// Scope is in-process membership authority, never a caller JSON assertion.
// Nodes must be derived from the EXACT same current membership snapshot hash
// and request nonce as this issue. Names/addresses and ordinary grants are absent.
type Scope struct {
	Workspace, Generation, Directory, DirectoryPin, MembershipHash, Requester, Nonce string
	MembershipRevision, RequesterGeneration, Revision                                int64
	At, MembershipUntil                                                              time.Time
	Nodes                                                                            []Node
}

// Observation's boot/deadline is process-local evidence established from the
// original authenticated request start. Persisted Reports alone are DISPLAY
// observations after restart. They cannot be re-timestamped into live hints.
type Observation struct {
	Report   Report
	Boot     string
	Deadline time.Duration
}

func hexValue(s string, n int) bool {
	v, e := hex.DecodeString(s)
	return e == nil && len(v) == n && strings.ToLower(s) == s
}
func validNode(n Node) bool { return hexValue(n.Host, 16) && n.Generation > 0 && hexValue(n.Pin, 32) }
func reportHash(r Report) string {
	v, e := json.Marshal(r)
	if e != nil {
		return ""
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:])
}
func (s Snapshot) Digest() string {
	s.Hash = ""
	v, e := json.Marshal(s)
	if e != nil {
		return ""
	}
	sum := sha256.Sum256(v)
	return hex.EncodeToString(sum[:])
}
func (r Report) ValidateStructure() error {
	if r.Schema != Schema || !hexValue(r.Workspace, 16) || !hexValue(r.Generation, 16) || !hexValue(r.Source, 16) || r.SourceGeneration < 1 || !hexValue(r.SourcePin, 32) || r.Sequence < 1 || !hexValue(r.Nonce, 32) || r.Issued.IsZero() || !r.ValidUntil.After(r.Issued) || r.ValidUntil.Sub(r.Issued) > MaxTTL || r.Neighbors == nil || len(r.Neighbors) > MaxNeighbors {
		return fmt.Errorf("invalid bounded graph report")
	}
	previous := ""
	for _, n := range r.Neighbors {
		if !hexValue(n.Host, 16) || n.Host == r.Source || n.Host <= previous || n.Generation < 1 || !hexValue(n.Pin, 32) || !hexValue(n.Pair, 32) {
			return fmt.Errorf("graph report neighbor is noncanonical/duplicate/unbound")
		}
		previous = n.Host
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > MaxReportBytes {
		return fmt.Errorf("complete graph report exceeds bound")
	}
	return nil
}
func (s Snapshot) ValidateStructure() error {
	if s.Schema != Schema || !hexValue(s.Workspace, 16) || !hexValue(s.Generation, 16) || !hexValue(s.Directory, 16) || !hexValue(s.DirectoryPin, 32) || !hexValue(s.MembershipHash, 32) || !hexValue(s.Requester, 16) || s.RequesterGeneration < 1 || s.MembershipRevision < 1 || s.Revision < 1 || !hexValue(s.Nonce, 32) || s.Issued.IsZero() || !s.ValidUntil.After(s.Issued) || s.ValidUntil.Sub(s.Issued) > MaxTTL || len(s.Nodes) < 1 || len(s.Nodes) > MaxNodes || s.Edges == nil || len(s.Edges) > MaxEdges || s.Hash != s.Digest() {
		return fmt.Errorf("invalid complete graph snapshot")
	}
	nodes := map[string]Node{}
	previous := ""
	for _, n := range s.Nodes {
		if !validNode(n) || n.Host <= previous {
			return fmt.Errorf("noncanonical/duplicate graph member")
		}
		previous = n.Host
		nodes[n.Host] = n
	}
	if nodes[s.Directory].Pin != s.DirectoryPin || nodes[s.Requester].Generation != s.RequesterGeneration {
		return fmt.Errorf("graph directory/requester is not exact current member")
	}
	previous = ""
	for _, edge := range s.Edges {
		key := edge.A + ":" + edge.B
		if edge.A >= edge.B || edge.A == "" || !hexValue(edge.Pair, 32) || !validNode(nodes[edge.A]) || !validNode(nodes[edge.B]) || key <= previous {
			return fmt.Errorf("unknown/duplicate/noncanonical graph edge")
		}
		previous = key
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > MaxSnapshotBytes {
		return fmt.Errorf("complete graph snapshot exceeds byte bound; nothing truncated")
	}
	return nil
}
func Build(scope Scope, observations []Observation, boot string, elapsed time.Duration) (Snapshot, error) {
	if len(scope.Nodes) < 1 || len(scope.Nodes) > MaxNodes {
		return Snapshot{}, fmt.Errorf("invalid bounded current member scope")
	}
	s := Snapshot{Schema: Schema, Workspace: scope.Workspace, Generation: scope.Generation, Directory: scope.Directory, DirectoryPin: scope.DirectoryPin, MembershipHash: scope.MembershipHash, MembershipRevision: scope.MembershipRevision, Requester: scope.Requester, RequesterGeneration: scope.RequesterGeneration, Nonce: scope.Nonce, Revision: scope.Revision, Issued: scope.At, ValidUntil: minTime(scope.MembershipUntil, scope.At.Add(MaxTTL)), Nodes: append([]Node{}, scope.Nodes...), Edges: []Edge{}}
	if boot == "" || elapsed < 0 || len(observations) > MaxNodes {
		return s, fmt.Errorf("invalid live graph observation bound")
	}
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].Host < s.Nodes[j].Host })
	nodes := map[string]Node{}
	for _, n := range s.Nodes {
		if !validNode(n) || nodes[n.Host].Host != "" {
			return s, fmt.Errorf("invalid exact current member scope")
		}
		nodes[n.Host] = n
	}
	latest := map[string]Observation{}
	for _, o := range observations {
		r := o.Report
		if r.Workspace != scope.Workspace || r.Generation != scope.Generation || r.Validate(scope.At) != nil || o.Boot != boot || o.Deadline <= elapsed {
			continue
		}
		n, ok := nodes[r.Source]
		if !ok || n.Generation != r.SourceGeneration || n.Pin != r.SourcePin {
			continue
		}
		if old, ok := latest[r.Source]; ok {
			if r.Sequence == old.Report.Sequence && reportHash(r) != reportHash(old.Report) {
				return s, fmt.Errorf("same graph sequence has conflicting evidence")
			}
			if r.Sequence <= old.Report.Sequence {
				continue
			}
		}
		latest[r.Source] = o
	}
	for _, o := range latest {
		for _, n := range o.Report.Neighbors {
			from := o.Report.Source
			if from >= n.Host {
				continue
			}
			current, ok := nodes[n.Host]
			if !ok || current.Generation != n.Generation || current.Pin != n.Pin {
				continue
			}
			other, ok := latest[n.Host]
			if !ok {
				continue
			}
			for _, back := range other.Report.Neighbors {
				own := nodes[from]
				if back.Host == from && back.Generation == own.Generation && back.Pin == own.Pin && back.Pair == n.Pair {
					s.Edges = append(s.Edges, Edge{A: from, B: n.Host, Pair: n.Pair})
					s.ValidUntil = minTime(s.ValidUntil, o.Report.ValidUntil, other.Report.ValidUntil, scope.At.Add(o.Deadline-elapsed), scope.At.Add(other.Deadline-elapsed))
					break
				}
			}
		}
	}
	sort.Slice(s.Edges, func(i, j int) bool { a, b := s.Edges[i], s.Edges[j]; return a.A < b.A || a.A == b.A && a.B < b.B })
	s.Hash = s.Digest()
	return s, s.Validate(scope.At)
}
func minTime(v time.Time, others ...time.Time) time.Time {
	for _, next := range others {
		if next.Before(v) {
			v = next
		}
	}
	return v
}

// ValidateFor checks a received snapshot AGAINST independently authenticated
// complete current membership and the exact live request. Self-consistent JSON
// hashes are not membership/identity proof. Revision rollback always refuses.
func (s Snapshot) ValidateFor(scope Scope) error {
	if e := s.Validate(scope.At); e != nil {
		return e
	}
	if s.Workspace != scope.Workspace || s.Generation != scope.Generation || s.Directory != scope.Directory || s.DirectoryPin != scope.DirectoryPin || s.MembershipHash != scope.MembershipHash || s.MembershipRevision != scope.MembershipRevision || s.Requester != scope.Requester || s.RequesterGeneration != scope.RequesterGeneration || s.Nonce != scope.Nonce || s.Revision < scope.Revision || s.ValidUntil.After(scope.MembershipUntil) || len(s.Nodes) != len(scope.Nodes) {
		return fmt.Errorf("graph does not match exact current membership/request/revision")
	}
	nodes := map[string]Node{}
	for _, n := range scope.Nodes {
		if !validNode(n) || nodes[n.Host].Host != "" {
			return fmt.Errorf("invalid exact current graph scope")
		}
		nodes[n.Host] = n
	}
	for _, n := range s.Nodes {
		expected, ok := nodes[n.Host]
		if !ok || n.Generation != expected.Generation || n.Pin != expected.Pin || n.Relay && !expected.Relay {
			return fmt.Errorf("graph changed a current member pin/generation or relay policy")
		}
	}
	return nil
}

func (r Report) Validate(now time.Time) error {
	if e := r.ValidateStructure(); e != nil {
		return e
	}
	if r.Issued.After(now.Add(MaxSkew)) || !r.ValidUntil.After(now) {
		return fmt.Errorf("graph report is not currently fresh")
	}
	return nil
}
func (s Snapshot) Validate(now time.Time) error {
	if e := s.ValidateStructure(); e != nil {
		return e
	}
	if s.Issued.After(now.Add(MaxSkew)) || !s.ValidUntil.After(now) {
		return fmt.Errorf("graph snapshot is not currently fresh")
	}
	return nil
}
