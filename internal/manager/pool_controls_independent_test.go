package manager

import (
	"testing"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

func TestIndependentPoolReviewKeepsEndingAndUncertainObligations(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "uncertain-review", "host", 2)
	for _, state := range []string{"ending", "needs_attention"} {
		lease, err := m.Acquire(poolRequest(pool, 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		lease.State = state
		lease.Revision++
		if err = m.put(lease, "fixture.uncertain"); err != nil {
			t.Fatal(err)
		}
	}
	view, err := m.PoolInspect(pool.ID)
	if err != nil || view.Reserved != 2 || len(view.Review.Consumers) != 2 {
		t.Fatalf("uncertain charges absent %+v %v", view, err)
	}
	request := model.PoolMutation{Operation: "set", ID: pool.ID, Revision: count(pool.Revision), Capacity: count(0), Reason: "preserve uncertainty", Key: local.ID(), Reviewed: view.Review}
	if _, err = m.MutatePool(request); err != nil {
		t.Fatal(err)
	}
	view, _ = m.PoolInspect(pool.ID)
	if view.Reserved != 2 || view.Deficit != 2 || len(view.Review.Consumers) != 2 {
		t.Fatalf("reduction erased uncertain use %+v", view)
	}
	_, err = m.MutatePool(model.PoolMutation{Operation: "delete", ID: pool.ID, Revision: count(view.Pool.Revision), Reason: "must not erase obligations", Key: local.ID(), Reviewed: view.Review})
	if errorCode(err) != "pool_in_use" {
		t.Fatal("delete erased uncertain claims", err)
	}
}
func TestIndependentReviewedPoolFaultRollsBackLookupAndJournal(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "rollback-review", "global", 2)
	view, _ := m.PoolInspect(pool.ID)
	request := model.PoolMutation{Operation: "adjust", ID: pool.ID, Revision: count(pool.Revision), Delta: count(1), Reason: "atomic review fixture", Key: local.ID(), Reviewed: view.Review}
	if _, err := m.db.Exec(`CREATE TRIGGER independent_pool_failure BEFORE INSERT ON events WHEN NEW.type='pool.adjust' BEGIN SELECT RAISE(ABORT,'independent reviewed mutation rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.MutatePool(request); err == nil {
		t.Fatal("ignored journal fault")
	}
	lookup, err := m.LookupPoolMutation(request.Key)
	if err != nil || lookup.Found {
		t.Fatalf("uncommitted lookup %+v %v", lookup, err)
	}
	current, _ := m.PoolInspect(pool.ID)
	history, _ := m.PoolHistory(pool.ID, 0)
	if current.Pool.Revision != pool.Revision || current.Pool.Capacity != 2 || len(history.Entries) != 1 {
		t.Fatalf("partial reviewed mutation %+v %+v", current, history)
	}
	m.db.Exec(`DROP TRIGGER independent_pool_failure`)
	change, err := m.MutatePool(request)
	if err != nil || change.Pool.Capacity != 3 {
		t.Fatalf("exact retry after rollback %+v %v", change, err)
	}
}
