package manager

import (
	"sort"

	"github.com/yrashk/arm/internal/model"
)

// Canonicalize immutable child identity before keyed lookup. State/revision and
// capacity checks occur only after lookup, so old exact retries remain stable.
func (m *Manager) normalizeSubpoolCreate(r *model.PoolMutation) error {
	if r.Parent == "" {
		if r.ParentRevision != nil {
			return model.Err("invalid_request", "parent revision requires a parent pool")
		}
		return nil
	}
	if r.Operation != "create" || r.ParentRevision == nil || *r.ParentRevision < 1 {
		return model.Err("invalid_request", "child creation requires --parent and a positive --parent-revision; parentage cannot change")
	}
	p, err := m.pool(r.Parent)
	if err != nil {
		return err
	}
	if r.Scope != "" && r.Scope != p.Scope || r.Host != "" && r.Host != p.Host && !(r.Host == "localhost" && p.Host == m.host) || r.Unit != "" && r.Unit != p.Unit {
		return model.Err("invalid_request", "child scope, host and unit must match its parent")
	}
	r.Scope, r.Host, r.Unit = p.Scope, p.Host, p.Unit
	return nil
}
func (m *Manager) validateSubpoolMutation(r model.PoolMutation, p model.Pool) ([]model.PoolPartitionChange, model.PoolLineage, error) {
	var lineage model.PoolLineage
	t, err := m.checkedPoolTree()
	if err != nil {
		return nil, lineage, err
	}
	if err = m.validatePoolBindings(t); err != nil {
		return nil, lineage, err
	}
	leases, err := m.list()
	if err != nil {
		return nil, lineage, err
	}
	used, err := poolReservations(leases)
	if err != nil {
		return nil, lineage, err
	}
	before, err := t.accounting(used)
	if err != nil {
		return nil, lineage, err
	}
	if r.Operation == "create" && p.Parent != "" {
		parent, ok := t.nodes[p.Parent]
		if !ok {
			return nil, lineage, model.Err("pool_not_found", "parent pool is missing")
		}
		if parent.Revision != *r.ParentRevision {
			return nil, lineage, model.Err("revision_conflict", "parent changed; inspect it before child creation")
		}
		if parent.State != "active" {
			return nil, lineage, model.Err("pool_unavailable", "parent is draining or deleted; select an active parent before carving a child")
		}
	}
	if r.Operation == "delete" {
		for _, id := range t.children[p.ID] {
			if t.nodes[id].State != "deleted" {
				return nil, lineage, model.Err("pool_in_use", "delete live child partitions before deleting their parent")
			}
		}
		for _, l := range leases {
			if !poolCharged(l.State) && l.State != "queued" {
				continue
			}
			for _, c := range l.Request.Pools {
				for _, id := range t.paths[c.Pool] {
					if id == p.ID {
						return nil, lineage, model.Err("pool_in_use", "queued or unresolved descendant claims still retain this partition")
					}
				}
			}
		}
	}
	defs := make([]model.Pool, 0, len(t.nodes)+1)
	for id, n := range t.nodes {
		if id != p.ID {
			defs = append(defs, n)
		}
	}
	defs = append(defs, p)
	next, err := buildPoolTree(defs)
	if err != nil {
		return nil, lineage, err
	}
	after, err := next.accounting(used)
	if err != nil {
		return nil, lineage, err
	}
	increased := r.Operation == "create" || p.Capacity > t.nodes[p.ID].Capacity
	if increased && p.Parent != "" {
		for _, id := range next.paths[p.Parent] {
			if next.nodes[id].State != "active" {
				return nil, lineage, model.Err("pool_unavailable", "ancestor is draining or deleted")
			}
			if before[id].Deficit > 0 || after[id].Deficit > 0 {
				return nil, lineage, model.Err("insufficient_capacity", "ancestor has insufficient uncarved backing; child capacity is never created from nothing")
			}
		}
	}
	// A drained parent also blocks reopening descendants until it is resumed.
	if r.Operation == "resume" && p.Parent != "" {
		for _, id := range next.paths[p.Parent] {
			if next.nodes[id].State != "active" {
				return nil, lineage, model.Err("pool_unavailable", "resume ancestors before reopening this partition")
			}
		}
	}
	changes := []model.PoolPartitionChange{}
	ids := append([]string{}, next.paths[p.ID]...)
	sort.Strings(ids)
	for _, id := range ids {
		v := after[id]
		var previous *model.PoolPartition
		if old, ok := before[id]; ok {
			copyOld := old
			previous = &copyOld
		}
		changes = append(changes, model.PoolPartitionChange{Pool: id, Before: previous, After: v})
	}
	return changes, poolLineage(next, p.ID), nil
}
