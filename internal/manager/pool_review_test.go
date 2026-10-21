package manager

import (
	"encoding/json"
	"math"
	"sync"
	"testing"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func TestReviewedPoolMutationAtomicScopeDeficitAndDurableLookup(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "reviewed permits", "global", math.MaxInt64)
	active, err := m.Acquire(poolRequest(pool, math.MaxInt64), nil)
	if err != nil {
		t.Fatal(err)
	}
	queuedRequest := poolRequest(pool, 1)
	queuedRequest.Queue = true
	queued, err := m.Acquire(queuedRequest, nil)
	if err != nil || queued.State != "queued" {
		t.Fatal(err, queued)
	}
	view, err := m.PoolInspect(pool.ID)
	if err != nil || view.Review == nil || len(view.Review.Consumers) != 2 {
		t.Fatal("atomic review missing scope", view, err)
	}
	request := model.PoolMutation{Operation: "set", ID: pool.ID, Revision: count(pool.Revision), Capacity: count(0), Reason: "device capacity unavailable", Key: local.ID(), Reviewed: view.Review}
	stale := request
	stale.Key = local.ID()
	stale.Reviewed = &model.PoolReview{Authority: m.host, Consumers: []model.LeaseRevision{}}
	if _, err = m.MutatePool(stale); errorCode(err) != "review_conflict" {
		t.Fatal("subset review accepted", err)
	}
	change, err := m.MutatePool(request)
	if err != nil || change.Pool.Capacity != 0 || change.Previous.Capacity != math.MaxInt64 {
		t.Fatal(change, err)
	}
	current, _ := m.PoolInspect(pool.ID)
	if current.Reserved != math.MaxInt64 || current.Deficit != math.MaxInt64 || len(current.Users) != 1 || len(current.Queued) != 1 {
		t.Fatal("reduction lost obligations", current)
	}
	stored, err := m.LookupPoolMutation(request.Key)
	if err != nil || !stored.Found || stored.Request.Key != request.Key || stored.Change.Sequence != change.Sequence {
		t.Fatal(stored, err)
	}
	if _, err = m.End(active.ID, "released"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(queued.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	retry, err := m.MutatePool(request)
	if err != nil || retry.Sequence != change.Sequence {
		t.Fatal("durable retry revalidated changed scope", retry, err)
	}
	stale.Revision = count(current.Pool.Revision)
	if _, err = m.MutatePool(stale); err != nil {
		t.Fatal("fresh empty review rejected", err)
	}
	history, err := m.PoolHistory(pool.ID, 0)
	if err != nil || len(history.Entries) != 3 || history.Entries[1].Reason != request.Reason || history.Entries[1].Actor != m.owner {
		t.Fatal("journal lost attribution", history, err)
	}
}
func TestReviewedPoolAuthorityBoundsRevisionAndLookupIsolation(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "scope", "host", 2)
	view, _ := m.PoolInspect(pool.ID)
	r := model.PoolMutation{Operation: "adjust", ID: pool.ID, Delta: count(1), Reason: "guarded increment", Key: local.ID(), Reviewed: view.Review}
	if _, err := m.MutatePool(r); errorCode(err) != "invalid_request" {
		t.Fatal("unguarded adjust accepted", err)
	}
	r.Revision = count(pool.Revision)
	r.Reviewed = &model.PoolReview{Authority: "other", Consumers: []model.LeaseRevision{}}
	if _, err := m.MutatePool(r); errorCode(err) != "authority_unavailable" {
		t.Fatal("wrong authority accepted", err)
	}
	r.Reviewed = &model.PoolReview{Authority: m.host, Consumers: make([]model.LeaseRevision, 8193)}
	if _, err := m.MutatePool(r); errorCode(err) != "invalid_request" {
		t.Fatal("oversize review silently truncated", err)
	}
	r.Reviewed = view.Review
	change, err := m.MutatePool(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", string(make([]byte, 129))} {
		if _, err = m.LookupPoolMutation(key); errorCode(err) != "invalid_request" {
			t.Fatal("bad lookup key", err)
		}
	}
	if found, err := m.LookupPoolMutation("missing"); err != nil || found.Found {
		t.Fatal(found, err)
	}
	change.Actor = "another-owner"
	raw, _ := json.Marshal(change)
	m.db.Exec(`UPDATE pool_operations SET result=? WHERE key=?`, string(raw), r.Key)
	if found, err := m.LookupPoolMutation(r.Key); err != nil || found.Found {
		t.Fatal("foreign owner lookup exposed", found, err)
	}
	change.Actor = m.owner
	change.Pool.Authority = "another-authority"
	raw, _ = json.Marshal(change)
	m.db.Exec(`UPDATE pool_operations SET result=? WHERE key=?`, string(raw), r.Key)
	if found, err := m.LookupPoolMutation(r.Key); err != nil || found.Found {
		t.Fatal("foreign authority lookup exposed", found, err)
	}
}
func TestReviewedPoolAdjustmentsCannotLoseConcurrentUpdates(t *testing.T) {
	m := newManager(t)
	pool := makePool(t, m, "race", "host", 1)
	view, _ := m.PoolInspect(pool.ID)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Go(func() {
			_, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: pool.ID, Revision: count(pool.Revision), Delta: count(1), Reason: "concurrent reviewed increment", Key: local.ID(), Reviewed: view.Review})
			results <- err
		})
	}
	group.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errorCode(err) == "revision_conflict" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	current, _ := m.PoolInspect(pool.ID)
	if success != 1 || conflicts != 1 || current.Pool.Capacity != 2 {
		t.Fatal(success, conflicts, current)
	}
}
