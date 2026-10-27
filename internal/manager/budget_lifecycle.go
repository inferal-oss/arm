package manager

import (
	"errors"
	"github.com/inferal-oss/arm/internal/model"
)

func (m *Manager) canonicalBudgetSelector(id string) string {
	if id == "" || id == "self" {
		return ownerBudgetID(m.host, m.owner)
	}
	return id
}
func requestedBudgetAmounts(r model.Request) (model.BudgetAmounts, error) {
	amount, _, err := budgetLeaseAmounts(model.Lease{State: "active", Request: r})
	return amount, err
}

// Only public read projections attach this evidence. Provider cleanup must still
// work when policy evidence is corrupt; admission fails closed independently.
func (m *Manager) attachBudgetEvidence(l *model.Lease) {
	if err := m.budgetPolicyError(); err != nil {
		l.Budget = nil
		l.BudgetError = err
		return
	}
	binding, found, err := m.budgetBinding(*l)
	l.Budget = nil
	l.BudgetError = nil
	if err == nil && !found && l.State != "terminal" {
		err = model.Err("invalid_accounting", "outstanding lease has no verified policy binding")
	}
	if err != nil {
		var api *model.Error
		if errors.As(err, &api) {
			l.BudgetError = api
		} else {
			l.BudgetError = &model.Error{Code: "invalid_accounting", Message: "policy evidence could not be read"}
		}
		return
	}
	if found {
		l.Budget = &binding
	}
}
func (m *Manager) budgetQueuedFit(l model.Lease) (bool, []model.BudgetPolicyRevision, error) {
	if len(l.Request.Pools) > 0 {
		t, err := m.checkedPoolTree()
		if err != nil {
			return false, nil, err
		}
		if _, err = m.poolClaimBinding(t, l); err != nil {
			return false, nil, err
		}
	}
	binding, found, err := m.budgetBinding(l)
	if err != nil {
		return false, nil, err
	}
	if !found {
		return false, nil, model.Err("invalid_accounting", "queued lease has no verified policy binding")
	}
	leases, err := m.list()
	if err != nil {
		return false, nil, err
	}
	charged, _, err := m.budgetCharges(leases)
	if err != nil {
		return false, nil, err
	}
	want, err := m.requestedBudgetAmounts(l.Request)
	if err != nil {
		return false, nil, err
	}
	blockers, policies, err := m.budgetAdmissionBlockers(binding, want, charged, true)
	return len(blockers) == 0, policies, err
}
func (m *Manager) budgetRenewal(l model.Lease, leases []model.Lease) ([]model.BudgetPolicyRevision, error) {
	binding, found, err := m.budgetBinding(l)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, model.Err("invalid_accounting", "renewal requires verified policy lineage")
	}
	charges, _, err := m.budgetCharges(leases)
	if err != nil {
		return nil, err
	}
	policies, err := m.budgetPolicySnapshot(binding)
	if err != nil {
		return nil, err
	}
	own, err := m.requestedBudgetAmounts(l.Request)
	if err != nil {
		return nil, err
	}
	// Existing allocation is already in each ancestor total. Only relevant
	// dimensions constrain renewal, and equality is valid (no double count).
	for _, p := range policies {
		node, err := m.budgetRecord(p.ID)
		if err != nil {
			return nil, err
		}
		if node.State != "active" {
			return nil, model.Err("renewal_denied", "policy ancestry is draining or deleted")
		}
		_, deficit := remainingBudgetLimits(node.Limits, charges[node.ID])
		if (own.Resources.CPU > 0 && deficit.Resources.CPU > 0) || (own.Resources.Memory > 0 && deficit.Resources.Memory > 0) || (own.Resources.Disk > 0 && deficit.Resources.Disk > 0) {
			return nil, model.Err("renewal_denied", "policy ceilings have a deficit in this allocation's resource dimensions")
		}
		for id, q := range own.Pools {
			if q > 0 && deficit.Pools[id] > 0 {
				return nil, model.Err("renewal_denied", "policy pool ceiling has an outstanding deficit")
			}
		}
	}
	return policies, nil
}
