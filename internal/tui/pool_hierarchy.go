package tui

import (
	"fmt"
	"sort"

	"github.com/inferal-oss/arm/internal/model"
)

func poolTreeOrder(views []model.PoolView) ([]model.PoolView, error) {
	nodes := map[string]model.PoolView{}
	children := map[string][]string{}
	for _, v := range views {
		if v.Pool.ID == "" || nodes[v.Pool.ID].Pool.ID != "" {
			return nil, fmt.Errorf("pool hierarchy has duplicate/missing identity")
		}
		nodes[v.Pool.ID] = v
	}
	for _, v := range views {
		if v.Pool.Parent != "" && nodes[v.Pool.Parent].Pool.ID == "" {
			return nil, fmt.Errorf("pool hierarchy is incomplete; refresh the complete roster")
		}
		children[v.Pool.Parent] = append(children[v.Pool.Parent], v.Pool.ID)
	}
	for id := range children {
		sort.Strings(children[id])
	}
	out := []model.PoolView{}
	seen := map[string]bool{}
	var walk func(string, int) error
	walk = func(id string, depth int) error {
		if depth >= 16 || seen[id] {
			return fmt.Errorf("pool ancestry is cyclic or exceeds its bound")
		}
		seen[id] = true
		v := nodes[id]
		if v.Pool.Parent != "" {
			p := nodes[v.Pool.Parent].Pool
			if p.Authority != v.Pool.Authority || p.Scope != v.Pool.Scope || p.Host != v.Pool.Host || p.Unit != v.Pool.Unit {
				return fmt.Errorf("pool child differs from inherited authority, scope or unit")
			}
		}
		path := []string{id}
		if v.Pool.Parent != "" {
			path = append(append([]string{}, nodes[v.Pool.Parent].Ancestors...), id)
		}
		v.Ancestors = path
		nodes[id] = v
		out = append(out, v)
		for _, child := range children[id] {
			if e := walk(child, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	for _, id := range children[""] {
		if e := walk(id, 0); e != nil {
			return nil, e
		}
	}
	if len(out) != len(nodes) {
		return nil, fmt.Errorf("pool hierarchy is cyclic or incomplete")
	}
	return out, nil
}
