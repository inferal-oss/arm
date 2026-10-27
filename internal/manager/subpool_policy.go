package manager

import "getarm.org/internal/model"

func expandPoolAmounts(amount model.BudgetAmounts, paths map[string][]string) (model.BudgetAmounts, error) {
	out := amount
	out.Pools = map[string]int64{}
	for id, q := range amount.Pools {
		path := paths[id]
		if len(path) == 0 {
			return out, model.Err("invalid_accounting", "policy charge lacks immutable pool ancestry")
		}
		for _, ancestor := range path {
			n, err := addPoolCount(out.Pools[ancestor], q)
			if err != nil {
				return out, err
			}
			out.Pools[ancestor] = n
		}
	}
	return out, nil
}
func (m *Manager) requestedBudgetAmounts(r model.Request) (model.BudgetAmounts, error) {
	amount, err := requestedBudgetAmounts(r)
	if err != nil || len(r.Pools) == 0 {
		return amount, err
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return amount, err
	}
	return expandPoolAmounts(amount, t.paths)
}
func (m *Manager) poolBudgetLeaseAmounts(l model.Lease) (model.BudgetAmounts, bool, error) {
	var tree *poolTree
	if poolCharged(l.State) && len(l.Request.Pools) > 0 {
		var err error
		tree, err = m.checkedPoolTree()
		if err != nil {
			return model.BudgetAmounts{}, true, err
		}
	}
	return m.poolBudgetLeaseAmountsWithTree(l, tree)
}
func (m *Manager) poolBudgetLeaseAmountsWithTree(l model.Lease, t *poolTree) (model.BudgetAmounts, bool, error) {
	amount, charged, err := budgetLeaseAmounts(l)
	if err != nil || !charged || len(l.Request.Pools) == 0 {
		return amount, charged, err
	}
	if t == nil {
		return amount, charged, model.Err("invalid_accounting", "policy charge lacks verified pool tree")
	}
	binding, err := m.poolClaimBinding(t, l)
	if err != nil {
		return amount, charged, err
	}
	paths := map[string][]string{}
	for _, c := range binding.Claims {
		paths[c.Pool] = c.Path
	}
	amount, err = expandPoolAmounts(amount, paths)
	return amount, charged, err
}
func (m *Manager) poolAdmissionAncestry(claims []model.PoolClaim) error {
	if err := m.poolPolicyError(); err != nil {
		return err
	}
	if len(claims) == 0 {
		return nil
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return err
	}
	for _, c := range claims {
		for _, id := range t.paths[c.Pool] {
			if t.nodes[id].State != "active" {
				return model.Err("pool_unavailable", "pool ancestry is draining or deleted; existing queued claims retain their place")
			}
		}
	}
	return nil
}
func (m *Manager) poolRenewal(l model.Lease, leases []model.Lease) error {
	if err := m.poolPolicyError(); err != nil {
		return err
	}
	if len(l.Request.Pools) == 0 {
		return nil
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return err
	}
	if _, err = m.poolClaimBinding(t, l); err != nil {
		return err
	}
	used, err := poolReservations(leases)
	if err != nil {
		return err
	}
	a, err := t.accounting(used)
	if err != nil {
		return err
	}
	for _, c := range l.Request.Pools {
		for _, id := range t.paths[c.Pool] {
			if t.nodes[id].State != "active" || a[id].Deficit > 0 {
				return model.Err("renewal_denied", "pool ancestry is draining or has a retained backing deficit")
			}
		}
	}
	return nil
}
func (m *Manager) attachPoolEvidence(l *model.Lease) {
	recorded := *l
	l.PoolBinding = nil
	l.PoolError = m.poolPolicyError()
	if l.PoolError != nil || len(l.Request.Pools) == 0 {
		return
	}
	t, err := m.checkedPoolTree()
	if err == nil {
		var v model.PoolClaimBinding
		v, err = m.poolClaimBinding(t, recorded)
		if err == nil {
			l.PoolBinding = &v
			return
		}
	}
	l.PoolError = &model.Error{Code: "invalid_accounting", Message: "Immutable pool lineage could not be verified; admission is paused for affected work"}
}

func poolCharged(state string) bool { return consuming(state) || state == "reclaiming" }
