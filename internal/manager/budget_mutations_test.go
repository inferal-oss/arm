package manager

import (
	"encoding/json"
	"sync"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func budgetFixture(t *testing.T) *Manager {
	t.Helper()
	m := newManager(t)
	if err := m.initBudgetRecords(); err != nil {
		t.Fatal(err)
	}
	return m
}
func makePolicyBudget(t *testing.T, m *Manager, parent, name string) model.Budget {
	t.Helper()
	change, err := m.MutateBudget(model.BudgetMutation{Operation: "create", Parent: parent, Name: name, Reason: "coordinate policy fixture", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return change.Budget
}
func TestBudgetMutationsStableLineageKeyedRevisionsAndZeroDeficit(t *testing.T) {
	m := budgetFixture(t)
	parent := makePolicyBudget(t, m, "self", "build")
	child := makePolicyBudget(t, m, parent.ID, "tests")
	path, err := m.resolveBudgetPath(child.ID)
	if err != nil || len(path) != 3 || path[0].Owner != m.owner {
		t.Fatal(path, err)
	}
	zero := float64(0)
	request := model.BudgetMutation{Operation: "set", ID: parent.ID, Revision: count(parent.Revision), Limits: &model.BudgetLimits{CPU: &zero}, Reason: "pause CPU in this branch", Key: "budget-zero"}
	change, err := m.MutateBudget(request)
	if err != nil || change.Budget.Limits.CPU == nil || *change.Budget.Limits.CPU != 0 {
		t.Fatal(change, err)
	}
	_, err = m.MutateBudget(model.BudgetMutation{Operation: "rename", ID: child.ID, Parent: ownerBudgetID(m.host, m.owner), Name: "moved", Revision: count(child.Revision), Reason: "unsupported reparent", Key: local.ID()})
	if errorCode(err) != "invalid_request" {
		t.Fatal("reparent accepted", err)
	}
	next, err := m.MutateBudget(model.BudgetMutation{Operation: "rename", ID: parent.ID, Name: "renamed build", Revision: count(change.Budget.Revision), Reason: "rename only", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := m.MutateBudget(request)
	if err != nil || retry.Sequence != change.Sequence {
		t.Fatal("exact retry revalidated newer revision", retry, err)
	}
	path, err = m.resolveBudgetPath(child.ID)
	if err != nil || path[1].ID != parent.ID || path[1].Name != next.Budget.Name {
		t.Fatal("metadata rename moved binding", path, err)
	}
	lookup, err := m.LookupBudgetMutation(request.Key)
	if err != nil || !lookup.Found || lookup.Change.Sequence != change.Sequence {
		t.Fatal(lookup, err)
	}
	request.Reason = "different rationale"
	if _, err = m.MutateBudget(request); errorCode(err) != "idempotency_conflict" {
		t.Fatal("conflicting key accepted", err)
	}
}
func TestBudgetMutationFaultAndConcurrentReviewCannotLoseUpdate(t *testing.T) {
	m := budgetFixture(t)
	budget := makePolicyBudget(t, m, "self", "faults")
	request := model.BudgetMutation{Operation: "drain", ID: budget.ID, Revision: count(budget.Revision), Reason: "drain atomically", Key: "budget-fault"}
	if _, err := m.db.Exec(`CREATE TRIGGER fail_budget_event BEFORE INSERT ON events WHEN NEW.type='budget.drain' BEGIN SELECT RAISE(ABORT,'fixture budget journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MutateBudget(request); err == nil {
		t.Fatal("ignored budget transaction fault")
	}
	stored, _ := m.budgetRecord(budget.ID)
	lookup, _ := m.LookupBudgetMutation(request.Key)
	if stored.Revision != budget.Revision || stored.State != "active" || lookup.Found {
		t.Fatal("partial budget change", stored, lookup)
	}
	m.db.Exec(`DROP TRIGGER fail_budget_event`)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Go(func() { r := request; r.Key = local.ID(); _, err := m.MutateBudget(r); results <- err })
	}
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errorCode(err) == "revision_conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("lost update", success, conflict)
	}
}
func TestBudgetBindingsMigrateWithoutChangingLeaseHistoryAndFailClosed(t *testing.T) {
	m := budgetFixture(t)
	active, err := m.Acquire(request("budget-migrate"), nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(active)
	if err := m.migrateBudgetBindings(); err != nil {
		t.Fatal(err)
	}
	binding, found, err := m.budgetBinding(active)
	if err != nil || !found || len(binding.Path) != 1 {
		t.Fatal(binding, found, err)
	}
	current, _ := m.get(active.ID)
	after, _ := json.Marshal(current)
	if string(before) != string(after) {
		t.Fatal("binding migration rewrote historical lease")
	}
	binding.Path = append(binding.Path, binding.Selected)
	raw, _ := json.Marshal(binding)
	m.db.Exec(`UPDATE lease_budget_bindings SET data=? WHERE lease=?`, string(raw), active.ID)
	if _, _, err = m.budgetBinding(active); errorCode(err) != "invalid_accounting" {
		t.Fatal("duplicate lineage gave apparent capacity", err)
	}
}
func TestBudgetChargesOverlapOnlyPolicyNotActualAllocation(t *testing.T) {
	m := budgetFixture(t)
	a, b := makePolicyBudget(t, m, "self", "a"), makePolicyBudget(t, m, "self", "b")
	pool := makePool(t, m, "policy permits", "global", 2)
	leases := []model.Lease{}
	for _, node := range []model.Budget{a, b} {
		r := poolRequest(pool, 1)
		r.Budget = node.ID
		lease, err := m.Acquire(r, nil)
		if err != nil {
			t.Fatal(err)
		}

		leases = append(leases, lease)
	}
	charges, _, err := m.budgetCharges(leases)
	root := ownerBudgetID(m.host, m.owner)
	if err != nil || charges[root].Pools[pool.ID] != 2 || charges[a.ID].Pools[pool.ID] != 1 || charges[b.ID].Pools[pool.ID] != 1 {
		t.Fatal("policy ancestry double counted root", charges, err)
	}
	actual, err := poolReservations(leases)
	if err != nil || actual[pool.ID] != 2 {
		t.Fatal("policy definitions made physical reservations", actual, err)
	}
	leases[0].State = "needs_attention"
	if _, err := m.MutateBudget(model.BudgetMutation{Operation: "delete", ID: a.ID, Revision: count(a.Revision), Reason: "cannot hide unresolved use", Key: local.ID()}); errorCode(err) != "budget_in_use" {
		t.Fatal("deletion hid bound allocation", err)
	}
	charges, _, err = m.budgetCharges(leases)
	if err != nil || charges[root].Pools[pool.ID] != 2 {
		t.Fatal("uncertainty freed root", charges, err)
	}
	leases[0].State = "alien-state"
	if _, _, err = m.budgetCharges(leases); errorCode(err) != "invalid_accounting" {
		t.Fatal("unknown lifecycle became free", err)
	}
}
