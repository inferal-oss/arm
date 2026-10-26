package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func makeChild(t *testing.T, m *Manager, parent model.Pool, name string, n int64) model.Pool {
	t.Helper()
	v, err := m.MutatePool(model.PoolMutation{Operation: "create", Parent: parent.ID, ParentRevision: count(parent.Revision), Name: name, Capacity: count(n), Reason: "carve owned partition", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return v.Pool
}
func setPartition(t *testing.T, m *Manager, p model.Pool, n int64) model.Pool {
	t.Helper()
	current, err := m.PoolInspect(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.MutatePool(model.PoolMutation{Operation: "set", ID: p.ID, Revision: count(current.Pool.Revision), Capacity: count(n), Reason: "resize without erasing obligations", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return v.Pool
}
func TestSubpoolProductionCarveDeficitAndDelayedReturn(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "partition-root", "host", 10)
	child := makeChild(t, m, root, "partition-child", 6)
	v, err := m.PoolInspect(root.ID)
	if err != nil || v.Available != 4 || v.Reserved != 0 || v.Partition.ChildBacking != 6 {
		t.Fatal(v, err)
	}
	if _, err = m.Acquire(poolRequest(root, 5), nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("borrowed idle child", err)
	}
	l, err := m.Acquire(poolRequest(child, 5), nil)
	if err != nil {
		t.Fatal(err)
	}
	child = setPartition(t, m, child, 2)
	v, err = m.PoolInspect(child.ID)
	if err != nil || v.Partition.Held != 5 || v.Deficit != 3 || v.Partition.PendingReturn != 3 {
		t.Fatal(v, err)
	}
	parent, _ := m.PoolInspect(root.ID)
	if parent.Available != 5 {
		t.Fatal(parent)
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal("renewed child deficit", err)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	parent, _ = m.PoolInspect(root.ID)
	if parent.Available != 8 {
		t.Fatal("did not return after settlement", parent)
	}
	grand := makeChild(t, m, child, "partition-grand", 2)
	r := poolRequest(root, 8)
	r.Pools = append(r.Pools, model.PoolClaim{Pool: grand.ID, Quantity: 2})
	l, err = m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	child = setPartition(t, m, child, 0)
	parent, _ = m.PoolInspect(root.ID)
	if parent.Available != 0 || parent.Partition.ChildBacking != 2 {
		t.Fatal("descendant backing vanished", parent)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	parent, _ = m.PoolInspect(root.ID)
	if parent.Partition.ChildBacking != 2 {
		t.Fatal("configured grandchild carve vanished", parent)
	}
}

func TestSubpoolProductionDeletedParentRefusesWithoutCorruption(t *testing.T) {
	m := newManager(t)
	parent := makePool(t, m, "deleted-parent", "host", 3)
	deleted, err := m.MutatePool(model.PoolMutation{Operation: "delete", ID: parent.ID, Revision: count(parent.Revision), Reason: "retire unused pool", Key: "delete-parent"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.MutatePool(model.PoolMutation{Operation: "create", Parent: parent.ID, ParentRevision: count(deleted.Pool.Revision), Name: "impossible-child", Capacity: count(1), Reason: "must select a live parent", Key: "child-after-delete"})
	if errorCode(err) != "pool_unavailable" {
		t.Fatalf("deleted parent should be unavailable, not corrupt: %v", err)
	}
	if found, err := m.LookupPoolMutation("child-after-delete"); err != nil || found.Found {
		t.Fatalf("refused child left mutation evidence: %+v, %v", found, err)
	}
	if _, err = m.Status(); err != nil {
		t.Fatalf("refused child damaged healthy ledger: %v", err)
	}
}

func TestSubpoolProductionConcurrentSiblingsAndAtomicBundle(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "siblings-root", "global", 10)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.MutatePool(model.PoolMutation{Operation: "create", Parent: root.ID, ParentRevision: count(root.Revision), Name: fmt.Sprintf("sibling-%d", i), Capacity: count(6), Reason: "concurrent carve", Key: fmt.Sprintf("sibling-key-%d", i)})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	granted := 0
	for err := range results {
		if err == nil {
			granted++
		} else if errorCode(err) != "insufficient_capacity" {
			t.Fatal(err)
		}
	}
	if granted != 1 {
		t.Fatal(granted)
	}
	page, err := m.PoolChildren(model.PoolTreeQuery{ID: root.ID, Limit: 1})
	if err != nil || len(page.Nodes) != 1 {
		t.Fatal(page, err)
	}
	child := page.Nodes[0].Pool
	r := poolRequest(root, 5)
	r.Pools = append(r.Pools, model.PoolClaim{Pool: child.ID, Quantity: 1})
	if _, err = m.Acquire(r, nil); err == nil {
		t.Fatal("partial bundle")
	}
	ls, _ := m.list()
	if len(ls) != 0 {
		t.Fatal("failed bundle persisted partial allocation")
	}
	var leases []model.Lease
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := m.Acquire(poolRequest(child, 1), nil)
			if err == nil {
				mu.Lock()
				leases = append(leases, l)
				mu.Unlock()
			} else if errorCode(err) != "insufficient_capacity" {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(leases) != 6 {
		t.Fatal(len(leases))
	}
	page2, err := m.PoolConsumers(model.PoolTreeQuery{ID: root.ID, Descendants: true, Limit: 2})
	if err != nil || len(page2.Entries) != 2 || !page2.More {
		t.Fatal(page2, err)
	}
}
func TestSubpoolProductionOwnerCapCannotBeBypassed(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "capped-root", "host", 10)
	child := makeChild(t, m, root, "capped-child", 6)
	grand := makeChild(t, m, child, "capped-grand", 5)
	budget, err := m.BudgetInspect("self")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.MutateBudget(model.BudgetMutation{Operation: "set", ID: "self", Revision: count(budget.Budget.Revision), Limits: &model.BudgetLimits{Pools: map[string]int64{root.ID: 3}}, Reason: "cap actual descendant use", Key: "root-cap"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(poolRequest(grand, 3), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(poolRequest(child, 1), nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("bypassed ancestor cap", err)
	}
	budget, err = m.BudgetInspect("self")
	if err != nil || budget.Charged.Pools[root.ID] != 3 || budget.Charged.Pools[child.ID] != 3 || budget.Charged.Pools[grand.ID] != 3 {
		t.Fatal(budget, err)
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); err != nil {
		t.Fatal("counted renewal twice", err)
	}
}
func TestSubpoolProductionQueueDrainDeadlineAndAncestorDeficit(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "queue-root", "host", 4)
	child := makeChild(t, m, root, "queue-child", 4)
	l, err := m.Acquire(poolRequest(child, 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := poolRequest(child, 1)
	r.Queue = true
	deadline := time.Now().UTC().Add(time.Hour)
	r.StartDeadline = &deadline
	wait, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.MutatePool(model.PoolMutation{Operation: "drain", ID: root.ID, Revision: count(root.Revision), Reason: "stop new descendant bindings", Key: "drain-tree"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(poolRequest(child, 1), nil); errorCode(err) != "pool_unavailable" {
		t.Fatal(err)
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal(err)
	}
	root = setPartition(t, m, root, 2)
	seen, err := m.Inspect(wait.ID)
	if err != nil || seen.QueueInfo == nil {
		t.Fatal(seen, err)
	}
	found := false
	for _, b := range seen.QueueInfo.Blockers {
		if b.Code == "pool_partition" && b.Resource == root.ID && b.Partition.Deficit == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("missing ancestor backing explanation", seen.QueueInfo)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	m.tick()
	seen, _ = m.Inspect(wait.ID)
	if seen.State != "queued" {
		t.Fatal("ignored idle configured child backing", seen)
	}
	child = setPartition(t, m, child, 2)
	m.tick()
	seen, _ = m.Inspect(wait.ID)
	if seen.State != "active" {
		t.Fatal("drain discarded existing queued binding", seen)
	}
}
func TestSubpoolProductionJournalRetryRollbackAndReviewRefusal(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "journal-root", "host", 10)
	r := model.PoolMutation{Operation: "create", Parent: root.ID, ParentRevision: count(root.Revision), Name: "journal-child", Capacity: count(6), Reason: "durable carve", Key: "carve-once"}
	if _, err := m.db.Exec(`CREATE TRIGGER reject_lineage BEFORE INSERT ON pool_lineage BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MutatePool(r); err == nil {
		t.Fatal("did not rollback")
	}
	if _, err := m.db.Exec(`DROP TRIGGER reject_lineage`); err != nil {
		t.Fatal(err)
	}
	first, err := m.MutatePool(r)
	if err != nil {
		t.Fatal(err)
	}
	setPartition(t, m, first.Pool, 2)
	retry, err := m.MutatePool(r)
	if err != nil || retry.Pool.Capacity != 6 || retry.Sequence != first.Sequence || len(retry.Partitions) != 2 {
		t.Fatal(retry, err)
	}
	reviewed := model.PoolMutation{Operation: "set", ID: root.ID, Revision: count(root.Revision), Capacity: count(9), Reviewed: &model.PoolReview{Authority: m.host, Consumers: []model.LeaseRevision{}}, Reason: "old UI cannot review tree", Key: "unsafe-direct-review"}
	if _, err = m.MutatePool(reviewed); errorCode(err) != "hierarchy_review_required" {
		t.Fatal(err)
	}
}
func TestSubpoolProductionRestartMigrationAndDegradedCleanup(t *testing.T) {
	for _, damage := range []string{"restart", "missing-lineage", "malformed-binding", "missing-marker", "zero-binding-time"} {
		t.Run(damage, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			capacity := model.Resources{CPU: 2, Disk: 4096}
			m, err := Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			root := makePool(t, m, "recovery-root", "host", 10)
			child := makeChild(t, m, root, "recovery-child", 6)
			r := storageRequest("subpool owned artifact")
			owned, err := m.Acquire(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.Acquire(poolRequest(child, 5), nil)
			if err != nil {
				t.Fatal(err)
			}
			setPartition(t, m, child, 2)
			switch damage {
			case "missing-lineage":
				_, err = m.db.Exec(`DELETE FROM pool_lineage WHERE pool=?`, child.ID)
			case "malformed-binding":
				_, err = m.db.Exec(`UPDATE pool_claim_bindings SET data='{}' WHERE lease=?`, lease.ID)
			case "missing-marker":
				_, err = m.db.Exec(`DELETE FROM metadata WHERE key='subpool_schema'`)
			case "zero-binding-time":
				_, err = m.db.Exec(`UPDATE pool_claim_bindings SET data=json_set(data,'$.bound_at','0001-01-01T00:00:00Z') WHERE lease=?`, lease.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			m.Close()
			m, err = Open(home, capacity)
			if err != nil {
				t.Fatal("cleanup authority unavailable", err)
			}
			defer m.Close()
			if damage == "restart" {
				v, e := m.PoolInspect(root.ID)
				if e != nil || v.Partition.ChildBacking != 5 || v.Available != 5 {
					t.Fatal(v, e)
				}
				return
			}
			status, e := m.Status()
			if e != nil || status.PoolError == nil || status.Reserved.Disk != 1024 {
				t.Fatal(status, e)
			}
			if _, e = m.Acquire(poolRequest(root, 1), nil); errorCode(e) != "invalid_accounting" {
				t.Fatal("admitted degraded", e)
			}
			if _, e = m.Renew(lease.ID, 2*time.Minute); errorCode(e) != "invalid_accounting" {
				t.Fatal("renewed degraded", e)
			}
			if _, e = m.End(lease.ID, "released"); e != nil {
				t.Fatal(e)
			}
			if _, e = m.End(owned.ID, "released"); e != nil {
				t.Fatal(e)
			}
			storageEventually(t, func() bool { l, _ := m.Inspect(owned.ID); return l.State == "terminal" || l.State == "needs_attention" })
			observed, _ := m.Inspect(owned.ID)
			if observed.State == "needs_attention" {
				if observed.Artifact == nil || !observed.Artifact.NamespaceRemoved {
					t.Fatal("provider cleanup failed before namespace removal", observed)
				}
				deadline := time.Now().Add(5 * time.Second)
				for {
					_, e = m.StorageHandoff(owned.ID)
					if e == nil {
						break
					}
					if errorCode(e) != "operation_busy" || time.Now().After(deadline) {
						t.Fatal(e)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if _, e = os.Lstat(owned.ManagedDir); !os.IsNotExist(e) {
				t.Fatal("owned empty artifact not cleaned", e)
			}
		})
	}
	// Simulate a root-only pre-subpool ledger. Migration adds separate evidence;
	// original lease bytes are not rewritten.
	m := newManager(t)
	root := makePool(t, m, "legacy-root", "host", 10)
	l, err := m.Acquire(poolRequest(root, 2), nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(l.Request)
	m.mu.Lock()
	_, err = m.db.Exec(`DELETE FROM pool_claim_bindings; DELETE FROM pool_lineage; DELETE FROM metadata WHERE key='subpool_schema'`)
	if err == nil {
		err = m.initSubpoolRecords()
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after, _ := m.get(l.ID)
	raw, _ := json.Marshal(after.Request)
	if string(before) != string(raw) {
		t.Fatal("migration rewrote history")
	}
}

func TestSubpoolProductionUnresolvedBackingAndBindingRollback(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "uncertain-root", "host", 10)
	child := makeChild(t, m, root, "uncertain-child", 6)
	l, err := m.Acquire(poolRequest(child, 5), nil)
	if err != nil {
		t.Fatal(err)
	}
	setPartition(t, m, child, 1)
	for _, state := range []string{"provisioning", "ending", "needs_attention", "reclaiming"} {
		l.State = state
		if err = m.put(l, "fixture.unresolved"); err != nil {
			t.Fatal(err)
		}
		v, e := m.PoolInspect(root.ID)
		if e != nil || v.Partition.ChildBacking != 5 || v.Available != 5 {
			t.Fatalf("state %s lost backing: %+v %v", state, v, e)
		}
	}
	l.State = "active"
	if err = m.put(l, "fixture.restored"); err != nil {
		t.Fatal(err)
	}
	m.End(l.ID, "released")
	r := poolRequest(child, 1)
	r.Key = "binding-atomic"
	if _, err = m.db.Exec(`CREATE TRIGGER fail_pool_binding BEFORE INSERT ON pool_claim_bindings BEGIN SELECT RAISE(ABORT,'private binding failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(r, nil); err == nil {
		t.Fatal("binding failure committed allocation")
	}
	var n int
	if err = m.db.QueryRow(`SELECT count(*) FROM leases WHERE request_key=?`, r.Key).Scan(&n); err != nil || n != 0 {
		t.Fatal("lease escaped rolled-back bundle", n, err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER fail_pool_binding`); err != nil {
		t.Fatal(err)
	}
	created, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := m.Acquire(r, nil)
	if err != nil || retry.ID != created.ID {
		t.Fatal(retry, err)
	}
}

func TestSubpoolProductionMigrationRollbackAndStrictEvidence(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "migration-root", "global", 10)
	l, err := m.Acquire(poolRequest(root, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`DELETE FROM pool_lineage; DELETE FROM pool_claim_bindings; DELETE FROM metadata WHERE key='subpool_schema'; CREATE TRIGGER fail_migration BEFORE INSERT ON pool_claim_bindings BEGIN SELECT RAISE(ABORT,'private migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = m.initSubpoolRecords(); err == nil {
		t.Fatal("migration did not fail")
	}
	var n int
	if err = m.db.QueryRow(`SELECT (SELECT count(*) FROM pool_lineage)+(SELECT count(*) FROM metadata WHERE key='subpool_schema')`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial migration survived", n, err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER fail_migration`); err != nil {
		t.Fatal(err)
	}
	if err = m.initSubpoolRecords(); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE pool_claim_bindings SET data=json_set(data,'$.claims[0].Quantity',0) WHERE lease=?`, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(poolRequest(root, 1), nil); errorCode(err) != "invalid_accounting" {
		t.Fatal("structured alias weakened immutable lineage", err)
	}
}

func TestSubpoolProductionDeleteRequiresSettledDescendants(t *testing.T) {
	m := newManager(t)
	root := makePool(t, m, "delete-root", "global", 10)
	child := makeChild(t, m, root, "delete-child", 6)
	grand := makeChild(t, m, child, "delete-grand", 2)
	remove := func(p model.Pool, key string) error {
		_, err := m.MutatePool(model.PoolMutation{Operation: "delete", ID: p.ID, Revision: count(p.Revision), Reason: "delete only settled tree", Key: key})
		return err
	}
	if err := remove(root, "root-early"); errorCode(err) != "pool_in_use" {
		t.Fatal(err)
	}
	if err := remove(child, "child-early"); errorCode(err) != "pool_in_use" {
		t.Fatal(err)
	}
	l, err := m.Acquire(poolRequest(grand, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = remove(grand, "grand-busy"); errorCode(err) != "pool_in_use" {
		t.Fatal(err)
	}
	m.End(l.ID, "released")
	if err = remove(grand, "grand-delete"); err != nil {
		t.Fatal(err)
	}
	if err = remove(child, "child-delete"); err != nil {
		t.Fatal(err)
	}
	v, err := m.PoolInspect(root.ID)
	if err != nil || v.Available != 10 || v.Partition.ChildBacking != 0 {
		t.Fatal(v, err)
	}
	if err = remove(root, "root-delete"); err != nil {
		t.Fatal(err)
	}
	tombstone, err := m.PoolInspect(root.ID)
	if err != nil || tombstone.Pool.State != "deleted" || tombstone.Partition.Held != 0 || tombstone.Available != 0 {
		t.Fatal(tombstone, err)
	}
}
