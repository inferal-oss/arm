package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func subpoolProofFixture(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(home, 0700)
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
	if err != nil {
		t.Fatal(err)
	}
	rootCap, childCap := int64(100), int64(25)
	root, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: "root", Scope: "global", Unit: "permits", Capacity: &rootCap, Reason: "proof test", Key: "root"})
	if err != nil {
		t.Fatal(err)
	}
	rev := root.Pool.Revision
	parentView, err := m.PoolInspect(root.Pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.MutatePool(model.PoolMutation{Reviewed: parentView.Review, Operation: "create", Name: "child", Parent: root.Pool.ID, ParentRevision: &rev, Capacity: &childCap, Reason: "proof test", Key: "child"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Acquire(model.Request{Pools: []model.PoolClaim{{Pool: child.Pool.ID, Quantity: 2}}, TTL: time.Minute, Reason: "child lease", Key: "child-lease"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	smaller := int64(1)
	childRev := child.Pool.Revision
	childView, err := m.PoolInspect(child.Pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.MutatePool(model.PoolMutation{Reviewed: childView.Review, Operation: "set", ID: child.Pool.ID, Revision: &childRev, Capacity: &smaller, Reason: "retain obligation", Key: "reduce-child"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(lease.ID, "released"); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	return home
}
func TestSettledProofSubpoolHistoryAndRetainedPartition(t *testing.T) {
	home := subpoolProofFixture(t)
	p, err := prove(t, home)
	if err != nil || !p.Complete || !p.Settled || p.Tables["pool_lineage"] != 2 || p.Tables["pool_claim_bindings"] != 1 {
		t.Fatalf("subpool proof %+v %v", p, err)
	}
}
func TestSettledProofRefusesCorruptSubpoolEvidence(t *testing.T) {
	cases := map[string]string{
		"review-null":          `UPDATE pool_operations SET request=json_set(request,'$.reviewed.hierarchy',null) WHERE json_extract(request,'$.parent') IS NOT NULL`,
		"review-alias":         `UPDATE pool_operations SET request=json_set(request,'$.reviewed.hierarchy.Root','hidden') WHERE json_extract(request,'$.parent') IS NOT NULL`,
		"review-missing-count": `UPDATE pool_operations SET request=json_remove(request,'$.reviewed.hierarchy.consumer_count') WHERE json_extract(request,'$.parent') IS NOT NULL`,
		"review-selection":     `UPDATE pool_operations SET request=json_set(request,'$.reviewed.hierarchy.pool','wrong') WHERE json_extract(request,'$.parent') IS NOT NULL`,
		"missing-marker":       `DELETE FROM metadata WHERE key='subpool_schema'`,
		"unknown-marker":       `UPDATE metadata SET value='2' WHERE key='subpool_schema'`,
		"lost-lineage":         `DELETE FROM pool_lineage WHERE json_extract(data,'$.parent') IS NOT NULL`,
		"foreign-lineage":      `UPDATE pool_lineage SET data=json_set(data,'$.authority','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
		"case-alias":           `UPDATE pool_lineage SET data=json_set(data,'$.Path',json('["hidden"]'))`,
		"wrong-path":           `UPDATE pool_lineage SET data=json_set(data,'$.path',json('["wrong"]'))`,
		"lost-binding":         `DELETE FROM pool_claim_bindings`,
		"orphan-binding":       `UPDATE pool_claim_bindings SET lease='missing'`,
		"wrong-quantity":       `UPDATE pool_claim_bindings SET data=json_set(data,'$.claims[0].quantity',0)`,
		"null-quantity":        `UPDATE pool_claim_bindings SET data=json_set(data,'$.claims[0].quantity',null)`,
		"binding-path":         `UPDATE pool_claim_bindings SET data=json_set(data,'$.claims[0].path',json('["wrong"]'))`,
		"binding-alias":        `UPDATE pool_claim_bindings SET data=json_set(data,'$.Claims',json('[]'))`,
		"lost-partitions":      `UPDATE pool_changes SET data=json_remove(data,'$.partitions') WHERE json_extract(data,'$.after.parent') IS NOT NULL`,
		"overflow-partition":   `UPDATE pool_changes SET data=json_set(data,'$.partitions[0].after.direct_reserved',9223372036854775807,'$.partitions[0].after.child_backing',1)`,
		"changed-parent":       `UPDATE pools SET data=json_set(data,'$.parent',id) WHERE json_extract(data,'$.parent') IS NOT NULL`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			home := subpoolProofFixture(t)
			edit(t, home, query)
			p, err := prove(t, home)
			if err == nil && p.Complete && p.Settled {
				raw, _ := json.Marshal(p)
				t.Fatalf("corrupt evidence accepted %s", raw)
			}
		})
	}
}

// Keep this test explicitly independent from manager's degraded-startup path:
// the destructive reader must reject corruption without invoking recovery.
func TestSettledProofDoesNotRepairSubpoolEvidence(t *testing.T) {
	home := subpoolProofFixture(t)
	edit(t, home, `DELETE FROM pool_claim_bindings`)
	before, err := os.ReadFile(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = prove(t, home)
	after, err := os.ReadFile(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("read-only proof changed source")
	}
	_ = context.Background()
}

func TestSettledProofStoredPoolConsumerReviewLeaf(t *testing.T) {
	for name, mutation := range map[string]string{
		"unknown": `json_set(request,'$.reviewed.consumers[0].future',true)`,
		"alias":   `json_set(request,'$.reviewed.consumers[0].Revision',1)`,
		"null":    `json_set(request,'$.reviewed.consumers[0].revision',null)`,
		"missing": `json_remove(request,'$.reviewed.consumers[0].id')`,
	} {
		t.Run(name, func(t *testing.T) {
			home := subpoolProofFixture(t)
			db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			var raw string
			e = db.QueryRow(`SELECT request FROM pool_operations WHERE key='reduce-child'`).Scan(&raw)
			db.Close()
			if e != nil {
				t.Fatal(e)
			}
			var q model.PoolMutation
			if e = json.Unmarshal([]byte(raw), &q); e != nil || q.Reviewed == nil || len(q.Reviewed.Consumers) != 1 || q.Reviewed.Consumers[0].ID == "" || q.Reviewed.Consumers[0].Revision < 1 {
				t.Fatal("fixture must have actual recorded nonempty consumer evidence", raw, e)
			}
			edit(t, home, `UPDATE pool_operations SET request=`+mutation+` WHERE key='reduce-child'`)
			proof, e := prove(t, home)
			if e == nil && proof.Complete && proof.Settled {
				t.Fatal("stored malformed consumer leaf was accepted", name)
			}
		})
	}
}
