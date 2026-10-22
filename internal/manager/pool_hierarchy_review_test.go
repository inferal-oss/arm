package manager

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func TestHierarchyReviewBindsCompleteTreeClaimsAndExactRetry(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "root reviewed", "global", 10)
	v, e := m.PoolInspect(root.ID)
	if e != nil || v.Review.Hierarchy == nil {
		t.Fatal(v, e)
	}
	q := model.PoolMutation{Operation: "create", Parent: root.ID, ParentRevision: count(root.Revision), Name: "child reviewed", Capacity: count(4), Reason: "carve child for agents", Key: local.ID(), Reviewed: v.Review}
	child, e := m.MutatePool(q)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := m.Acquire(poolRequest(child.Pool, 3), nil)
	if e != nil {
		t.Fatal(e)
	}
	view, e := m.PoolInspect(child.Pool.ID)
	if e != nil || view.Review.Hierarchy.Pools != 2 || view.Review.Hierarchy.Consumers != 1 {
		t.Fatal(view, e)
	}
	reduce := model.PoolMutation{Operation: "set", ID: child.Pool.ID, Revision: count(child.Pool.Revision), Capacity: count(1), Reason: "retain old promises", Key: local.ID(), Reviewed: view.Review}
	sibling, e := m.MutatePool(model.PoolMutation{Operation: "create", Parent: root.ID, ParentRevision: count(root.Revision), Name: "sibling", Capacity: count(2), Reason: "sibling partition", Key: local.ID()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.MutatePool(reduce); errorCode(e) != "review_conflict" {
		t.Fatal("sibling tree mutation did not stale review", e)
	}
	view, e = m.PoolInspect(child.Pool.ID)
	if e != nil {
		t.Fatal(e)
	}
	reduce.Reviewed = view.Review
	change, e := m.MutatePool(reduce)
	if e != nil {
		t.Fatal(e)
	}
	now, e := m.PoolInspect(child.Pool.ID)
	if e != nil || now.Partition.Direct != 3 || now.Partition.Held != 3 || now.Partition.PendingReturn != 2 || now.Partition.Deficit != 2 {
		t.Fatal(now, e)
	}
	if _, e = m.End(lease.ID, "released"); e != nil {
		t.Fatal(e)
	}
	retry, e := m.MutatePool(reduce)
	if e != nil || retry.Sequence != change.Sequence {
		t.Fatal("exact key revalidated old tree", retry, e)
	}
	stale := reduce
	stale.Key = local.ID()
	stale.Revision = count(change.Pool.Revision)
	if _, e = m.MutatePool(stale); errorCode(e) != "review_conflict" {
		t.Fatal("changed consumer revision did not stale snapshot", e)
	}
	foreign := reduce
	foreign.Key = local.ID()
	foreign.ID = sibling.Pool.ID
	foreign.Revision = count(sibling.Pool.Revision)
	if _, e = m.MutatePool(foreign); errorCode(e) != "hierarchy_review_required" {
		t.Fatal("selected-node token lent to sibling", e)
	}
	// Unrelated physical telemetry must not stale concurrency evidence.
	a, _ := m.PoolInspect(root.ID)
	m.observationMu.Lock()
	m.memoryAvailability.Detail = "unrelated telemetry"
	m.observationMu.Unlock()
	b, _ := m.PoolInspect(root.ID)
	x, _ := json.Marshal(a.Review.Hierarchy)
	y, _ := json.Marshal(b.Review.Hierarchy)
	if string(x) != string(y) {
		t.Fatal("telemetry churned pool review")
	}
}
func TestHierarchyReviewWireRejectsAliasesNullUnknownAndMissingFields(t *testing.T) {
	good := `{"operation":"set","id":"pool-a","revision":1,"reason":"review","idempotency_key":"k","reviewed":{"authority":"a","consumers":[],"hierarchy":{"schema_version":1,"pool":"pool-a","root":"pool-a","pool_count":1,"consumer_count":0,"snapshot_sha256":"` + strings.Repeat("a", 64) + `"}}}`
	if _, e := DecodePoolMutation([]byte(good)); e != nil {
		t.Fatal(e)
	}
	bad := []string{strings.Replace(good, `"authority":"a"`, `"authority":"a","unavailable":null`, 1), strings.Replace(good, `"hierarchy":{`, `"Hierarchy":{`, 1), strings.Replace(good, `"consumer_count":0`, `"consumer_count":null`, 1), strings.Replace(good, `"consumer_count":0,`, ``, 1), strings.Replace(good, `"pool_count":1`, `"pool_count":1,"future":true`, 1), strings.Replace(good, `"pool_count":1`, `"pool_count":1,"pool_count":1`, 1), `{"operation":"create","reason":"q","idempotency_key":"k","reviewed":null}`}
	for _, raw := range bad {
		if _, e := DecodePoolMutation([]byte(raw)); e == nil {
			t.Fatal("malformed concurrency evidence accepted", raw)
		}
	}
	raw := strings.Replace(good, `"hierarchy":{`+strings.Split(strings.Split(good, `"hierarchy":{`)[1], `}}}`)[0]+`}`, `"hierarchy":null`, 1)
	if _, e := DecodePoolMutation([]byte(raw)); e == nil {
		t.Fatal("explicit null hierarchy accepted")
	}
}

func TestHierarchyReviewChildRequiresCompleteParentConsumers(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "parent consumers", "global", 10)
	if _, e := m.Acquire(poolRequest(p, 1), nil); e != nil {
		t.Fatal(e)
	}
	v, e := m.PoolInspect(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	v.Review.Consumers = []model.LeaseRevision{}
	_, e = m.MutatePool(model.PoolMutation{Operation: "create", Parent: p.ID, ParentRevision: count(p.Revision), Name: "hidden consumer", Capacity: count(1), Reason: "review all parent obligations", Key: local.ID(), Reviewed: v.Review})
	if errorCode(e) != "review_conflict" {
		t.Fatal("parent obligations omitted", e)
	}
}

func TestHierarchyReviewOversizeIsReadOnlyOnlyForItsRoot(t *testing.T) {
	m := newManager(t)
	big := makePool(t, m, "large review tree", "global", 10)
	small := makePool(t, m, "independent small tree", "global", 10)
	// Seed structurally valid no-obligation definitions/lineages, not 1100 public
	// mutations: the regression exercises the read/admission boundary, not tools.
	tx, e := m.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for i := 0; i < 1100; i++ {
		p := big
		p.ID = fmt.Sprintf("pool_large_%04d", i)
		p.Name = fmt.Sprintf("large child %04d", i)
		p.Parent = big.ID
		p.Capacity = 0
		p.Description = strings.Repeat("x", 4096)
		raw, _ := json.Marshal(p)
		if _, e = tx.Exec(`INSERT INTO pools(id,scope,host,name,data) VALUES(?,?,?,?,?)`, p.ID, p.Scope, p.Host, p.Name, string(raw)); e != nil {
			t.Fatal(e)
		}
		lineage := model.PoolLineage{Pool: p.ID, Parent: big.ID, Authority: p.Authority, Owner: p.Owner, Scope: p.Scope, Host: p.Host, Unit: p.Unit, Path: []string{big.ID, p.ID}}
		raw, _ = json.Marshal(lineage)
		if _, e = tx.Exec(`INSERT INTO pool_lineage(pool,data) VALUES(?,?)`, p.ID, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	status, e := m.Status()
	if e != nil || len(status.Resources) == 0 {
		t.Fatal("unrelated host visibility lost", e)
	}
	bv, e := m.PoolInspect(big.ID)
	if e != nil || bv.Review.Hierarchy != nil || bv.Review.Unavailable != "hierarchy_review_too_large" {
		t.Fatal(bv.Review, e)
	}
	sv, e := m.PoolInspect(small.ID)
	if e != nil || sv.Review.Hierarchy == nil || sv.Review.Unavailable != "" {
		t.Fatal(sv.Review, e)
	}
	_, e = m.MutatePool(model.PoolMutation{Operation: "set", ID: small.ID, Revision: count(small.Revision), Capacity: count(9), Reason: "unrelated small reviewed change", Key: local.ID(), Reviewed: sv.Review})
	if e != nil {
		t.Fatal("large root blocked small root", e)
	}
	q := model.PoolMutation{Operation: "set", ID: big.ID, Revision: count(big.Revision), Capacity: count(9), Reason: "never truncate review", Key: local.ID(), Reviewed: bv.Review}
	if _, e = m.MutatePool(q); errorCode(e) != "hierarchy_review_too_large" {
		t.Fatal(e)
	}
	bv.Review.Unavailable = ""
	bv.Review.Hierarchy = &model.PoolHierarchyReview{Schema: 1, Pool: big.ID, Root: big.ID, Pools: 1101, Consumers: 0, SHA256: strings.Repeat("a", 64)}
	q.Key = local.ID()
	if _, e = m.MutatePool(q); errorCode(e) != "hierarchy_review_too_large" {
		t.Fatal("forged incomplete digest accepted", e)
	}
}
