package manager

import (
	"fmt"
	"reflect"

	"getarm.org/internal/model"
)

func (r *providerPairC2Reader) budget(id string) (model.Budget, error) {
	if v, ok := r.budgetCache[id]; ok {
		return v, nil
	}
	var v model.Budget
	if len(r.budgetCache) == providerPairC2Rows || !providerPairRequestKey(id) {
		return v, fmt.Errorf("census budget definition bound")
	}
	raw, e := r.raw("budgets", "id", id, 64<<10)
	if e != nil {
		return v, e
	}
	if e = decodeProviderPairC2JSON(raw, &v, 64<<10); e != nil {
		return v, e
	}
	var physical, owner, parent []byte
	var it, ot, pt string
	e = r.tx.QueryRowContext(r.ctx, `SELECT substr(CAST(id AS BLOB),1,129),typeof(id),substr(CAST(owner AS BLOB),1,11),typeof(owner),substr(CAST(parent AS BLOB),1,129),typeof(parent) FROM budgets WHERE id=?`, id).Scan(&physical, &it, &owner, &ot, &parent, &pt)
	if e != nil {
		return v, e
	}
	if it != "text" || string(physical) != id || v.ID != id || ot != "text" || string(owner) != r.owner || v.Owner != r.owner || v.Authority != r.host || v.Revision < 1 || v.Created.IsZero() || v.Updated.Before(v.Created) || !budgetText(v.Name, 128, true) || validBudgetLimits(v.Limits) != nil {
		return v, fmt.Errorf("census budget identity/limits")
	}
	if v.Root {
		if pt != "null" || len(parent) != 0 || v.Parent != "" || v.ID != ownerBudgetID(r.host, r.owner) || v.State == "deleted" {
			return v, fmt.Errorf("census budget root binding")
		}
	} else if pt != "text" || string(parent) != v.Parent || !providerPairRequestKey(v.Parent) {
		return v, fmt.Errorf("census budget parent binding")
	}
	switch v.State {
	case "active", "draining", "deleted":
	default:
		return v, fmt.Errorf("census budget state")
	}
	r.budgetCache[id] = v
	return v, nil
}
func (r *providerPairC2Reader) checkBudgetPath(path []string, retained bool) error {
	if len(path) == 0 || len(path) > 16 || path[0] != ownerBudgetID(r.host, r.owner) {
		return fmt.Errorf("census budget ancestry bound")
	}
	seen := map[string]bool{}
	for i, id := range path {
		if seen[id] {
			return fmt.Errorf("census budget ancestry cycle")
		}
		seen[id] = true
		node, e := r.budget(id)
		if e != nil {
			return e
		}
		if node.Root != (i == 0) || i > 0 && node.Parent != path[i-1] || retained && node.State == "deleted" {
			return fmt.Errorf("census immutable budget ancestry differs")
		}
	}
	return nil
}
func (r *providerPairC2Reader) budgetPath(l model.Lease) ([]string, error) {
	raw, e := r.raw("lease_budget_bindings", "lease", l.ID, providerPairC2BudgetBytes)
	if e != nil {
		return nil, e
	}
	var b model.BudgetBinding
	if e = decodeProviderPairC2JSON(raw, &b, providerPairC2BudgetBytes); e != nil {
		return nil, e
	}
	selected := l.Request.Budget
	if selected == "" || selected == "self" {
		selected = ownerBudgetID(r.host, r.owner)
	}
	if b.Authority != r.host || b.Owner != r.owner || b.Selected != selected || len(b.Path) == 0 || b.Selected != b.Path[len(b.Path)-1] || b.Bound.IsZero() || l.Budget != nil && !reflect.DeepEqual(*l.Budget, b) {
		return nil, fmt.Errorf("census budget sidecar differs")
	}
	if e = r.checkBudgetPath(b.Path, l.State != "terminal"); e != nil {
		return nil, e
	}
	if len(l.BudgetPolicy) != 0 {
		if len(l.BudgetPolicy) != len(b.Path) {
			return nil, fmt.Errorf("census recorded budget policies differ")
		}
		for i, p := range l.BudgetPolicy {
			if p.ID != b.Path[i] || p.Revision < 1 || r.budgetCache[p.ID].Revision < p.Revision || validBudgetLimits(p.Limits) != nil {
				return nil, fmt.Errorf("census recorded budget policy")
			}
		}
	}
	return b.Path, nil
}
func (r *providerPairC2Reader) pool(id string) (model.Pool, error) {
	if p, ok := r.poolCache[id]; ok {
		return p, nil
	}
	var p model.Pool
	if !providerPairRequestKey(id) || len(r.poolCache) == providerPairC2Rows {
		return p, fmt.Errorf("census pool definition bound")
	}
	raw, e := r.raw("pools", "id", id, 64<<10)
	if e != nil {
		return p, e
	}
	if e = decodeProviderPairC2JSON(raw, &p, 64<<10); e != nil {
		return p, e
	}
	var indexedID, scope, host, name []byte
	var it, st, ht, nt string
	e = r.tx.QueryRowContext(r.ctx, `SELECT substr(CAST(id AS BLOB),1,129),typeof(id),substr(CAST(scope AS BLOB),1,17),typeof(scope),substr(CAST(host AS BLOB),1,33),typeof(host),substr(CAST(name AS BLOB),1,129),typeof(name) FROM pools WHERE id=?`, id).Scan(&indexedID, &it, &scope, &st, &host, &ht, &name, &nt)
	if e != nil {
		return p, e
	}
	if it != "text" || string(indexedID) != id || p.ID != id || st != "text" || string(scope) != p.Scope || ht != "text" || string(host) != p.Host || nt != "text" || string(name) != p.Name || p.Authority != r.host || p.Owner != r.owner || p.Revision < 1 || p.Capacity < 0 || p.Created.IsZero() || p.Updated.Before(p.Created) || !poolText(p.Name, 128, true) || !poolText(p.Unit, 64, true) {
		return p, fmt.Errorf("census pool definition identity")
	}
	if p.Scope != "global" && p.Scope != "host" || p.Scope == "global" && p.Host != "" || p.Scope == "host" && p.Host != r.host {
		return p, fmt.Errorf("census pool scope")
	}
	switch p.State {
	case "active", "draining", "deleted":
	default:
		return p, fmt.Errorf("census pool state")
	}
	r.poolCache[id] = p
	return p, nil
}
func (r *providerPairC2Reader) poolPath(id string) ([]string, error) {
	leaf, e := r.pool(id)
	if e != nil {
		return nil, e
	}
	reverse := []string{}
	seen := map[string]bool{}
	for id != "" {
		if seen[id] || len(reverse) == 16 {
			return nil, fmt.Errorf("census pool ancestry cycle/bound")
		}
		seen[id] = true
		p, e := r.pool(id)
		if e != nil {
			return nil, e
		}
		if p.Authority != leaf.Authority || p.Owner != leaf.Owner || p.Scope != leaf.Scope || p.Host != leaf.Host || p.Unit != leaf.Unit || p.State == "deleted" {
			return nil, fmt.Errorf("census pool ancestry identity/state")
		}
		reverse = append(reverse, id)
		id = p.Parent
	}
	path := make([]string, len(reverse))
	for i, id := range reverse {
		path[len(reverse)-i-1] = id
	}
	// Check immutable definition lineage as well as the mutable definition's
	// parent field. Neither can silently reparent the other's charged claims.
	for i, id := range path {
		raw, e := r.raw("pool_lineage", "pool", id, 16<<10)
		if e != nil {
			return nil, e
		}
		var v model.PoolLineage
		if e = decodeProviderPairC2JSON(raw, &v, 16<<10); e != nil {
			return nil, e
		}
		p := r.poolCache[id]
		want := model.PoolLineage{Pool: id, Parent: p.Parent, Authority: p.Authority, Owner: p.Owner, Scope: p.Scope, Host: p.Host, Unit: p.Unit, Path: path[:i+1]}
		if !reflect.DeepEqual(v, want) {
			return nil, fmt.Errorf("census immutable pool lineage differs")
		}
	}
	return path, nil
}
func (r *providerPairC2Reader) poolAmounts(l model.Lease) (map[string]int64, error) {
	amounts := map[string]int64{}
	if len(l.Request.Pools) == 0 {
		var present int
		if e := r.tx.QueryRowContext(r.ctx, `SELECT EXISTS(SELECT 1 FROM pool_claim_bindings WHERE lease=?)`, l.ID).Scan(&present); e != nil {
			return nil, e
		}
		if present != 0 {
			return nil, fmt.Errorf("census unexpected durable pool binding")
		}
		if l.PoolBinding != nil {
			return nil, fmt.Errorf("census unexpected embedded pool binding")
		}
		return amounts, nil
	}
	raw, e := r.raw("pool_claim_bindings", "lease", l.ID, 64<<10)
	if e != nil {
		return nil, e
	}
	var b model.PoolClaimBinding
	if e = decodeProviderPairC2JSON(raw, &b, 64<<10); e != nil {
		return nil, e
	}
	if b.Authority != r.host || b.Owner != r.owner || b.Bound.IsZero() || len(b.Claims) != len(l.Request.Pools) || l.PoolBinding != nil && !reflect.DeepEqual(*l.PoolBinding, b) {
		return nil, fmt.Errorf("census immutable pool binding")
	}
	seen := map[string]bool{}
	for i, claim := range l.Request.Pools {
		if seen[claim.Pool] || claim.Quantity <= 0 {
			return nil, fmt.Errorf("census duplicate/invalid pool claim")
		}
		seen[claim.Pool] = true
		path, e := r.poolPath(claim.Pool)
		if e != nil {
			return nil, e
		}
		definition := r.poolCache[claim.Pool]
		if claim.Authority != definition.Authority || claim.Scope != definition.Scope || claim.Host != definition.Host || b.Claims[i].Pool != claim.Pool || b.Claims[i].Quantity != claim.Quantity || !reflect.DeepEqual(b.Claims[i].Path, path) {
			return nil, fmt.Errorf("census pool claim ancestry/scope")
		}
		for _, id := range path {
			if e = providerPairC2AddPool(amounts, id, claim.Quantity); e != nil {
				return nil, e
			}
		}
	}
	return amounts, nil
}

