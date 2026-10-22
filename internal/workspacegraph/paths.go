package workspacegraph

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

func edgeKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + ":" + b
}

// Paths uses bounded Yen/BFS (at most4 results×8 spurs), rather than enumerating
// every simple path in a dense graph. Hints contain stable IDs only. All hops
// MUST still independently authenticate current direct/workspace authority.
func (s Snapshot) Paths(now time.Time, source, target string) ([][]string, error) {
	if e := s.Validate(now); e != nil {
		return nil, e
	}
	if source == target || !hexValue(source, 16) || !hexValue(target, 16) {
		return nil, fmt.Errorf("exact distinct route endpoints required")
	}
	nodes := map[string]Node{}
	neighbors := map[string][]string{}
	for _, n := range s.Nodes {
		nodes[n.Host] = n
	}
	if nodes[source].Host == "" || nodes[target].Host == "" {
		return nil, fmt.Errorf("route endpoint is not in exact graph membership")
	}
	for _, e := range s.Edges {
		neighbors[e.A] = append(neighbors[e.A], e.B)
		neighbors[e.B] = append(neighbors[e.B], e.A)
	}
	for h := range neighbors {
		sort.Strings(neighbors[h])
	}
	shortest := func(start string, blockedNodes, blockedEdges map[string]bool, maxNodes int) []string {
		queue := [][]string{{start}}
		seen := map[string]bool{start: true}
		for len(queue) > 0 {
			path := queue[0]
			queue = queue[1:]
			last := path[len(path)-1]
			if last == target {
				return path
			}
			if len(path) >= maxNodes || last != source && last != start && !nodes[last].Relay {
				continue
			}
			for _, next := range neighbors[last] {
				if seen[next] || blockedNodes[next] || blockedEdges[edgeKey(last, next)] {
					continue
				}
				if next != target && !nodes[next].Relay {
					continue
				}
				seen[next] = true
				queue = append(queue, append(append([]string{}, path...), next))
			}
		}
		return nil
	}
	first := shortest(source, map[string]bool{}, map[string]bool{}, MaxPathNodes)
	if first == nil {
		return [][]string{}, nil
	}
	accepted := [][]string{first}
	candidates := map[string][]string{}
	for len(accepted) < MaxPaths {
		last := accepted[len(accepted)-1]
		for i := 0; i < len(last)-1; i++ {
			prefix := last[:i+1]
			blockedNodes := map[string]bool{}
			for _, n := range prefix[:len(prefix)-1] {
				blockedNodes[n] = true
			}
			blockedEdges := map[string]bool{}
			for _, p := range accepted {
				if len(p) > i+1 && slices.Equal(p[:i+1], prefix) {
					blockedEdges[edgeKey(p[i], p[i+1])] = true
				}
			}
			spur := shortest(prefix[len(prefix)-1], blockedNodes, blockedEdges, MaxPathNodes-len(prefix)+1)
			if spur == nil {
				continue
			}
			candidate := append(append([]string{}, prefix[:len(prefix)-1]...), spur...)
			key := strings.Join(candidate, ":")
			duplicate := false
			for _, p := range accepted {
				if slices.Equal(p, candidate) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				candidates[key] = candidate
			}
		}
		if len(candidates) == 0 {
			break
		}
		keys := make([]string, 0, len(candidates))
		for k := range candidates {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := candidates[keys[i]], candidates[keys[j]]
			return len(a) < len(b) || len(a) == len(b) && keys[i] < keys[j]
		})
		selected := keys[0]
		accepted = append(accepted, candidates[selected])
		delete(candidates, selected)
	}
	return accepted, nil
}
