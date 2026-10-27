package manager

import (
	"math"
	"testing"

	"getarm.org/internal/model"
)

func partitionNode(id, parent string, n int64) model.Pool {
	return model.Pool{ID: id, Parent: parent, Authority: "host", Owner: "501", Scope: "host", Host: "host", Unit: "token", Capacity: n, Revision: 1, State: "active"}
}
func TestSubpoolBackingAndDeferredReturn(t *testing.T) {
	defs := []model.Pool{partitionNode("root", "", 10), partitionNode("child", "root", 6), partitionNode("nested", "child", 2)}
	tree, err := buildPoolTree(defs)
	if err != nil {
		t.Fatal(err)
	}
	v, err := tree.accounting(map[string]int64{"child": 3, "nested": 2})
	if err != nil {
		t.Fatal(err)
	}
	if v["root"].Available != 4 || v["child"].Direct != 3 || v["child"].ChildBacking != 2 || v["child"].Available != 1 || v["root"].Direct != 0 {
		t.Fatal(v)
	}
	defs[1].Capacity = 2
	tree, _ = buildPoolTree(defs)
	v, err = tree.accounting(map[string]int64{"child": 3, "nested": 2})
	if err != nil {
		t.Fatal(err)
	}
	if v["child"].Held != 5 || v["child"].PendingReturn != 3 || v["root"].Available != 5 {
		t.Fatal(v)
	}
	v, err = tree.accounting(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v["child"].Held != 2 || v["root"].Available != 8 {
		t.Fatal(v)
	}
	// A parent's own direct claims remain disjoint from its child's idle carve.
	ok, err := tree.admit([]model.PoolClaim{{Pool: "root", Quantity: 9}}, nil)
	if err != nil || ok {
		t.Fatalf("borrowed child partition: %v %v", ok, err)
	}
	ok, err = tree.admit([]model.PoolClaim{{Pool: "root", Quantity: 8}, {Pool: "nested", Quantity: 2}}, nil)
	if err != nil || !ok {
		t.Fatalf("atomic disjoint bundle: %v %v", ok, err)
	}
	ok, err = tree.admit([]model.PoolClaim{{Pool: "root", Quantity: 9}, {Pool: "nested", Quantity: 2}}, nil)
	if err != nil || ok {
		t.Fatalf("root double allocation: %v %v", ok, err)
	}
	defs[0].Capacity = 1
	tree, _ = buildPoolTree(defs)
	ok, err = tree.admit([]model.PoolClaim{{Pool: "nested", Quantity: 1}}, nil)
	if err != nil || ok {
		t.Fatalf("ancestor deficit bypass: %v %v", ok, err)
	}
}
func TestSubpoolExactCountsOverflowAndZero(t *testing.T) {
	n := int64(9007199254740993)
	tree, err := buildPoolTree([]model.Pool{partitionNode("root", "", n), partitionNode("child", "root", n-1), partitionNode("zero", "child", 0)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := tree.accounting(map[string]int64{"child": n - 1})
	if err != nil || v["root"].ChildBacking != n-1 || v["root"].Available != 1 {
		t.Fatal(v, err)
	}
	if ok, err := tree.admit([]model.PoolClaim{{Pool: "zero", Quantity: 1}}, nil); err != nil || ok {
		t.Fatal(ok, err)
	}
	tree, _ = buildPoolTree([]model.Pool{partitionNode("root", "", math.MaxInt64), partitionNode("a", "root", math.MaxInt64), partitionNode("b", "root", 1)})
	if _, err = tree.accounting(nil); err == nil {
		t.Fatal("summed backing overflow")
	}
	tree, _ = buildPoolTree([]model.Pool{partitionNode("root", "", math.MaxInt64)})
	if fit, err := tree.admit([]model.PoolClaim{{Pool: "root", Quantity: 1}}, map[string]int64{"root": math.MaxInt64}); err != nil || fit {
		t.Fatal("full exact capacity should remain waitable without overflow", fit, err)
	}
	if _, err = tree.accounting(map[string]int64{"missing": 1}); err == nil {
		t.Fatal("unknown charged pool")
	}
}
func TestSubpoolTreeRejectsInvalidIdentityAndCycles(t *testing.T) {
	for _, kind := range []string{"cycle", "unit", "owner", "authority", "scope", "host", "state", "negative", "depth", "duplicate", "missing"} {
		t.Run(kind, func(t *testing.T) {
			defs := []model.Pool{partitionNode("root", "", 10), partitionNode("child", "root", 1)}
			switch kind {
			case "cycle":
				defs[0].Parent = "child"
			case "unit":
				defs[1].Unit = "other"
			case "owner":
				defs[1].Owner = "other"
			case "authority":
				defs[1].Authority = "other"
				defs[1].Host = "other"
			case "scope":
				defs[1].Scope = "global"
				defs[1].Host = ""
			case "host":
				defs[1].Host = "other"
			case "state":
				defs[1].State = "future"
			case "negative":
				defs[1].Capacity = -1
			case "depth":
				for i := 0; i < maxPoolDepth; i++ {
					parent := defs[len(defs)-1].ID
					defs = append(defs, partitionNode(parent+"x", parent, 0))
				}
			case "duplicate":
				defs = append(defs, defs[1])
			case "missing":
				defs[1].Parent = "missing"
			}
			if _, err := buildPoolTree(defs); err == nil {
				t.Fatal("accepted invalid hierarchy")
			}
		})
	}
}
