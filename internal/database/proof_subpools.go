package database

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/inferal-oss/arm/internal/model"
)

// Lineage and claim bindings are separate durable evidence. A current mutable
// definition alone cannot establish the ancestry that an old lease promised.
func (p *proofReader) readSubpools() error {
	if !p.subpoolSchema || len(p.pools) > 4096 {
		return fmt.Errorf("subpool schema marker missing or definition bound exceeded")
	}
	p.poolPaths = map[string][]string{}
	var path func(string, map[string]bool) ([]string, error)
	path = func(id string, seen map[string]bool) ([]string, error) {
		if v, ok := p.poolPaths[id]; ok {
			return v, nil
		}
		node, ok := p.pools[id]
		if !ok || seen[id] || len(seen) >= 16 {
			return nil, fmt.Errorf("subpool ancestry missing, cyclic or too deep")
		}
		seen[id] = true
		v := []string{}
		if node.Parent != "" {
			parent, ok := p.pools[node.Parent]
			if !ok || parent.Authority != node.Authority || parent.Owner != node.Owner || parent.Scope != node.Scope || parent.Host != node.Host || parent.Unit != node.Unit || parent.State == "deleted" && node.State != "deleted" {
				return nil, fmt.Errorf("subpool immutable parent identity differs")
			}
			prefix, err := path(node.Parent, seen)
			if err != nil {
				return nil, err
			}
			v = append(v, prefix...)
		}
		delete(seen, id)
		v = append(v, id)
		if len(v) > 16 {
			return nil, fmt.Errorf("subpool ancestry exceeds complete path bound")
		}
		p.poolPaths[id] = v
		return v, nil
	}
	for id := range p.pools {
		if _, err := path(id, map[string]bool{}); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	if err := p.walk("pool_lineage", func(r proofRow) error {
		id, err := r.text("pool")
		if err != nil {
			return err
		}
		v, err := rowJSON[model.PoolLineage](r, "data", "pool", "authority", "owner", "scope", "unit", "path")
		if err != nil {
			return err
		}
		node, ok := p.pools[id]
		expected := model.PoolLineage{Pool: id, Parent: node.Parent, Authority: node.Authority, Owner: node.Owner, Scope: node.Scope, Host: node.Host, Unit: node.Unit, Path: p.poolPaths[id]}
		if !ok || !reflect.DeepEqual(v, expected) {
			return fmt.Errorf("immutable pool lineage differs or references missing definition")
		}
		seen[id] = true
		return nil
	}); err != nil {
		return err
	}
	if len(seen) != len(p.pools) {
		return fmt.Errorf("pool immutable lineage is incomplete")
	}
	bound := map[string]bool{}
	if err := p.walk("pool_claim_bindings", func(r proofRow) error {
		id, err := r.text("lease")
		if err != nil {
			return err
		}
		v, err := rowJSON[model.PoolClaimBinding](r, "data", "authority", "owner", "bound_at", "claims")
		if err != nil {
			return err
		}
		lease, ok := p.leases[id]
		if !ok || len(lease.Request.Pools) == 0 || len(lease.Request.Pools) > 64 || v.Authority != p.result.Host || v.Owner != p.result.Owner || v.Bound.IsZero() || len(v.Claims) != len(lease.Request.Pools) {
			return fmt.Errorf("pool claim binding authority or complete lease scope differs")
		}
		if lease.PoolBinding != nil && !reflect.DeepEqual(*lease.PoolBinding, v) {
			return fmt.Errorf("embedded pool binding differs from durable claim evidence")
		}
		claims := map[string]model.PoolClaim{}
		for _, claim := range lease.Request.Pools {
			if _, ok := claims[claim.Pool]; ok {
				return fmt.Errorf("duplicate direct pool claim")
			}
			claims[claim.Pool] = claim
		}
		used := map[string]bool{}
		for i, claim := range v.Claims {
			want, ok := claims[claim.Pool]
			node, exists := p.pools[claim.Pool]
			if !ok || !exists || used[claim.Pool] || lease.Request.Pools[i].Pool != claim.Pool || claim.Quantity <= 0 || want.Quantity != claim.Quantity || want.Authority != node.Authority || want.Scope != node.Scope || want.Host != node.Host || !reflect.DeepEqual(claim.Path, p.poolPaths[claim.Pool]) {
				return fmt.Errorf("pool claim binding quantity/lineage differs")
			}
			used[claim.Pool] = true
		}
		bound[id] = true
		return nil
	}); err != nil {
		return err
	}
	for id, lease := range p.leases {
		if len(lease.Request.Pools) > 0 && !bound[id] {
			return fmt.Errorf("lease lost separate pool claim binding")
		}
	}
	// Configured idle children still consume parent backing. Validate its exact
	// arithmetic even though settled terminal leases contribute no direct charge.
	held := map[string]int64{}
	var backing func(string) (int64, error)
	backing = func(id string) (int64, error) {
		if n, ok := held[id]; ok {
			return n, nil
		}
		node := p.pools[id]
		var children int64
		for child, c := range p.pools {
			if c.Parent == id {
				n, err := backing(child)
				if err != nil {
					return 0, err
				}
				if n > math.MaxInt64-children {
					return 0, fmt.Errorf("subpool backing overflows int64")
				}
				children += n
			}
		}
		if node.State == "deleted" {
			if children != 0 {
				return 0, fmt.Errorf("deleted pool retains live child backing")
			}
			held[id] = 0
			return 0, nil
		}
		n := max(node.Capacity, children)
		held[id] = n
		return n, nil
	}
	for id := range p.pools {
		if _, err := backing(id); err != nil {
			return err
		}
	}
	return nil
}

func validPartition(v model.PoolPartition, deleted bool) bool {
	if v.Configured < 0 || v.Direct < 0 || v.ChildBacking < 0 || v.Committed < 0 || v.Held < 0 || v.Available < 0 || v.Deficit < 0 || v.PendingReturn < 0 {
		return false
	}
	if deleted {
		return v.Direct == 0 && v.ChildBacking == 0 && v.Committed == 0 && v.Held == 0 && v.Available == 0 && v.Deficit == 0 && v.PendingReturn == 0
	}
	if v.Direct > math.MaxInt64-v.ChildBacking {
		return false
	}
	committed := v.Direct + v.ChildBacking
	return v.Committed == committed && v.Held == max(v.Configured, committed) && v.Available == max(int64(0), v.Configured-committed) && v.Deficit == max(int64(0), committed-v.Configured) && v.PendingReturn == max(int64(0), v.Held-v.Configured)
}
func (p *proofReader) validatePartitionChange(v model.PoolChange) error {
	path := p.poolPaths[v.Pool.ID]
	current := p.pools[v.Pool.ID]
	if len(path) == 0 || current.Parent != v.Pool.Parent || current.Unit != v.Pool.Unit || current.Authority != v.Pool.Authority || current.Owner != v.Pool.Owner || current.Scope != v.Pool.Scope || current.Host != v.Pool.Host {
		return fmt.Errorf("journal pool differs from immutable lineage")
	}
	if len(v.Partitions) == 0 {
		// Root journals from the one-time migration predate partition observations.
		// Child definitions have never existed without this evidence.
		if v.Pool.Parent != "" {
			return fmt.Errorf("subpool journal lost partition observations")
		}
		return nil
	}
	if len(v.Partitions) != len(path) {
		return fmt.Errorf("partition journal path is incomplete")
	}
	entries := map[string]model.PoolPartitionChange{}
	for _, entry := range v.Partitions {
		if _, ok := entries[entry.Pool]; ok {
			return fmt.Errorf("duplicate partition journal node")
		}
		entries[entry.Pool] = entry
	}
	var child *model.PoolPartitionChange
	for i := len(path) - 1; i >= 0; i-- {
		id := path[i]
		entry, ok := entries[id]
		deleted := id == v.Pool.ID && v.Pool.State == "deleted"
		if !ok || !validPartition(entry.After, deleted) {
			return fmt.Errorf("invalid partition after observation")
		}
		if id == v.Pool.ID {
			if entry.After.Configured != v.Pool.Capacity {
				return fmt.Errorf("partition configured quantity differs from transition")
			}
			if v.Previous == nil {
				if entry.Before != nil || entry.After.Direct != 0 || entry.After.ChildBacking != 0 {
					return fmt.Errorf("new partition has prior charges")
				}
			} else if entry.Before == nil || entry.Before.Configured != v.Previous.Capacity || !validPartition(*entry.Before, v.Previous.State == "deleted") || entry.Before.Direct != entry.After.Direct || entry.Before.ChildBacking != entry.After.ChildBacking {
				return fmt.Errorf("partition mutation changes charges or previous capacity")
			}
		} else {
			if entry.Before == nil || !validPartition(*entry.Before, false) || entry.Before.Configured != entry.After.Configured || entry.Before.Direct != entry.After.Direct {
				return fmt.Errorf("ancestor partition mutation changes configured/direct quantity")
			}
			oldHeld := int64(0)
			if child.Before != nil {
				oldHeld = child.Before.Held
			}
			// Difference is checked without overflowing either signed side.
			if entry.Before.ChildBacking < oldHeld || entry.After.ChildBacking < child.After.Held || entry.Before.ChildBacking-oldHeld != entry.After.ChildBacking-child.After.Held {
				return fmt.Errorf("ancestor backing does not track changed child")
			}
		}
		copy := entry
		child = &copy
	}
	return nil
}

func (p *proofReader) poolJournalLineage() error {
	sequences := make([]int64, 0, len(p.poolChanges))
	for seq := range p.poolChanges {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	latest := map[string]model.Pool{}
	p.poolParentRevisions = map[int64]int64{}
	for _, seq := range sequences {
		v := p.poolChanges[seq]
		if v.Operation == "create" && v.Pool.Parent != "" {
			parent, ok := latest[v.Pool.Parent]
			if !ok || parent.State != "active" {
				return fmt.Errorf("child creation lacks active historical parent")
			}
			p.poolParentRevisions[seq] = parent.Revision
		}
		for _, entry := range v.Partitions {
			if entry.Pool == v.Pool.ID {
				continue
			}
			ancestor, ok := latest[entry.Pool]
			if !ok || ancestor.State == "deleted" || entry.Before == nil || entry.Before.Configured != ancestor.Capacity || entry.After.Configured != ancestor.Capacity {
				return fmt.Errorf("partition ancestor differs from historical definition")
			}
		}
		latest[v.Pool.ID] = v.Pool
	}
	return nil
}
