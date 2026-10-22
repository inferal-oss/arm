package workspacegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func graphFixture(t *testing.T, count int, links [][2]int) (Scope, []Observation, string) {
	t.Helper()
	now := time.Now().UTC()
	boot := strings.Repeat("b", 32)
	scope := Scope{Workspace: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Directory: fmt.Sprintf("%032x", 1), DirectoryPin: fmt.Sprintf("%064x", 1), MembershipHash: strings.Repeat("3", 64), Requester: fmt.Sprintf("%032x", 2), RequesterGeneration: 1, Nonce: strings.Repeat("4", 64), MembershipRevision: 7, Revision: 1, At: now, MembershipUntil: now.Add(time.Minute)}
	for i := 1; i <= count; i++ {
		scope.Nodes = append(scope.Nodes, Node{Host: fmt.Sprintf("%032x", i), Generation: 1, Pin: fmt.Sprintf("%064x", i), Relay: true})
	}
	reports := make([]Observation, count)
	for i, node := range scope.Nodes {
		r := Report{Schema: Schema, Workspace: scope.Workspace, Generation: scope.Generation, Source: node.Host, SourceGeneration: node.Generation, SourcePin: node.Pin, Sequence: 1, Nonce: strings.Repeat("5", 64), Issued: now, ValidUntil: now.Add(time.Minute), Neighbors: []Neighbor{}}
		for _, link := range links {
			next := 0
			if link[0] == i+1 {
				next = link[1]
			}
			if link[1] == i+1 {
				next = link[0]
			}
			if next == 0 {
				continue
			}
			to := scope.Nodes[next-1]
			a, b := link[0], link[1]
			if a > b {
				a, b = b, a
			}
			sum := sha256.Sum256([]byte(fmt.Sprintf("pair-%d-%d", a, b)))
			r.Neighbors = append(r.Neighbors, Neighbor{Host: to.Host, Generation: to.Generation, Pin: to.Pin, Pair: hex.EncodeToString(sum[:])})
		}
		slices.SortFunc(r.Neighbors, func(a, b Neighbor) int { return strings.Compare(a.Host, b.Host) })
		reports[i] = Observation{Report: r, Boot: boot, Deadline: time.Minute}
	}
	return scope, reports, boot
}
func TestGraphRequiresBothCurrentExactReportsAndNoRestartPromotion(t *testing.T) {
	scope, reports, boot := graphFixture(t, 3, [][2]int{{1, 2}, {1, 3}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil || len(s.Edges) != 2 {
		t.Fatal(s, e)
	}
	paths, e := s.Paths(scope.At, scope.Nodes[1].Host, scope.Nodes[2].Host)
	want := []string{scope.Nodes[1].Host, scope.Nodes[0].Host, scope.Nodes[2].Host}
	if e != nil || len(paths) != 1 || !slices.Equal(paths[0], want) {
		t.Fatal(paths, e)
	}
	for _, damage := range []string{"unilateral", "pair", "pin", "member-generation", "boot", "elapsed", "expired-wall"} {
		t.Run(damage, func(t *testing.T) {
			copy := append([]Observation{}, reports...)
			copy[2].Report.Neighbors = append([]Neighbor{}, reports[2].Report.Neighbors...)
			elapsed := time.Duration(0)
			switch damage {
			case "unilateral":
				copy[2].Report.Neighbors = []Neighbor{}
			case "pair":
				copy[2].Report.Neighbors[0].Pair = strings.Repeat("9", 64)
			case "pin":
				copy[2].Report.Neighbors[0].Pin = strings.Repeat("9", 64)
			case "member-generation":
				copy[2].Report.Neighbors[0].Generation++
			case "boot":
				copy[2].Boot = strings.Repeat("c", 32)
			case "elapsed":
				copy[2].Deadline = time.Second
				elapsed = 2 * time.Second
			case "expired-wall":
				copy[2].Report.ValidUntil = scope.At.Add(-time.Second)
			}
			current, e := Build(scope, copy, boot, elapsed)
			if e != nil {
				t.Fatal(e)
			}
			paths, e := current.Paths(scope.At, scope.Nodes[1].Host, scope.Nodes[2].Host)
			if e != nil || len(paths) != 0 {
				t.Fatal("unconfirmed/stale edge became usable", damage, paths, e)
			}
		})
	}
	restarted, e := Build(scope, reports, strings.Repeat("d", 32), 0)
	if e != nil || len(restarted.Edges) != 0 {
		t.Fatal("persisted reports became current after restart", restarted, e)
	}
	conflict := reports[0]
	conflict.Report.Neighbors = []Neighbor{}
	if _, e = Build(scope, append(reports, conflict), boot, 0); e == nil {
		t.Fatal("same report sequence silently changed immutable content")
	}
}
func TestGraphAlternateSimplePathsCyclesAndRelayOptOut(t *testing.T) {
	scope, reports, boot := graphFixture(t, 4, [][2]int{{1, 2}, {2, 4}, {1, 3}, {3, 4}, {2, 3}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	source, target := scope.Nodes[0].Host, scope.Nodes[3].Host
	paths, e := s.Paths(scope.At, source, target)
	if e != nil || len(paths) != 4 {
		t.Fatal(paths, e)
	}
	if len(paths[0]) != 3 || paths[0][1] != scope.Nodes[1].Host || paths[1][1] != scope.Nodes[2].Host {
		t.Fatal("shortest path order not stable", paths)
	}
	for _, path := range paths {
		seen := map[string]bool{}
		if len(path) > MaxPathNodes || path[0] != source || path[len(path)-1] != target {
			t.Fatal(path)
		}
		for _, h := range path {
			if seen[h] {
				t.Fatal("cyclic route escaped", path)
			}
			seen[h] = true
		}
	}
	// Breaking one confirmed edge leaves an actual alternate hint; no send or
	// delivery retry is performed by this reachability-only helper.
	reports[0].Report.Neighbors = reports[0].Report.Neighbors[1:]
	changed, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	alternative, e := changed.Paths(scope.At, source, target)
	if e != nil || len(alternative) < 1 || alternative[0][1] != scope.Nodes[2].Host {
		t.Fatal(alternative, e)
	}
	scope.Nodes[2].Relay = false
	changed, e = Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	alternative, e = changed.Paths(scope.At, source, target)
	if e != nil || len(alternative) != 0 {
		t.Fatal("unavailable relay became interior node", alternative, e)
	}
	// A node that does not relay can still be an explicitly addressed FINAL node.
	if paths, e = changed.Paths(scope.At, source, scope.Nodes[2].Host); e != nil || len(paths) == 0 {
		t.Fatal(paths, e)
	}
}
func TestGraphBoundsAndExactSnapshotMembershipBinding(t *testing.T) {
	scope, reports, boot := graphFixture(t, 10, [][2]int{{1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}, {7, 8}, {8, 9}, {9, 10}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	if paths, e := s.Paths(scope.At, scope.Nodes[0].Host, scope.Nodes[9].Host); e != nil || len(paths) != 0 {
		t.Fatal("route beyond8nodes returned", paths, e)
	}
	corrupt := s
	corrupt.Nodes = append([]Node{}, s.Nodes...)
	corrupt.Nodes[0].Pin = strings.Repeat("8", 64)
	corrupt.Hash = corrupt.Digest()
	if corrupt.Validate(scope.At) == nil {
		t.Fatal("snapshot silently changed directory pin")
	}
	corrupt = s
	corrupt.Edges = append(append([]Edge{}, s.Edges...), s.Edges[0])
	corrupt.Hash = corrupt.Digest()
	if corrupt.Validate(scope.At) == nil {
		t.Fatal("duplicate edge returned")
	}
	if _, e = s.Paths(s.ValidUntil, scope.Nodes[0].Host, scope.Nodes[1].Host); e == nil {
		t.Fatal("expired graph reused as current hint")
	}
}

func TestGraphSelfConsistentHashCannotReplaceIndependentMembership(t *testing.T) {
	scope, reports, boot := graphFixture(t, 3, [][2]int{{1, 2}, {1, 3}})
	s, e := Build(scope, reports, boot, 0)
	if e != nil || s.ValidateFor(scope) != nil {
		t.Fatal(s, e)
	}
	for _, change := range []func(*Snapshot){
		func(v *Snapshot) { v.Nodes = append([]Node{}, v.Nodes...); v.Nodes[2].Pin = strings.Repeat("f", 64) },
		func(v *Snapshot) { v.MembershipHash = strings.Repeat("e", 64) },
		func(v *Snapshot) { v.Nonce = strings.Repeat("d", 64) },
		func(v *Snapshot) { v.Nodes = append([]Node{}, v.Nodes...); v.Nodes[2].Generation++ },
		func(v *Snapshot) { v.Requester = scope.Nodes[2].Host },
	} {
		forged := s
		change(&forged)
		forged.Hash = forged.Digest()
		if forged.Validate(scope.At) != nil {
			t.Fatal("fixture should be structurally self-consistent")
		}
		if forged.ValidateFor(scope) == nil {
			t.Fatal("self-hash replaced independent current membership")
		}
	}
	scope.Revision = s.Revision + 1
	if s.ValidateFor(scope) == nil {
		t.Fatal("graph revision moved backward")
	}
}

func TestGraphHistoricalStructureAndExactCompletePages(t *testing.T) {
	links := [][2]int{}
	for a := 1; a <= 12; a++ {
		for b := a + 1; b <= 12; b++ {
			links = append(links, [2]int{a, b})
		}
	}
	scope, reports, boot := graphFixture(t, 12, links)
	s, e := Build(scope, reports, boot, 0)
	if e != nil {
		t.Fatal(e)
	}
	pages, e := Pages(s, 2048)
	if e != nil || len(pages) < 2 {
		t.Fatal(pages, e)
	}
	complete, e := Assemble(pages)
	if e != nil || complete.Hash != s.Hash || complete.ValidateFor(scope) != nil {
		t.Fatal(complete, e)
	}
	for _, bad := range [][]Page{pages[:len(pages)-1], append(append([]Page{}, pages...), pages[0])} {
		if _, e := Assemble(bad); e == nil {
			t.Fatal("partial/replayed graph pages accepted")
		}
	}
	changed := append([]Page{}, pages...)
	changed[1].Snapshot.Nonce = strings.Repeat("a", 64)
	if _, e := Assemble(changed); e == nil {
		t.Fatal("mixed nonce graph page accepted")
	}
	if e = s.Validate(s.ValidUntil.Add(time.Hour)); e == nil {
		t.Fatal("expired graph became currently valid")
	}
	if e = s.ValidateStructure(); e != nil {
		t.Fatal("historical snapshot expired into corruption", e)
	}
	report := reports[0].Report
	if e = report.Validate(report.ValidUntil); e == nil {
		t.Fatal("expired report became currently valid")
	}
	if e = report.ValidateStructure(); e != nil {
		t.Fatal("historical report expired into corruption", e)
	}
}
