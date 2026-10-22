package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/yrashk/arm/internal/model"
)

func (p *proofReader) validPool(v model.Pool) error {
	if !evidenceID(v.ID) || v.Authority != p.result.Host || v.Owner != p.result.Owner || v.Name == "" || v.Unit == "" || v.Capacity < 0 || v.Revision < 1 || v.Created.IsZero() || v.Updated.Before(v.Created) || !oneOf(v.State, "active", "draining", "deleted") || v.Accounting != "cooperative-counted-permits" || v.Expiry != "return-after-lease-settlement" {
		return fmt.Errorf("unsupported or inconsistent pool definition")
	}
	if v.Scope == "host" && v.Host == p.result.Host || v.Scope == "global" && v.Host == "" {
		return nil
	}
	return fmt.Errorf("pool scope/authority differs")
}
func (p *proofReader) readPools() error {
	return p.walk("pools", func(r proofRow) error {
		v, err := rowJSON[model.Pool](r, "data", "id", "name", "scope", "authority", "owner", "unit", "capacity", "revision", "state", "accounting", "expiry_policy", "created_at", "updated_at")
		if err != nil {
			return err
		}
		if err = p.validPool(v); err != nil {
			return err
		}
		for key, value := range map[string]string{"id": v.ID, "scope": v.Scope, "host": v.Host, "name": v.Name} {
			indexed, e := r.text(key)
			if e != nil || indexed != value {
				return fmt.Errorf("indexed pool %s differs", key)
			}
		}
		p.pools[v.ID] = v
		return nil
	})
}
func (p *proofReader) validBudget(v model.Budget) error {
	if !evidenceID(v.ID) || v.Authority != p.result.Host || v.Owner != p.result.Owner || v.Name == "" || v.Revision < 1 || v.Created.IsZero() || v.Updated.Before(v.Created) || !oneOf(v.State, "active", "draining", "deleted") {
		return fmt.Errorf("invalid policy definition")
	}
	if v.Root && (v.Parent != "" || v.State == "deleted") || !v.Root && v.Parent == "" {
		return fmt.Errorf("invalid policy root/parent")
	}
	if r := v.Limits.CPU; r != nil && (math.IsNaN(*r) || math.IsInf(*r, 0) || *r < 0) {
		return fmt.Errorf("invalid policy CPU ceiling")
	}
	if v.Limits.Memory != nil && *v.Limits.Memory < 0 || v.Limits.Disk != nil && *v.Limits.Disk < 0 {
		return fmt.Errorf("invalid policy byte ceiling")
	}
	for id, q := range v.Limits.Pools {
		if !evidenceID(id) || q < 0 {
			return fmt.Errorf("invalid policy pool ceiling")
		}
	}
	if v.Root {
		raw, _ := json.Marshal([]string{p.result.Host, "local_uid", p.result.Owner})
		sum := sha256.Sum256(raw)
		if v.ID != "budget_owner_"+hex.EncodeToString(sum[:]) {
			return fmt.Errorf("policy root does not match authenticated owner")
		}
	}
	return nil
}
func (p *proofReader) readBudgets() error {
	if err := p.walk("budgets", func(r proofRow) error {
		v, err := rowJSON[model.Budget](r, "data", "id", "authority", "owner", "root", "name", "limits", "state", "revision", "created_at", "updated_at")
		if err != nil {
			return err
		}
		if err = p.validBudget(v); err != nil {
			return err
		}
		id, e := r.text("id")
		owner, f := r.text("owner")
		if e != nil || f != nil || id != v.ID || owner != v.Owner {
			return fmt.Errorf("indexed policy identity differs")
		}
		if v.Root {
			if r["parent"] != nil {
				return fmt.Errorf("root indexed parent must be NULL")
			}
		} else {
			parent, e := r.text("parent")
			if e != nil || parent != v.Parent {
				return fmt.Errorf("indexed policy parent differs")
			}
		}
		p.budgets[id] = v
		return nil
	}); err != nil {
		return err
	}
	roots := 0
	for _, v := range p.budgets {
		if v.Root {
			roots++
		}
		seen := map[string]bool{}
		at := v
		for !at.Root {
			if len(seen) >= 16 || seen[at.ID] {
				return fmt.Errorf("policy ancestry cycles or exceeds bound")
			}
			seen[at.ID] = true
			parent, ok := p.budgets[at.Parent]
			if !ok {
				return fmt.Errorf("policy parent missing")
			}
			at = parent
		}
	}
	if roots != 1 {
		return fmt.Errorf("expected exactly one local authenticated policy root")
	}
	return p.walk("lease_budget_bindings", func(r proofRow) error {
		id, err := r.text("lease")
		if err != nil {
			return err
		}
		v, err := rowJSON[model.BudgetBinding](r, "data", "authority", "owner", "selected", "path", "bound_at")
		if err != nil {
			return err
		}
		if _, ok := p.leases[id]; !ok {
			return fmt.Errorf("policy binding has no lease")
		}
		p.bindings[id] = v
		return p.validBinding(v)
	})
}
func (p *proofReader) validBinding(v model.BudgetBinding) error {
	if v.Authority != p.result.Host || v.Owner != p.result.Owner || v.Bound.IsZero() || len(v.Path) == 0 || len(v.Path) > 16 || v.Selected != v.Path[len(v.Path)-1] {
		return fmt.Errorf("invalid policy binding authority/lineage")
	}
	for i, id := range v.Path {
		b, ok := p.budgets[id]
		if !ok {
			return fmt.Errorf("binding references missing policy")
		}
		if i == 0 && !b.Root || i > 0 && (b.Root || b.Parent != v.Path[i-1]) {
			return fmt.Errorf("binding ancestry differs from policy")
		}
	}
	return nil
}
func (p *proofReader) readPoolJournal() error {
	return p.walk("pool_changes", func(r proofRow) error {
		v, err := rowJSON[model.PoolChange](r, "data", "operation", "idempotency_key", "actor", "reason", "at", "after", "delta")
		if err != nil {
			return err
		}
		seq, err := r.integer("sequence")
		if err != nil {
			return err
		}
		id, err := r.text("pool")
		if err != nil {
			return err
		}
		if err = p.validPool(v.Pool); err != nil {
			return err
		}
		if id != v.Pool.ID || seq < 1 || v.Sequence != 0 || v.Actor != p.result.Owner || !evidenceID(v.Key) || v.Reason == "" || v.At.IsZero() || !oneOf(v.Operation, "create", "set", "adjust", "drain", "resume", "delete", "rename", "describe") {
			return fmt.Errorf("invalid pool journal identity/action")
		}
		if v.Previous == nil {
			if v.Operation != "create" || v.Pool.Revision != 1 || v.Delta != v.Pool.Capacity {
				return fmt.Errorf("invalid pool creation journal")
			}
		} else {
			if err = p.validPool(*v.Previous); err != nil {
				return err
			}
			if v.Previous.ID != id || v.Pool.Revision != v.Previous.Revision+1 || v.Delta != v.Pool.Capacity-v.Previous.Capacity {
				return fmt.Errorf("pool transition differs")
			}
		}
		v.Sequence = seq
		p.poolChanges[seq] = v
		return nil
	})
}
func (p *proofReader) readBudgetJournal() error {
	return p.walk("budget_changes", func(r proofRow) error {
		v, err := rowJSON[model.BudgetChange](r, "data", "operation", "actor", "reason", "at", "after")
		if err != nil {
			return err
		}
		seq, err := r.integer("sequence")
		if err != nil {
			return err
		}
		id, err := r.text("budget")
		if err != nil {
			return err
		}
		if err = p.validBudget(v.Budget); err != nil {
			return err
		}
		if id != v.Budget.ID || seq < 1 || v.Sequence != 0 || v.Actor != p.result.Owner || v.Reason == "" || v.At.IsZero() || !oneOf(v.Operation, "initialize", "create", "set", "drain", "resume", "delete", "rename", "describe") {
			return fmt.Errorf("invalid policy journal")
		}
		if v.Previous == nil {
			if !oneOf(v.Operation, "create", "initialize") || v.Budget.Revision != 1 {
				return fmt.Errorf("invalid policy creation journal")
			}
		} else {
			if err = p.validBudget(*v.Previous); err != nil {
				return err
			}
			if v.Previous.ID != id || v.Budget.Revision != v.Previous.Revision+1 {
				return fmt.Errorf("policy transition differs")
			}
		}
		v.Sequence = seq
		p.budgetChanges[seq] = v
		return nil
	})
}