// Settled ordinary history may refer to deleted pool definitions; still inspect
// its raw immutable claim sidecar rather than ignoring malformed terminal data.
func (r *providerPairC2Reader) terminalPoolBinding(l model.Lease) error {
	raw, e := r.raw("pool_claim_bindings", "lease", l.ID, 64<<10)
	if e != nil {
		return e
	}
	var b model.PoolClaimBinding
	if e = decodeProviderPairC2JSON(raw, &b, 64<<10); e != nil {
		return e
	}
	if b.Authority != r.host || b.Owner != r.owner || b.Bound.IsZero() || len(b.Claims) != len(l.Request.Pools) || len(b.Claims) == 0 || l.PoolBinding != nil && !reflect.DeepEqual(*l.PoolBinding, b) {
		return fmt.Errorf("terminal pool sidecar differs")
	}
	seen := map[string]bool{}
	for i, c := range b.Claims {
		if seen[c.Pool] || c.Pool != l.Request.Pools[i].Pool || c.Quantity <= 0 || c.Quantity != l.Request.Pools[i].Quantity || len(c.Path) == 0 || len(c.Path) > 16 || c.Path[len(c.Path)-1] != c.Pool {
			return fmt.Errorf("terminal pool claim differs")
		}
		seen[c.Pool] = true
		ancestors := map[string]bool{}
		for _, id := range c.Path {
			if !providerPairRequestKey(id) || ancestors[id] {
				return fmt.Errorf("terminal pool ancestry differs")
			}
			ancestors[id] = true
		}
	}
	return nil
}

func (r *providerPairC2Reader) checkPairPolicyRevisions(pair providerPairDecoded) error {
	path := pair.budgets[0].Path
	if e := r.checkBudgetPath(path, true); e != nil {
		return e
	}
	for _, member := range pair.leases {
		if len(member.Policies) != len(path) {
			return fmt.Errorf("paired recorded policy ancestry differs")
		}
		for i, recorded := range member.Policies {
			if recorded.ID != path[i] || recorded.Revision < 1 || r.budgetCache[recorded.ID].Revision < recorded.Revision {
				return fmt.Errorf("paired policy revision rolled back")
			}
		}
	}
	return nil
}
