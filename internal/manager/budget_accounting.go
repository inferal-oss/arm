package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

// Policy accounting is an overlay on actual leases. An ancestor's rollup is not
// another physical reservation. Unknown lifecycle states fail closed.
func budgetLeaseAmounts(lease model.Lease) (model.BudgetAmounts, bool, error) {
	zero := model.BudgetAmounts{Pools: map[string]int64{}}
	switch lease.State {
	case "queued", "terminal":
		return zero, false, nil
	case "provisioning", "active", "ending", "reclaiming", "needs_attention":
	default:
		return zero, false, model.Err("invalid_accounting", "lease lifecycle is unknown to policy accounting")
	}
	amount := model.BudgetAmounts{Resources: lease.Request.Resources, Pools: map[string]int64{}}
	for _, claim := range lease.Request.Pools {
		if claim.Pool == "" || claim.Quantity <= 0 {
			return zero, false, model.Err("invalid_accounting", "persisted policy pool charge is invalid")
		}
		next, err := addBudgetAmounts(amount, model.BudgetAmounts{Pools: map[string]int64{claim.Pool: claim.Quantity}})
		if err != nil {
			return zero, false, err
		}
		amount = next
	}
	checked, err := addBudgetAmounts(zero, amount)
	return checked, true, err
}

func (m *Manager) validateBudgetBinding(lease model.Lease, binding model.BudgetBinding) error {
	if lease.Request.Budget != "" && m.canonicalBudgetSelector(lease.Request.Budget) != binding.Selected {
		return model.Err("invalid_accounting", "request policy selector differs from immutable ledger binding")
	}
	if lease.Budget != nil && !reflect.DeepEqual(*lease.Budget, binding) {
		return model.Err("invalid_accounting", "lease policy evidence differs from immutable ledger binding")
	}
	if lease.Host != m.host || lease.Owner != m.owner || binding.Authority != m.host || binding.Owner != m.owner || binding.Bound.IsZero() || len(binding.Path) == 0 || len(binding.Path) > maxBudgetDepth || binding.Selected != binding.Path[len(binding.Path)-1] {
		return model.Err("invalid_accounting", "lease policy binding differs from authenticated owner/host or has invalid lineage")
	}
	path, err := m.resolveBudgetPath(binding.Selected)
	if err != nil {
		var api *model.Error
		if errors.As(err, &api) && api.Code == "budget_not_found" {
			return model.Err("invalid_accounting", "durable lease policy ancestry references a missing definition")
		}
		return err
	}
	ids := make([]string, len(path))
	for i, node := range path {
		ids[i] = node.ID
		if (lease.State == "queued" || consuming(lease.State) || lease.State == "reclaiming") && node.State == "deleted" {
			return model.Err("invalid_accounting", "outstanding lease points through a deleted policy budget")
		}
	}
	if !reflect.DeepEqual(ids, binding.Path) {
		return model.Err("invalid_accounting", "persisted policy ancestry changed; rebinding an allocation is unsupported")
	}
	return nil
}
func (m *Manager) budgetBinding(lease model.Lease) (model.BudgetBinding, bool, error) {
	var binding model.BudgetBinding
	var raw []byte
	err := m.db.QueryRow(`SELECT data FROM lease_budget_bindings WHERE lease=?`, lease.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, false, nil
	}
	if err != nil {
		return binding, false, err
	}
	if len(raw) > 16<<10 {
		return binding, false, model.Err("invalid_accounting", "lease policy binding exceeds bound")
	}
	if err = json.Unmarshal(raw, &binding); err != nil {
		return binding, false, model.Err("invalid_accounting", "durable policy binding JSON is malformed")
	}
	if err = m.validateBudgetBinding(lease, binding); err != nil {
		return binding, false, err
	}
	return binding, true, nil
}
func (m *Manager) resolveNewBudgetBinding(lease model.Lease, selected string) (model.BudgetBinding, error) {
	var binding model.BudgetBinding
	if lease.Host != m.host || lease.Owner != m.owner {
		return binding, model.Err("invalid_accounting", "policy binding requires this authority's authenticated owner")
	}
	path, err := m.resolveBudgetPath(selected)
	if err != nil {
		return binding, err
	}
	binding = model.BudgetBinding{Authority: m.host, Owner: m.owner, Selected: path[len(path)-1].ID, Bound: time.Now().UTC(), Path: []string{}}
	for _, node := range path {
		if node.State != "active" {
			return model.BudgetBinding{}, model.Err("budget_unavailable", "policy ancestry is draining or deleted; select an active budget")
		}
		binding.Path = append(binding.Path, node.ID)
	}
	return binding, nil
}
func insertBudgetBinding(tx *sql.Tx, lease string, binding model.BudgetBinding, kind string) error {
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO lease_budget_bindings(lease,data) VALUES(?,?)`, lease, string(raw)); err != nil {
		return err
	}
	return event(tx, kind, lease, binding)
}

// Migration adds separate binding evidence without rewriting lease request,
// grant, terminal, timestamp or historical username data. Admission must remain
// unavailable until this pass completes successfully.
func (m *Manager) migrateBudgetBindings() error {
	leases, err := m.list()
	if err != nil {
		return err
	}
	var version string
	err = m.db.QueryRow(`SELECT value FROM metadata WHERE key='budget_bindings_v1'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	initialized := err == nil
	if initialized && version != "1" {
		return model.Err("invalid_accounting", "unsupported policy binding schema version")
	}
	missing := map[string]model.BudgetBinding{}
	for _, lease := range leases {
		_, charged, err := budgetLeaseAmounts(lease)
		if err != nil {
			return err
		}
		if !charged && lease.State != "queued" {
			continue
		}
		if _, found, err := m.budgetBinding(lease); err != nil {
			return err
		} else if found {
			continue
		}
		if initialized || lease.Request.Budget != "" {
			return model.Err("invalid_accounting", "an initialized outstanding allocation lost its durable budget lineage; inspect the ledger before admitting work")
		}
		root, err := m.budgetRecord(ownerBudgetID(m.host, m.owner))
		if err != nil {
			return err
		}
		binding := model.BudgetBinding{Authority: m.host, Owner: m.owner, Selected: root.ID, Path: []string{root.ID}, Bound: time.Now().UTC()}
		if err := m.validateBudgetBinding(lease, binding); err != nil {
			return err
		}
		missing[lease.ID] = binding
	}
	if len(missing) == 0 && initialized {
		return nil
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, lease := range leases {
		binding, ok := missing[lease.ID]
		if !ok {
			continue
		}
		if err = insertBudgetBinding(tx, lease.ID, binding, "budget.binding_migrated"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('budget_bindings_v1','1') ON CONFLICT(key) DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit()
}
func (m *Manager) budgetCharges(leases []model.Lease) (map[string]model.BudgetAmounts, map[string][]model.BudgetUse, error) {
	amounts := map[string]model.BudgetAmounts{}
	users := map[string][]model.BudgetUse{}
	var tree *poolTree
	for _, lease := range leases {
		if tree == nil && poolCharged(lease.State) && len(lease.Request.Pools) > 0 {
			var err error
			tree, err = m.checkedPoolTree()
			if err != nil {
				return nil, nil, err
			}
		}
		charge, charged, err := m.poolBudgetLeaseAmountsWithTree(lease, tree)
		if err != nil {
			return nil, nil, err
		}
		if !charged {
			continue
		}
		binding, found, err := m.budgetBinding(lease)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, model.Err("invalid_accounting", "outstanding lease has no verified policy binding")
		}
		for _, id := range binding.Path {
			sum, err := addBudgetAmounts(amounts[id], charge)
			if err != nil {
				return nil, nil, err
			}
			amounts[id] = sum
			users[id] = append(users[id], model.BudgetUse{Lease: lease.ID, Owner: lease.Owner, OwnerUsername: lease.OwnerUsername, Agent: lease.Request.Agent, Project: lease.Request.Project, Reason: lease.Request.Reason, State: lease.State, Expires: lease.Expires, Amounts: charge})
		}
	}
	return amounts, users, nil
}
