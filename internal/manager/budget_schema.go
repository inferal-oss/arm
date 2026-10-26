package manager

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

// Caller owns Open initialization or holds m.mu. Schema/root initialization
// precedes lineage migration and any scheduling/admission.
func (m *Manager) initBudgetRecords() error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS budgets (id TEXT PRIMARY KEY, owner TEXT NOT NULL, parent TEXT, data TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS budgets_root ON budgets(owner) WHERE parent IS NULL`,
		`CREATE INDEX IF NOT EXISTS budgets_parent ON budgets(parent)`,
		`CREATE TABLE IF NOT EXISTS budget_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT,budget TEXT NOT NULL,data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS budget_changes_budget ON budget_changes(budget,sequence)`,
		`CREATE TABLE IF NOT EXISTS budget_operations (key TEXT PRIMARY KEY,request TEXT NOT NULL,result TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS lease_budget_bindings (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	} {
		if _, err := m.db.Exec(query); err != nil {
			return err
		}
	}
	id := ownerBudgetID(m.host, m.owner)
	var raw []byte
	err := m.db.QueryRow(`SELECT data FROM budgets WHERE id=?`, id).Scan(&raw)
	if err == nil {
		_, err = m.budgetRecord(id)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var initialized int
	if err = m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM metadata WHERE key='budget_bindings_v1')+(SELECT COUNT(*) FROM budgets)+(SELECT COUNT(*) FROM budget_changes)+(SELECT COUNT(*) FROM budget_operations)+(SELECT COUNT(*) FROM lease_budget_bindings)`).Scan(&initialized); err != nil {
		return err
	}
	if initialized > 0 {
		return model.Err("invalid_accounting", "existing policy ledger lost its protected owner root; it cannot be recreated without its prior ceilings")
	}
	now := time.Now().UTC()
	root := model.Budget{ID: id, Authority: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Root: true, Name: "Owner budget", Limits: model.BudgetLimits{}, State: "active", Revision: 1, Created: now, Updated: now}
	raw, err = json.Marshal(root)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO budgets(id,owner,parent,data) VALUES(?,?,NULL,?)`, id, m.owner, string(raw)); err != nil {
		return err
	}
	change := model.BudgetChange{Operation: "initialize", Actor: m.owner, ActorUsername: m.ownerUsername, Reason: "Initialize the authenticated local owner policy root without extra ceilings", At: now, Budget: root}
	journal, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO budget_changes(budget,data) VALUES(?,?)`, id, string(journal)); err != nil {
		return err
	}
	if err := event(tx, "budget.root_created", id, root); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) budgetRecord(id string) (model.Budget, error) {
	var budget model.Budget
	var raw []byte
	var indexedOwner string
	var indexedParent sql.NullString
	err := m.db.QueryRow(`SELECT data,owner,parent FROM budgets WHERE id=?`, id).Scan(&raw, &indexedOwner, &indexedParent)
	if errors.Is(err, sql.ErrNoRows) {
		return budget, model.Err("budget_not_found", "policy budget is not defined at this authority")
	}
	if err != nil {
		return budget, err
	}
	if len(raw) > 64<<10 {
		return budget, model.Err("invalid_accounting", "policy budget record exceeds bound")
	}
	if err := decodeBudgetRecord(raw, &budget); err != nil {
		return budget, err
	}
	if budget.ID != id || budget.Authority != m.host || budget.Owner != m.owner || indexedOwner != budget.Owner || indexedParent.String != budget.Parent || indexedParent.Valid == budget.Root {
		return model.Budget{}, model.Err("invalid_accounting", "policy budget owner/authority/identity differs from its authenticated ledger")
	}
	if budget.Revision < 1 || !budgetText(budget.Name, 128, true) || !budgetText(budget.Description, 4096, false) {
		return budget, model.Err("invalid_accounting", "policy budget metadata or revision is invalid")
	}
	if err := validBudgetLimits(budget.Limits); err != nil {
		return budget, model.Err("invalid_accounting", err.Error())
	}
	switch budget.State {
	case "active", "draining", "deleted":
	default:
		return budget, model.Err("invalid_accounting", "policy budget lifecycle is unknown")
	}
	if budget.Root {
		if budget.Parent != "" || budget.ID != ownerBudgetID(m.host, m.owner) || budget.State == "deleted" {
			return budget, model.Err("invalid_accounting", "authenticated owner policy root is invalid")
		}
	} else if budget.Parent == "" {
		return budget, model.Err("invalid_accounting", "policy child has no immutable parent")
	}
	return budget, nil
}

func (m *Manager) resolveBudgetPath(id string) ([]model.Budget, error) {
	root := ownerBudgetID(m.host, m.owner)
	if id == "" || id == "self" {
		id = root
	}
	path := []model.Budget{}
	seen := map[string]bool{}
	for id != "" {
		if len(path) >= maxBudgetDepth || seen[id] {
			return nil, model.Err("invalid_accounting", "policy budget lineage cycles or exceeds maximum depth")
		}
		seen[id] = true
		node, err := m.budgetRecord(id)
		if err != nil {
			return nil, err
		}
		path = append(path, node)
		if node.Root {
			break
		}
		id = node.Parent
	}
	if len(path) == 0 || !path[len(path)-1].Root || path[len(path)-1].ID != root {
		return nil, model.Err("invalid_accounting", "policy lineage does not terminate at the authenticated owner root")
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

// These records are created only by this schema. Unknown/missing/duplicate
// required policy fields are corruption, not an implicit migration to no cap.
func decodeBudgetRecord(raw []byte, budget *model.Budget) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return model.Err("invalid_accounting", "policy record must be one object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return model.Err("invalid_accounting", "invalid policy record")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return model.Err("invalid_accounting", "duplicate or invalid policy record field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return model.Err("invalid_accounting", "invalid policy record field")
		}
	}
	if _, err = decoder.Token(); err != nil {
		return model.Err("invalid_accounting", "invalid policy object ending")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return model.Err("invalid_accounting", "trailing data in policy record")
	}
	for _, field := range []string{"id", "authority", "owner", "root", "name", "limits", "state", "revision", "created_at", "updated_at"} {
		if !seen[field] {
			return model.Err("invalid_accounting", "policy record lacks required "+field)
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(budget); err != nil {
		return model.Err("invalid_accounting", "invalid policy record schema: "+err.Error())
	}
	if budget.Created.IsZero() || budget.Updated.IsZero() {
		return model.Err("invalid_accounting", "policy creation/update timestamps must be recorded")
	}
	return nil
}
