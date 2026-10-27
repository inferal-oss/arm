package manager

import (
	"encoding/json"
	"math"
	"testing"

	"getarm.org/internal/model"
)

func TestBudgetPolicyRootAndLimitsKeepIdentityAndZeroSemantics(t *testing.T) {
	a := ownerBudgetID("authority-a", "123")
	if a != ownerBudgetID("authority-a", "123") || a == ownerBudgetID("authority-b", "123") || a == ownerBudgetID("authority-a", "124") {
		t.Fatal("owner policy identity not stable/authority scoped")
	}
	zero := int64(0)
	zeroCPU := float64(0)
	limits := model.BudgetLimits{CPU: &zeroCPU, Memory: &zero, Pools: map[string]int64{"pool-one": 0}}
	if err := validBudgetLimits(limits); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(limits)
	var decoded model.BudgetLimits
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.CPU == nil || decoded.Memory == nil || decoded.Disk != nil {
		t.Fatal("zero became unset/automatic", string(encoded), err)
	}
	copied := copyBudgetLimits(limits)
	*copied.Memory = 1
	copied.Pools["pool-one"] = 2
	if *limits.Memory != 0 || limits.Pools["pool-one"] != 0 {
		t.Fatal("policy copy aliases caller data")
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		if err := validBudgetLimits(model.BudgetLimits{CPU: &invalid}); err == nil {
			t.Fatal("invalid CPU ceiling", invalid)
		}
	}
}
func TestBudgetPolicyCheckedChargesAndOverlappingHeadroom(t *testing.T) {
	maxBytes := int64(math.MaxInt64)
	a := model.BudgetAmounts{Resources: model.Resources{CPU: 1, Memory: maxBytes}, Pools: map[string]int64{"p": maxBytes}}
	if _, err := addBudgetAmounts(a, model.BudgetAmounts{Resources: model.Resources{Memory: 1}}); errorCode(err) != "invalid_accounting" {
		t.Fatal("byte overflow accepted", err)
	}
	if _, err := addBudgetAmounts(a, model.BudgetAmounts{Pools: map[string]int64{"p": 1}}); errorCode(err) != "invalid_accounting" {
		t.Fatal("pool overflow accepted", err)
	}
	cpu, mem := float64(2), int64(5)
	limits := model.BudgetLimits{CPU: &cpu, Memory: &mem, Pools: map[string]int64{"p": 3}}
	available, deficit := remainingBudgetLimits(limits, model.BudgetAmounts{Resources: model.Resources{CPU: 3, Memory: 4}, Pools: map[string]int64{"p": 4}})
	if *available.CPU != 0 || *available.Memory != 1 || available.Disk != nil || available.Pools["p"] != 0 || deficit.Resources.CPU != 1 || deficit.Pools["p"] != 1 {
		t.Fatalf("wrong conservative headroom %+v %+v", available, deficit)
	}
	parent := float64(.5)
	effective := minBudgetLimits(available, model.BudgetLimits{CPU: &parent, Pools: map[string]int64{"q": 0}})
	if *effective.CPU != 0 || effective.Pools["p"] != 0 || effective.Pools["q"] != 0 || *effective.Memory != 1 {
		t.Fatal("ancestor minimum added independent capacity", effective)
	}
}
func TestBudgetSchemaInitializationAndLineageAreIndependentOfAttribution(t *testing.T) {
	m := newManager(t)
	if err := m.initBudgetRecords(); err != nil {
		t.Fatal(err)
	}
	path, err := m.resolveBudgetPath("self")
	if err != nil || len(path) != 1 || !path[0].Root || path[0].Owner != m.owner || path[0].Limits.CPU != nil || path[0].Limits.Memory != nil {
		t.Fatalf("default policy invents resource capacity %+v %v", path, err)
	}
	first := path[0]
	m.ownerUsername = "renamed-account"
	if err := m.initBudgetRecords(); err != nil {
		t.Fatal(err)
	}
	path, err = m.resolveBudgetPath("")
	if err != nil || path[0].ID != first.ID || path[0].OwnerUsername != first.OwnerUsername {
		t.Fatal("display rename changed authenticated root/history", path, err)
	}
	var roots int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM budgets WHERE parent IS NULL`).Scan(&roots); err != nil || roots != 1 {
		t.Fatal("root duplicated", roots, err)
	}
}

func TestBudgetPolicyAdmissionChecksAllAncestorsWithoutHoldingCapacity(t *testing.T) {
	m := budgetFixture(t)
	parent := makePolicyBudget(t, m, "self", "ancestor")
	child := makePolicyBudget(t, m, parent.ID, "leaf")
	limit := float64(2)
	_, err := m.MutateBudget(model.BudgetMutation{Operation: "set", ID: parent.ID, Revision: count(parent.Revision), Limits: &model.BudgetLimits{CPU: &limit}, Reason: "share two CPU promises", Key: "budget-ancestor-cap"})
	if err != nil {
		t.Fatal(err)
	}
	lease := model.Lease{ID: "not-admitted", Host: m.host, Owner: m.owner}
	binding, err := m.resolveNewBudgetBinding(lease, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := model.BudgetAmounts{Resources: model.Resources{CPU: 1}}
	used := map[string]model.BudgetAmounts{parent.ID: {Resources: model.Resources{CPU: 2}}}
	blockers, snapshot, err := m.budgetAdmissionBlockers(binding, want, used, false)
	if err != nil || len(blockers) != 1 || blockers[0].Code != "budget_limit" || len(snapshot) != 3 {
		t.Fatalf("ancestor bypass %+v %+v %v", blockers, snapshot, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != 0 {
		t.Fatal("policy checks reserved physical capacity", status.Reserved, err)
	}
	// Unrelated dimensions need not worsen an existing CPU-only policy deficit.
	blockers, _, err = m.budgetAdmissionBlockers(binding, model.BudgetAmounts{Resources: model.Resources{Disk: 1}}, map[string]model.BudgetAmounts{parent.ID: {Resources: model.Resources{CPU: 3}}}, false)
	if err != nil || len(blockers) != 0 {
		t.Fatal("unrelated policy dimension blocked", blockers, err)
	}
}
