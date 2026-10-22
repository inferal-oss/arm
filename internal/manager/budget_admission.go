package manager

import (
	"encoding/json"
	"sort"

	"github.com/yrashk/arm/internal/model"
)

// These helpers check only additional
// policy ceilings; ordinary host/pool/live-memory checks must still succeed.
// Caller holds m.mu so policy snapshots and charge decisions cannot interleave
// with definition mutation. alreadyQueued comes only from durable lease state,
// never the incoming request's willingness-to-wait flag.
func (m *Manager) budgetPolicySnapshot(binding model.BudgetBinding) ([]model.BudgetPolicyRevision, error) {
	path, err := m.resolveBudgetPath(binding.Selected)
	if err != nil {
		return nil, err
	}
	if len(path) != len(binding.Path) {
		return nil, model.Err("invalid_accounting", "policy lineage changed")
	}
	out := make([]model.BudgetPolicyRevision, len(path))
	for i, node := range path {
		if node.ID != binding.Path[i] || node.Authority != binding.Authority || node.Owner != binding.Owner {
			return nil, model.Err("invalid_accounting", "policy lineage changed")
		}
		out[i] = model.BudgetPolicyRevision{ID: node.ID, Revision: node.Revision, Limits: copyBudgetLimits(node.Limits)}
	}
	return out, nil
}
func budgetQuantity(value json.Number) *json.Number { return &value }
func budgetLimitBlockers(node model.Budget, want, charged model.BudgetAmounts) []model.QueueBlocker {
	blockers := []model.QueueBlocker{}
	available, _ := remainingBudgetLimits(node.Limits, charged)
	appendBlocker := func(kind, unit string, requested, remaining json.Number) {
		code := "budget_limit"
		if node.Root {
			code = "owner_budget"
		}
		blockers = append(blockers, model.QueueBlocker{Code: code, Resource: node.ID + "/" + kind, Unit: unit, Requested: budgetQuantity(requested), Available: budgetQuantity(remaining), Detail: "Policy ceiling is committed or reduced below outstanding obligations; this budget does not reserve future host capacity."})
	}
	if node.Limits.CPU != nil && want.Resources.CPU > 0 && (charged.Resources.CPU > *node.Limits.CPU || want.Resources.CPU > *available.CPU) {
		appendBlocker("cpu", "logical-core-equivalents", model.FloatQuantity(want.Resources.CPU), model.FloatQuantity(*available.CPU))
	}
	if node.Limits.Memory != nil && want.Resources.Memory > 0 && (charged.Resources.Memory > *node.Limits.Memory || want.Resources.Memory > *available.Memory) {
		appendBlocker("memory", "bytes", model.CountQuantity(want.Resources.Memory), model.CountQuantity(*available.Memory))
	}
	if node.Limits.Disk != nil && want.Resources.Disk > 0 && (charged.Resources.Disk > *node.Limits.Disk || want.Resources.Disk > *available.Disk) {
		appendBlocker("disk", "bytes", model.CountQuantity(want.Resources.Disk), model.CountQuantity(*available.Disk))
	}
	ids := make([]string, 0, len(want.Pools))
	for id := range want.Pools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		quantity := want.Pools[id]
		if limit, ok := node.Limits.Pools[id]; ok && quantity > 0 && (charged.Pools[id] > limit || quantity > available.Pools[id]) {
			appendBlocker(id, "count", model.CountQuantity(quantity), model.CountQuantity(available.Pools[id]))
		}
	}
	return blockers
}
func (m *Manager) budgetAdmissionBlockers(binding model.BudgetBinding, want model.BudgetAmounts, charged map[string]model.BudgetAmounts, alreadyQueued bool) ([]model.QueueBlocker, []model.BudgetPolicyRevision, error) {
	policies, err := m.budgetPolicySnapshot(binding)
	if err != nil {
		return nil, nil, err
	}
	if _, err = addBudgetAmounts(model.BudgetAmounts{}, want); err != nil {
		return nil, nil, err
	}
	blockers := []model.QueueBlocker{}
	for _, snapshot := range policies {
		node, err := m.budgetRecord(snapshot.ID)
		if err != nil {
			return nil, nil, err
		}
		if node.State == "deleted" {
			return nil, nil, model.Err("budget_deleted", "selected policy ancestry includes a tombstone")
		}
		if node.State == "draining" && !alreadyQueued {
			blockers = append(blockers, model.QueueBlocker{Code: "budget_draining", Resource: node.ID, Detail: "This policy ancestry no longer admits new requests; existing queued bindings retain their queue age."})
		}
		used := charged[node.ID]
		if _, err := addBudgetAmounts(model.BudgetAmounts{}, used); err != nil {
			return nil, nil, err
		}
		blockers = append(blockers, budgetLimitBlockers(node, want, used)...)
	}
	return blockers, policies, nil
}

func (m *Manager) queuedBudgetBlockers(l model.Lease, leases []model.Lease) ([]model.QueueBlocker, error) {
	if err := m.budgetPolicyError(); err != nil {
		return nil, err
	}
	binding, found, err := m.budgetBinding(l)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, model.Err("invalid_accounting", "queued lease has no verified policy binding")
	}
	charges, users, err := m.budgetCharges(leases)
	if err != nil {
		return nil, err
	}
	want, err := m.requestedBudgetAmounts(l.Request)
	if err != nil {
		return nil, err
	}
	blockers, _, err := m.budgetAdmissionBlockers(binding, want, charges, true)
	if err != nil {
		return nil, err
	}
	for i := range blockers {
		b := &blockers[i]
		for _, id := range binding.Path {
			if len(b.Resource) > len(id) && b.Resource[:len(id)+1] == id+"/" {
				dim := b.Resource[len(id)+1:]
				for _, u := range users[id] {
					positive := dim == "cpu" && u.Amounts.Resources.CPU > 0 || dim == "memory" && u.Amounts.Resources.Memory > 0 || dim == "disk" && u.Amounts.Resources.Disk > 0 || u.Amounts.Pools[dim] > 0
					if positive {
						b.BlockingCount++
						if len(b.Leases) < 32 {
							b.Leases = append(b.Leases, u.Lease)
						}
					}
				}
				if p, err := m.pool(dim); err == nil {
					b.Unit = p.Unit
				}
				break
			}
		}
	}
	return blockers, nil
}