func (p *proofReader) policyJournalRelationships() error {
	poolLatest := map[string]model.Pool{}
	budgetLatest := map[string]model.Budget{}
	poolSeq := make([]int64, 0, len(p.poolChanges))
	for seq := range p.poolChanges {
		poolSeq = append(poolSeq, seq)
	}
	sort.Slice(poolSeq, func(i, j int) bool { return poolSeq[i] < poolSeq[j] })
	for _, seq := range poolSeq {
		v := p.poolChanges[seq]
		old, exists := poolLatest[v.Pool.ID]
		if exists && (v.Previous == nil || !equalEvidence(old, *v.Previous)) || !exists && v.Previous != nil {
			return fmt.Errorf("pool journal has a missing or inconsistent transition")
		}
		if !p.poolOperationSequences[seq] {
			return fmt.Errorf("pool transition lacks durable operation result")
		}
		poolLatest[v.Pool.ID] = v.Pool
	}
	for id, v := range p.pools {
		latest, ok := poolLatest[id]
		if !ok || !equalEvidence(latest, v) {
			return fmt.Errorf("pool definition differs from complete journal")
		}
	}
	if len(poolLatest) != len(p.pools) {
		return fmt.Errorf("pool journal references missing definition")
	}
	budgetSeq := make([]int64, 0, len(p.budgetChanges))
	for seq := range p.budgetChanges {
		budgetSeq = append(budgetSeq, seq)
	}
	sort.Slice(budgetSeq, func(i, j int) bool { return budgetSeq[i] < budgetSeq[j] })
	for _, seq := range budgetSeq {
		v := p.budgetChanges[seq]
		old, exists := budgetLatest[v.Budget.ID]
		if exists && (v.Previous == nil || !equalEvidence(old, *v.Previous)) || !exists && v.Previous != nil {
			return fmt.Errorf("policy journal has a missing or inconsistent transition")
		}
		if !p.budgetOperationSequences[seq] && v.Operation != "initialize" {
			return fmt.Errorf("policy transition lacks durable operation result")
		}
		budgetLatest[v.Budget.ID] = v.Budget
	}
	for id, v := range p.budgets {
		latest, ok := budgetLatest[id]
		if !ok || !equalEvidence(latest, v) {
			return fmt.Errorf("policy definition differs from complete journal")
		}
	}
	if len(budgetLatest) != len(p.budgets) {
		return fmt.Errorf("policy journal references missing definition")
	}
	return nil
}
func matchPoolTransition(q model.PoolMutation, v model.PoolChange) error {
	if q.Revision != nil && (v.Previous == nil || *q.Revision != v.Previous.Revision) {
		return fmt.Errorf("pool request revision differs from transition")
	}
	if q.Capacity != nil && *q.Capacity != v.Pool.Capacity || q.Delta != nil && *q.Delta != v.Delta {
		return fmt.Errorf("pool request capacity differs from transition")
	}
	if q.Operation == "create" && (q.Capacity == nil || q.Name != v.Pool.Name || q.Scope != v.Pool.Scope || q.Host != v.Pool.Host || q.Unit != v.Pool.Unit) {
		return fmt.Errorf("pool creation request differs from result")
	}
	if q.Operation == "rename" && q.Name != v.Pool.Name || q.Operation == "describe" && q.Description != v.Pool.Description {
		return fmt.Errorf("pool metadata request differs from result")
	}
	return nil
}
