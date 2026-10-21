package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// MutateBudget is native same-user coordination. No caller chooses the owner,
// and this helper does not expose an administrative endpoint to mesh/browser.
func (m *Manager) MutateBudget(request model.BudgetMutation) (model.BudgetChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateBudget(request)
}
func (m *Manager) mutateBudget(request model.BudgetMutation) (model.BudgetChange, error) {
	var result model.BudgetChange
	if err := m.budgetPolicyError(); err != nil {
		return result, err
	}
	if m.closing || m.draining {
		return result, model.Err("backend_stopping", "policy mutations are paused while backend shutdown drains work")
	}
	if strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 4096 || request.Key == "" || len(request.Key) > 128 || len(request.Agent) > 128 || len(request.Project) > 256 {
		return result, model.Err("invalid_request", "provide a bounded policy reason, key and attribution")
	}
	if request.Limits != nil {
		limits := copyBudgetLimits(*request.Limits)
		if limits.CPU != nil && *limits.CPU == 0 {
			value := float64(0)
			limits.CPU = &value
		}
		request.Limits = &limits
		if err := validBudgetLimits(limits); err != nil {
			return result, err
		}
	}
	switch request.Operation {
	case "create":
		if request.ID != "" || request.Revision != nil || !budgetText(request.Name, 128, true) || !budgetText(request.Description, 4096, false) {
			return result, model.Err("invalid_request", "create requires bounded name/description and no existing ID or revision")
		}
		request.Name = strings.TrimSpace(request.Name)
		if request.Parent == "" || request.Parent == "self" {
			request.Parent = ownerBudgetID(m.host, m.owner)
		}
	case "set", "drain", "resume", "delete", "rename", "describe":
		if request.ID == "self" {
			request.ID = ownerBudgetID(m.host, m.owner)
		}
		if request.ID == "" || request.Parent != "" || request.Revision == nil || *request.Revision < 1 {
			return result, model.Err("invalid_request", "policy changes require an ID and expected positive revision; parentage is immutable")
		}
		if request.Operation == "set" {
			if request.Limits == nil {
				return result, model.Err("invalid_request", "set requires complete explicit limits; use an empty limits object to clear additional ceilings")
			}
		} else if request.Limits != nil {
			return result, model.Err("invalid_request", "limits apply only to create or set")
		}
		if request.Operation == "rename" {
			if !budgetText(request.Name, 128, true) {
				return result, model.Err("invalid_request", "rename requires a bounded printable name")
			}
			request.Name = strings.TrimSpace(request.Name)
		} else if request.Name != "" {
			return result, model.Err("invalid_request", "name applies only to create or rename")
		}
		if request.Operation == "describe" {
			if !budgetText(request.Description, 4096, false) {
				return result, model.Err("invalid_request", "description must be bounded printable UTF-8")
			}
		} else if request.Description != "" {
			return result, model.Err("invalid_request", "description applies only to create or describe")
		}
	default:
		return result, model.Err("invalid_request", "unsupported policy budget operation")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	if len(raw) > 60<<10 {
		return result, model.Err("invalid_request", "policy request exceeds bounded local transport envelope")
	}
	var saved, reply []byte
	err = m.db.QueryRow(`SELECT request,result FROM budget_operations WHERE key=?`, request.Key).Scan(&saved, &reply)
	if err == nil {
		if string(saved) != string(raw) {
			return result, model.Err("idempotency_conflict", "policy operation key was used for different parameters")
		}
		err = json.Unmarshal(reply, &result)
		if err == nil && (result.Actor != m.owner || result.Budget.Authority != m.host) {
			return model.BudgetChange{}, model.Err("budget_not_found", "policy operation belongs to another authenticated ledger")
		}
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if request.Limits != nil {
		for id := range request.Limits.Pools {
			pool, err := m.pool(id)
			if err != nil {
				return result, err
			}
			if pool.Authority != m.host || (pool.Scope == "host" && pool.Host != m.host) {
				return result, model.Err("authority_unavailable", "policy pool ceiling belongs to another authority")
			}
		}
	}
	session, registration := "", ""
	if request.Agent != "" {
		contact, err := m.contact(request.Agent)
		if errors.Is(err, sql.ErrNoRows) {
			return result, model.Err("invalid_contact", "register the agent before attributing a policy change")
		}
		if err != nil {
			return result, err
		}
		session, registration = contact.Session, contact.Registration
	}
	now := time.Now().UTC()
	var budget model.Budget
	if request.Operation == "create" {
		path, err := m.resolveBudgetPath(request.Parent)
		if err != nil {
			return result, err
		}
		if len(path) >= maxBudgetDepth {
			return result, model.Err("invalid_request", "policy hierarchy exceeds maximum depth16")
		}
		for _, node := range path {
			if node.State != "active" {
				return result, model.Err("budget_unavailable", "policy ancestry must be active to create a child")
			}
		}
		var count int
		if err := m.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE owner=? AND json_extract(data,'$.state')<>'deleted'`, m.owner).Scan(&count); err != nil {
			return result, err
		}
		if count >= maxBudgetDefinitions {
			return result, model.Err("budget_limit", "too many live policy definitions at this authority")
		}
		limits := model.BudgetLimits{}
		if request.Limits != nil {
			limits = copyBudgetLimits(*request.Limits)
		}
		budget = model.Budget{ID: "budget_" + local.ID(), Authority: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Parent: request.Parent, Name: request.Name, Description: request.Description, Limits: limits, State: "active", Revision: 1, Created: now, Updated: now}
	} else {
		budget, err = m.budgetRecord(request.ID)
		if err != nil {
			return result, err
		}
		if budget.State == "deleted" {
			return result, model.Err("budget_deleted", "policy definition is a retained tombstone")
		}
		if budget.Revision != *request.Revision {
			return result, model.Err("revision_conflict", "policy budget changed; inspect its current revision")
		}
		if budget.Revision == math.MaxInt64 {
			return result, model.Err("revision_exhausted", "policy revision cannot be incremented")
		}
		before := budget
		before.Limits = copyBudgetLimits(before.Limits)
		result.Previous = &before
		switch request.Operation {
		case "set":
			budget.Limits = copyBudgetLimits(*request.Limits)
		case "drain":
			budget.State = "draining"
		case "resume":
			budget.State = "active"
		case "rename":
			budget.Name = request.Name
		case "describe":
			budget.Description = request.Description
		case "delete":
			if budget.Root {
				return result, model.Err("invalid_request", "authenticated owner policy root cannot be deleted")
			}
			if err := m.budgetCanDelete(budget.ID); err != nil {
				return result, err
			}
			budget.State = "deleted"
		}
		budget.Revision++
		budget.Updated = now
	}
	var collision int
	err = m.db.QueryRow(`SELECT 1 FROM budgets WHERE owner=? AND parent=? AND id<>? AND json_extract(data,'$.name')=? AND json_extract(data,'$.state')<>'deleted' LIMIT 1`, m.owner, budget.Parent, budget.ID, budget.Name).Scan(&collision)
	if err == nil {
		return result, model.Err("budget_exists", "a live policy sibling already has this name")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	result.Operation, result.Key, result.Actor, result.ActorUsername = request.Operation, request.Key, m.owner, m.ownerUsername
	result.Agent, result.Session, result.Registration, result.Project = request.Agent, session, registration, request.Project
	result.Reason, result.At, result.Budget = request.Reason, now, budget
	tx, err := m.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	data, err := json.Marshal(budget)
	if err != nil {
		return result, err
	}
	var parent any
	if budget.Parent != "" {
		parent = budget.Parent
	}
	if _, err = tx.Exec(`INSERT INTO budgets(id,owner,parent,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, budget.ID, budget.Owner, parent, string(data)); err != nil {
		return result, err
	}
	data, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	inserted, err := tx.Exec(`INSERT INTO budget_changes(budget,data) VALUES(?,?)`, budget.ID, string(data))
	if err != nil {
		return result, err
	}
	result.Sequence, err = inserted.LastInsertId()
	if err != nil {
		return result, err
	}
	data, err = json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.Exec(`INSERT INTO budget_operations(key,request,result) VALUES(?,?,?)`, request.Key, string(raw), string(data)); err != nil {
		return result, err
	}
	if err = event(tx, "budget."+request.Operation, budget.ID, result); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
func (m *Manager) budgetCanDelete(id string) error {
	var found int
	err := m.db.QueryRow(`SELECT 1 FROM budgets WHERE parent=? AND json_extract(data,'$.state')<>'deleted' LIMIT 1`, id).Scan(&found)
	if err == nil {
		return model.Err("budget_in_use", "finish or delete child policy definitions first")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	leases, err := m.list()
	if err != nil {
		return err
	}
	for _, lease := range leases {
		_, charged, err := budgetLeaseAmounts(lease)
		if err != nil {
			return err
		}
		if !charged && lease.State != "queued" {
			continue
		}
		binding, ok, err := m.budgetBinding(lease)
		if err != nil {
			return err
		}
		if !ok {
			return model.Err("invalid_accounting", "outstanding lease has no policy binding")
		}
		for _, ancestor := range binding.Path {
			if ancestor == id {
				return model.Err("budget_in_use", "queued or outstanding descendant obligations prevent policy deletion")
			}
		}
	}
	return nil
}
func (m *Manager) LookupBudgetMutation(key string) (model.BudgetMutationLookup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result model.BudgetMutationLookup
	if key == "" || len(key) > 128 {
		return result, model.Err("invalid_request", "provide original policy operation key of at most128 bytes")
	}
	var raw, reply []byte
	err := m.db.QueryRow(`SELECT request,result FROM budget_operations WHERE key=?`, key).Scan(&raw, &reply)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var request model.BudgetMutation
	var change model.BudgetChange
	if err = json.Unmarshal(raw, &request); err != nil {
		return result, err
	}
	if err = json.Unmarshal(reply, &change); err != nil {
		return result, err
	}
	if change.Actor != m.owner || change.Budget.Authority != m.host {
		return result, nil
	}
	return model.BudgetMutationLookup{Found: true, Request: &request, Change: &change}, nil
}
