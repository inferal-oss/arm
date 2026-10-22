package manager

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/model"
	"sort"
	"time"
)

func budgetPageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 100 {
		return 0, model.Err("invalid_request", "budget page limit must be 1 through 100")
	}
	return limit, nil
}
func (m *Manager) Budgets(q model.BudgetQuery) (model.BudgetList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := model.BudgetList{Budgets: []model.Budget{}, Root: ownerBudgetID(m.host, m.owner)}
	limit, err := budgetPageLimit(q.Limit)
	if err != nil {
		return result, err
	}
	if len(q.After) > 128 {
		return result, model.Err("invalid_request", "invalid budget cursor")
	}
	if q.Parent != "" {
		q.Parent = m.canonicalBudgetSelector(q.Parent)
		if _, err = m.resolveBudgetPath(q.Parent); err != nil {
			return result, err
		}
	}
	// Scan IDs first: the ledger uses one SQLite connection and record validation
	// must run after closing the cursor, never via an open nested query.
	rows, err := m.db.Query(`SELECT id FROM budgets WHERE owner=? AND id>? AND (?='' OR parent=?) AND (? OR json_extract(data,'$.state')<>'deleted') ORDER BY id LIMIT ?`, m.owner, q.After, q.Parent, q.Parent, q.All, limit+1)
	if err != nil {
		return result, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(ids) > limit {
		result.More = true
		ids = ids[:limit]
	}
	for _, id := range ids {
		node, err := m.budgetRecord(id)
		if err != nil {
			return result, err
		}
		result.Budgets = append(result.Budgets, node)
		result.Cursor = id
	}
	return result, nil
}
func (m *Manager) BudgetInspect(id string) (model.BudgetView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result model.BudgetView
	path, err := m.resolveBudgetPath(id)
	if err != nil {
		return result, err
	}
	leases, err := m.list()
	if err != nil {
		return result, err
	}
	charged, users, err := m.budgetCharges(leases)
	if err != nil {
		return result, err
	}
	node := path[len(path)-1]
	result.Budget = node
	result.Charged = charged[node.ID]
	result.Users = len(users[node.ID])
	result.Path = []model.BudgetConstraint{}
	for _, p := range path {
		available, deficit := remainingBudgetLimits(p.Limits, charged[p.ID])
		result.Path = append(result.Path, model.BudgetConstraint{Budget: p, Charged: charged[p.ID], Available: available, Deficit: deficit})
		result.Available = minBudgetLimits(result.Available, available)
		if p.ID == node.ID {
			result.Deficit = deficit
		}
	}
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE owner=? AND parent=? AND json_extract(data,'$.state')<>'deleted'`, m.owner, node.ID).Scan(&result.Children); err != nil {
		return result, err
	}
	for _, l := range leases {
		if l.State != "queued" {
			continue
		}
		binding, found, err := m.budgetBinding(l)
		if err != nil {
			return result, err
		}
		if !found {
			return result, model.Err("invalid_accounting", "queued lease lost policy lineage")
		}
		for _, ancestor := range binding.Path {
			if ancestor == node.ID {
				result.Queued++
				break
			}
		}
	}
	result.Accounting = "policy-ceilings-only; ancestor rollups overlap; no resource reservation reuse; queued requests charge zero; unresolved allocations remain charged"
	result.Observed = time.Now().UTC()
	return result, nil
}
func (m *Manager) BudgetConsumers(q model.BudgetQuery) (model.BudgetConsumers, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := model.BudgetConsumers{Users: []model.BudgetUse{}}
	path, err := m.resolveBudgetPath(q.ID)
	if err != nil {
		return result, err
	}
	result.Budget = path[len(path)-1].ID
	limit, err := budgetPageLimit(q.Limit)
	if err != nil {
		return result, err
	}
	if len(q.After) > 128 {
		return result, model.Err("invalid_request", "invalid consumer cursor")
	}
	leases, err := m.list()
	if err != nil {
		return result, err
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].ID < leases[j].ID })
	for _, l := range leases {
		amount, charged, err := m.poolBudgetLeaseAmounts(l)
		if err != nil {
			return result, err
		}
		if !charged && l.State != "queued" {
			continue
		}
		b, found, err := m.budgetBinding(l)
		if err != nil {
			return result, err
		}
		if !found {
			return result, model.Err("invalid_accounting", "outstanding allocation lost policy lineage")
		}
		if l.ID <= q.After {
			continue
		}
		matches := false
		for _, id := range b.Path {
			if id == result.Budget {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if len(result.Users) == limit {
			result.More = true
			break
		}
		if l.State == "queued" {
			amount, err = m.requestedBudgetAmounts(l.Request)
			if err != nil {
				return result, err
			}
		}
		result.Users = append(result.Users, model.BudgetUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, State: l.State, Expires: l.Expires, Amounts: amount})
		result.Cursor = l.ID
	}
	return result, nil
}
func (m *Manager) BudgetHistory(q model.BudgetQuery) (model.BudgetHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := model.BudgetHistory{Entries: []model.BudgetChange{}, Cursor: q.Sequence}
	path, err := m.resolveBudgetPath(q.ID)
	if err != nil {
		return result, err
	}
	result.Budget = path[len(path)-1].ID
	limit, err := budgetPageLimit(q.Limit)
	if err != nil {
		return result, err
	}
	if q.Sequence < 0 {
		return result, model.Err("invalid_request", "journal cursor must be nonnegative")
	}
	rows, err := m.db.Query(`SELECT sequence,data FROM budget_changes WHERE budget=? AND sequence>? ORDER BY sequence LIMIT ?`, result.Budget, q.Sequence, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			return result, err
		}
		if len(result.Entries) == limit {
			result.More = true
			break
		}
		if len(raw) > 128<<10 {
			return result, model.Err("invalid_accounting", "policy journal record exceeds bound")
		}
		var change model.BudgetChange
		if err = json.Unmarshal(raw, &change); err != nil {
			return result, err
		}
		if change.Budget.Owner != m.owner || change.Budget.Authority != m.host || change.Budget.ID != result.Budget {
			return result, model.Err("invalid_accounting", "policy journal identity mismatch")
		}
		change.Sequence = seq
		result.Entries = append(result.Entries, change)
		result.Cursor = seq
	}
	return result, rows.Err()
}
