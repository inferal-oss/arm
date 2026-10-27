package manager

import (
	"encoding/json"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func setPolicy(t *testing.T, m *Manager, node model.Budget, limits model.BudgetLimits) model.Budget {
	t.Helper()
	change, err := m.MutateBudget(model.BudgetMutation{Operation: "set", ID: node.ID, Revision: count(node.Revision), Limits: &limits, Reason: "review policy ceilings", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return change.Budget
}
func TestBudgetRuntimeHierarchyQueueGrowthAndNoReservationReuse(t *testing.T) {
	m := newManager(t)
	parent := makePolicyBudget(t, m, "self", "parent")
	child := makePolicyBudget(t, m, parent.ID, "child")
	half := .5
	parent = setPolicy(t, m, parent, model.BudgetLimits{CPU: &half})
	r := request("budget-first")
	r.Resources.CPU = .5
	r.Budget = child.ID
	first, err := m.Acquire(r, nil)
	if err != nil || first.State != "active" || len(first.Budget.Path) != 3 || len(first.BudgetPolicy) != 3 {
		t.Fatal(first, err)
	}
	waiting := r
	waiting.Key = "budget-wait"
	waiting.Queue = true
	second, err := m.Acquire(waiting, nil)
	if err != nil || second.State != "queued" {
		t.Fatal(second, err)
	}
	found := false
	for _, b := range second.QueueInfo.Blockers {
		if b.Code == "budget_limit" && strings.HasPrefix(b.Resource, parent.ID+"/") {
			found = true
			if b.BlockingCount != 1 || !reflect.DeepEqual(b.Leases, []string{first.ID}) {
				t.Fatal(b)
			}
		}
	}
	if !found || second.QueueInfo.Estimate.Kind != "unknown" {
		t.Fatal(second.QueueInfo)
	}
	view, err := m.BudgetInspect("self")
	if err != nil || view.Charged.Resources.CPU != .5 || view.Queued != 1 {
		t.Fatal(view, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != .5 {
		t.Fatal("policy manufactured physical reservation", status, err)
	}
	one := 1.
	setPolicy(t, m, parent, model.BudgetLimits{CPU: &one})
	m.tick()
	second, err = m.Inspect(second.ID)
	if err != nil || second.State != "active" || second.BudgetPolicy[1].Revision != parent.Revision+1 {
		t.Fatal(second, err)
	}
	// Exact retry returns original lineage, not a new admission after policy edit.
	repeat, err := m.Acquire(r, nil)
	if err != nil || repeat.ID != first.ID {
		t.Fatal(repeat, err)
	}
}
func TestBudgetRuntimeReductionRenewalDrainAndQueuedEntitlement(t *testing.T) {
	m := newManager(t)
	node := makePolicyBudget(t, m, "self", "drain")
	half := .5
	node = setPolicy(t, m, node, model.BudgetLimits{CPU: &half})
	r := request("renew-once")
	r.Resources.CPU = .5
	r.Budget = node.ID
	active, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Renew(active.ID, 2*time.Minute); err != nil {
		t.Fatal("renew double-counted its existing allocation", err)
	}
	zero := 0.
	node = setPolicy(t, m, node, model.BudgetLimits{CPU: &zero})
	view, err := m.BudgetInspect(node.ID)
	if err != nil || view.Deficit.Resources.CPU != .5 {
		t.Fatal(view, err)
	}
	if _, err = m.Renew(active.ID, 3*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal(err)
	}
	// CPU-only deficit must not block an unrelated disk-only claim.
	disk := request("disk-independent")
	disk.Budget = node.ID
	disk.Resources = model.Resources{Disk: 1}
	if _, err = m.Acquire(disk, nil); err != nil {
		t.Fatal(err)
	}
	queued := r
	queued.Queue = true
	queued.Key = "before-drain"
	wait, err := m.Acquire(queued, nil)
	if err != nil {
		t.Fatal(err)
	}
	drained, err := m.MutateBudget(model.BudgetMutation{Operation: "drain", ID: node.ID, Revision: count(node.Revision), Reason: "no new requests", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	queued.Key = "after-drain"
	if _, err = m.Acquire(queued, nil); errorCode(err) != "budget_unavailable" {
		t.Fatal("incoming willingness to queue bypassed drain", err)
	}
	_, err = m.End(active.ID, "released")
	if err != nil {
		t.Fatal(err)
	}
	setPolicy(t, m, drained.Budget, model.BudgetLimits{CPU: &half})
	m.tick()
	current, err := m.Inspect(wait.ID)
	if err != nil || current.State != "active" {
		t.Fatal("prior durable queue did not progress through drain", current, err)
	}
	if _, err = m.Renew(current.ID, 3*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal("draining lineage renewal", err)
	}
}
func TestBudgetRuntimeOwnerRootIncludesPeerAndCannotSelectRemotePolicy(t *testing.T) {
	m := newManager(t)
	root, err := m.budgetRecord(ownerBudgetID(m.host, m.owner))
	if err != nil {
		t.Fatal(err)
	}
	half := .5
	setPolicy(t, m, root, model.BudgetLimits{CPU: &half})
	r := request("remote")
	r.Resources.CPU = .5
	lease, err := m.AcquirePeer("peer-a", r, nil, model.Resources{CPU: 1}, time.Hour)
	if err != nil || lease.Owner != m.owner || lease.Budget.Selected != root.ID {
		t.Fatal(lease, err)
	}
	r.Key = "local-after-peer"
	if _, err = m.Acquire(r, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("peer skipped local owner ceiling", err)
	}
	r.Budget = "self"
	if _, err = m.AcquirePeer("peer-a", r, nil, model.Resources{CPU: 1}, time.Hour); errorCode(err) != "unsupported_remote_claim" {
		t.Fatal(err)
	}
}
func TestBudgetRuntimeAtomicLeaseBindingFaultAndConcurrentAdmission(t *testing.T) {
	m := newManager(t)
	node := makePolicyBudget(t, m, "self", "atomic")
	half := .5
	setPolicy(t, m, node, model.BudgetLimits{CPU: &half})
	if _, err := m.db.Exec(`CREATE TRIGGER fail_budget_bound BEFORE INSERT ON events WHEN NEW.type='budget.bound' BEGIN SELECT RAISE(ABORT,'binding journal fixture'); END`); err != nil {
		t.Fatal(err)
	}
	r := request("budget-rollback")
	r.Budget = node.ID
	r.Resources.CPU = .5
	if _, err := m.Acquire(r, nil); err == nil {
		t.Fatal("ignored binding journal failure")
	}
	var n int
	m.db.QueryRow(`SELECT COUNT(*) FROM leases WHERE request_key=?`, r.Key).Scan(&n)
	if n != 0 {
		t.Fatal("partial lease intent")
	}
	m.db.Exec(`DROP TRIGGER fail_budget_bound`)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { req := r; req.Key = local.ID(); _, err := m.Acquire(req, nil); results <- err })
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if errorCode(err) != "insufficient_capacity" {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
}
func TestBudgetRuntimeCustomExactCountsAndUnresolvedCharges(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "exact-policy", "global", 9007199254741000)
	node := makePolicyBudget(t, m, "self", "exact")
	setPolicy(t, m, node, model.BudgetLimits{Pools: map[string]int64{pool.ID: 9007199254740993}})
	r := poolRequest(pool, 9007199254740993)
	r.Budget = node.ID
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.State = "needs_attention"
	if err = m.put(l, "budget.fixture_uncertain"); err != nil {
		t.Fatal(err)
	}
	view, err := m.BudgetInspect(node.ID)
	if err != nil || view.Charged.Pools[pool.ID] != 9007199254740993 {
		t.Fatal(view, err)
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal(string(raw))
	}
	r.Key = local.ID()
	r.Pools[0].Quantity = 1
	if _, err = m.Acquire(r, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("uncertainty freed policy permits", err)
	}
	// Policy corruption blocks admission, but cannot disable ordinary provider-safe release.
	l.State = "active"
	if err = m.put(l, "budget.fixture_active_cleanup"); err != nil {
		t.Fatal(err)
	}
	m.db.Exec(`UPDATE lease_budget_bindings SET data='{}' WHERE lease=?`, l.ID)
	if _, err = m.Acquire(request("corrupt-new"), nil); err == nil {
		t.Fatal("corrupt obligations admitted work")
	}
	seen, err := m.Inspect(l.ID)
	if err != nil || seen.BudgetError == nil {
		t.Fatal(seen, err)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal("policy corruption prevented resource release", err)
	}
}
func TestBudgetRuntimeMigrationRestartAndLostBindingFailClosed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(request("legacy"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct an old-version record and remove only the migration marker.
	l.Request.Budget = ""
	l.Budget = nil
	l.BudgetPolicy = nil
	original, _ := json.Marshal(l)
	m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(original), l.ID)
	m.db.Exec(`DELETE FROM lease_budget_bindings WHERE lease=?`, l.ID)
	m.db.Exec(`DELETE FROM metadata WHERE key='budget_bindings_v1'`)
	m.Close()
	m, err = Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw)
	if raw != string(original) {
		t.Fatal("migration rewrote historical lease")
	}
	current, err := m.Inspect(l.ID)
	if err != nil || current.Budget == nil || len(current.Budget.Path) != 1 {
		t.Fatal(current, err)
	}
	retry, err := m.Acquire(request("legacy"), nil)
	if err != nil || retry.ID != l.ID {
		t.Fatal(retry, err)
	}
	m.db.Exec(`DELETE FROM lease_budget_bindings WHERE lease=?`, l.ID)
	m.Close()
	m, err = Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertBudgetDegraded(t, m)
	var bindings int
	m.db.QueryRow(`SELECT COUNT(*) FROM lease_budget_bindings WHERE lease=?`, l.ID).Scan(&bindings)
	if bindings != 0 {
		t.Fatal("lost lineage was rebound")
	}

}
func TestBudgetRuntimePagedReadsAndJournalNoScopeTruncation(t *testing.T) {
	m := newManager(t)
	node := makePolicyBudget(t, m, "self", "pages")
	for range 3 {
		r := request(local.ID())
		r.Budget = node.ID
		r.Resources.CPU = .1
		if _, err := m.Acquire(r, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := m.BudgetConsumers(model.BudgetQuery{ID: node.ID, Limit: 2})
	if err != nil || len(first.Users) != 2 || !first.More {
		t.Fatal(first, err)
	}
	second, err := m.BudgetConsumers(model.BudgetQuery{ID: node.ID, Limit: 2, After: first.Cursor})
	if err != nil || len(second.Users) != 1 || second.More {
		t.Fatal(second, err)
	}
	defs, err := m.Budgets(model.BudgetQuery{Limit: 1})
	if err != nil || len(defs.Budgets) != 1 || !defs.More {
		t.Fatal(defs, err)
	}
	history, err := m.BudgetHistory(model.BudgetQuery{ID: "self", Limit: 1})
	if err != nil || len(history.Entries) != 1 || history.Entries[0].Operation != "initialize" {
		t.Fatal(history, err)
	}
}

func TestBudgetRuntimeMissingProtectedRootNeverResetsPriorCeilings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	root, err := m.budgetRecord(ownerBudgetID(m.host, m.owner))
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.
	setPolicy(t, m, root, model.BudgetLimits{CPU: &zero})
	if _, err = m.db.Exec(`DELETE FROM budgets WHERE id=?`, root.ID); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(home, model.Resources{CPU: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	assertBudgetDegraded(t, m)
	var roots int
	m.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE id=?`, root.ID).Scan(&roots)
	if roots != 0 {
		t.Fatal("missing root was recreated")
	}

}

func TestBudgetRuntimeCorruptRequiredSchemaNeverBecomesUnlimited(t *testing.T) {
	for _, scenario := range []string{"missing-limits", "zero-created", "zero-updated", "unknown-field", "trailing-json", "duplicate-limits"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, err := Open(home, model.Resources{CPU: 2})
			if err != nil {
				t.Fatal(err)
			}
			node, err := m.budgetRecord(ownerBudgetID(m.host, m.owner))
			if err != nil {
				t.Fatal(err)
			}
			zero := 0.
			node = setPolicy(t, m, node, model.BudgetLimits{CPU: &zero})
			raw, _ := json.Marshal(node)
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			switch scenario {
			case "missing-limits":
				delete(fields, "limits")
			case "zero-created":
				fields["created_at"] = json.RawMessage(`"0001-01-01T00:00:00Z"`)
			case "zero-updated":
				fields["updated_at"] = json.RawMessage(`"0001-01-01T00:00:00Z"`)
			case "unknown-field":
				fields["unsupported_policy"] = json.RawMessage(`true`)
			}
			raw, _ = json.Marshal(fields)
			if scenario == "trailing-json" {
				raw = append(raw, []byte(` {}`)...)
			}
			if scenario == "duplicate-limits" {
				raw = append(raw[:len(raw)-1], []byte(`,"limits":{}}`)...)
			}
			if _, err = m.db.Exec(`UPDATE budgets SET data=? WHERE id=?`, string(raw), node.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = m.Acquire(request("corrupt-policy"), nil); errorCode(err) != "invalid_accounting" {
				t.Fatal("malformed policy admitted or misclassified", err)
			}
			m.Close()
			m, err = Open(home, model.Resources{CPU: 2})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			assertBudgetDegraded(t, m)
			var after string
			m.db.QueryRow(`SELECT data FROM budgets WHERE id=?`, node.ID).Scan(&after)
			if after != string(raw) {
				t.Fatal("corrupt root was rewritten")
			}

		})
	}
}
