package manager

import (
	"sort"

	"getarm.org/internal/model"
)

const maxPoolDepth = 16
const maxPoolDefinitions = 4096

type poolTree struct {
	nodes    map[string]model.Pool
	paths    map[string][]string
	children map[string][]string
}

func buildPoolTree(definitions []model.Pool) (*poolTree, error) {
	if len(definitions) > maxPoolDefinitions {
		return nil, model.Err("invalid_accounting", "pool definition count exceeds bounded hierarchy")
	}
	t := &poolTree{nodes: map[string]model.Pool{}, paths: map[string][]string{}, children: map[string][]string{}}
	for _, p := range definitions {
		if p.ID == "" || len(p.ID) > 128 || p.Capacity < 0 || p.Revision < 1 || p.Authority == "" || p.Owner == "" || p.Unit == "" || (p.Scope != "host" && p.Scope != "global") || (p.Scope == "host" && p.Host != p.Authority) || (p.Scope == "global" && p.Host != "") || (p.State != "active" && p.State != "draining" && p.State != "deleted") {
			return nil, model.Err("invalid_accounting", "pool definition identity, state or quantity is invalid")
		}
		if _, exists := t.nodes[p.ID]; exists {
			return nil, model.Err("invalid_accounting", "pool definition IDs are not distinct")
		}
		t.nodes[p.ID] = p
	}
	var visit func(string, map[string]bool) ([]string, error)
	visit = func(id string, seen map[string]bool) ([]string, error) {
		if path := t.paths[id]; path != nil {
			return path, nil
		}
		p, found := t.nodes[id]
		if !found || seen[id] || len(seen) >= maxPoolDepth {
			return nil, model.Err("invalid_accounting", "pool ancestry is missing, cyclic or too deep")
		}
		seen[id] = true
		var path []string
		if p.Parent != "" {
			parent, found := t.nodes[p.Parent]
			if !found || parent.Authority != p.Authority || parent.Owner != p.Owner || parent.Scope != p.Scope || parent.Host != p.Host || parent.Unit != p.Unit || (parent.State == "deleted" && p.State != "deleted") {
				return nil, model.Err("invalid_accounting", "subpool identity differs from its parent or has a deleted ancestor")
			}
			prefix, err := visit(p.Parent, seen)
			if err != nil {
				return nil, err
			}
			path = append(path, prefix...)
		}
		delete(seen, id)
		path = append(path, id)
		if len(path) > maxPoolDepth {
			return nil, model.Err("invalid_accounting", "pool ancestry is too deep")
		}
		t.paths[id] = path
		return path, nil
	}
	for id, p := range t.nodes {
		if _, err := visit(id, map[string]bool{}); err != nil {
			return nil, err
		}
		if p.Parent != "" {
			t.children[p.Parent] = append(t.children[p.Parent], id)
		}
	}
	for id := range t.children {
		sort.Strings(t.children[id])
	}
	return t, nil
}
