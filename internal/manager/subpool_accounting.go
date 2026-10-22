package manager

import (
	"math"

	"github.com/yrashk/arm/internal/model"
)

func addPoolCount(a, b int64) (int64, error) {
	if a < 0 || b < 0 || b > math.MaxInt64-a {
		return 0, model.Err("invalid_accounting", "pool partition quantities are negative or overflow int64")
	}
	return a + b, nil
}

func (t *poolTree) accounting(direct map[string]int64) (map[string]model.PoolPartition, error) {
	out := map[string]model.PoolPartition{}
	for id, n := range direct {
		if _, ok := t.nodes[id]; !ok || n < 0 {
			return nil, model.Err("invalid_accounting", "pool charge references missing definition or negative count")
		}
	}
	var visit func(string) (model.PoolPartition, error)
	visit = func(id string) (model.PoolPartition, error) {
		if value, ok := out[id]; ok {
			return value, nil
		}
		p := t.nodes[id]
		v := model.PoolPartition{Configured: p.Capacity, Direct: direct[id]}
		if p.State == "deleted" {
			if v.Direct != 0 {
				return v, model.Err("invalid_accounting", "deleted pool still holds charged claims")
			}
			for _, child := range t.children[id] {
				if t.nodes[child].State != "deleted" {
					return v, model.Err("invalid_accounting", "deleted pool has live child")
				}
			}
			out[id] = v
			return v, nil
		}
		for _, child := range t.children[id] {
			c, err := visit(child)
			if err != nil {
				return v, err
			}
			v.ChildBacking, err = addPoolCount(v.ChildBacking, c.Held)
			if err != nil {
				return v, err
			}
		}
		var err error
		v.Committed, err = addPoolCount(v.Direct, v.ChildBacking)
		if err != nil {
			return v, err
		}
		v.Held = max(v.Configured, v.Committed)
		v.Available = max(int64(0), v.Configured-v.Committed)
		v.Deficit = max(int64(0), v.Committed-v.Configured)
		v.PendingReturn = max(int64(0), v.Held-v.Configured)
		out[id] = v
		return v, nil
	}
	for id := range t.nodes {
		if _, err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (t *poolTree) admit(claims []model.PoolClaim, used map[string]int64) (bool, error) {
	next := make(map[string]int64, len(used)+len(claims))
	for id, n := range used {
		next[id] = n
	}
	relevant := map[string]bool{}
	for _, claim := range claims {
		p, found := t.nodes[claim.Pool]
		if !found || p.State == "deleted" {
			return false, model.Err("pool_deleted", "claim has no live pool")
		}
		if claim.Quantity <= 0 {
			return false, model.Err("invalid_accounting", "claim quantity must be positive")
		}
		var err error
		next[claim.Pool], err = addPoolCount(next[claim.Pool], claim.Quantity)
		if err != nil {
			// Valid existing charges can be full at MaxInt64. A new request
			// waits for release; hypothetical overflow is not ledger corruption.
			if _, beforeErr := t.accounting(used); beforeErr != nil {
				return false, beforeErr
			}
			return false, nil
		}
		for _, id := range t.paths[claim.Pool] {
			relevant[id] = true
		}
	}
	before, err := t.accounting(used)
	if err != nil {
		return false, err
	}
	after, err := t.accounting(next)
	if err != nil {
		// Existing state was validated above. Overflow in the hypothetical
		// bundled commitment is unavailable capacity, never a partial grant.
		return false, nil
	}
	for id := range relevant {
		if before[id].Deficit > 0 || after[id].Deficit > 0 {
			return false, nil
		}
	}
	return true, nil
}
