package manager

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func count(v int64) *int64 { return &v }
func makePool(t *testing.T, m *Manager, name, scope string, capacity int64) model.Pool {
	t.Helper()
	change, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: name, Scope: scope, Capacity: count(capacity), Reason: "coordination fixture", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	return change.Pool
}
func poolRequest(p model.Pool, q int64) model.Request {
	return model.Request{Reason: "consume counted permits", TTL: time.Minute, Key: local.ID(), Pools: []model.PoolClaim{{Pool: p.ID, Quantity: q}}}
}

func TestPoolsAtomicAdmissionQueueGrowthAndDeficit(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "browser slots", "host", 0)
	global := makePool(t, m, "browser slots", "global", 2)
	if p.Host != m.host || global.Host != "" || global.Authority != m.host || p.ID == global.ID {
		t.Fatal("resource scope confused with execution host")
	}
	if _, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: p.Name, Scope: "host", Capacity: count(3), Reason: "duplicate", Key: local.ID()}); err == nil {
		t.Fatal("silently replaced a named definition")
	}
	r := poolRequest(p, 1)
	r.Resources.CPU = 1
	r.Queue = true
	queued, err := m.Acquire(r, nil)
	if err != nil || queued.State != "queued" {
		t.Fatalf("zero-capacity queue: %+v %v", queued, err)
	}
	status, _ := m.Status()
	if status.Reserved.CPU != 0 {
		t.Fatal("queued pool claim held native budget")
	}
	if _, err = m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(3), Reason: "three browsers available", Key: local.ID()}); err != nil {
		t.Fatal(err)
	}
	m.tick()
	eventually(t, func() bool { l, _ := m.Inspect(queued.ID); return l.State == "active" })
	second, err := m.Acquire(poolRequest(p, 2), nil)
	if err != nil || second.State != "active" {
		t.Fatal(err)
	}
	current, _ := m.PoolInspect(p.ID)
	changed, err := m.MutatePool(model.PoolMutation{Operation: "set", ID: p.ID, Revision: count(current.Pool.Revision), Capacity: count(1), Reason: "devices unavailable", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := m.PoolInspect(p.ID)
	if v.Reserved != 3 || v.Available != 0 || v.Deficit != 2 || len(v.Users) != 2 || v.Users[0].Reason == "" {
		t.Fatalf("reduction erased obligations: %+v", v)
	}
	if changed.Previous.Capacity != 3 || changed.Pool.Capacity != 1 || changed.Delta != -2 || changed.Actor != m.owner || changed.Reason != "devices unavailable" || changed.At.IsZero() {
		t.Fatalf("incomplete journal: %+v", changed)
	}
	if _, err = m.Renew(second.ID, 2*time.Minute); err == nil {
		t.Fatal("deficit renewed indefinitely")
	}
	if _, err = m.Acquire(poolRequest(p, 1), nil); err == nil {
		t.Fatal("admitted over a deficit")
	}
	waiting := poolRequest(p, 1)
	waiting.Queue = true
	next, err := m.Acquire(waiting, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.End(second.ID, "released")
	m.tick()
	next, _ = m.Inspect(next.ID)
	if next.State != "queued" {
		t.Fatal("grant ignored remaining reservation")
	}
	m.End(queued.ID, "released")
	m.tick()
	next, _ = m.Inspect(next.ID)
	if next.State != "active" {
		t.Fatal("release did not unblock queue")
	}
	current, _ = m.PoolInspect(p.ID)
	if len(current.Queued) != 0 || current.Reserved != 1 || current.Deficit != 0 {
		t.Fatalf("bad queue accounting: %+v", current)
	}
}

func TestPoolsConcurrentDeltasIdempotencyAndRevisionGuards(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "deployment slots", "global", 0)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Go(func() {
			_, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(1), Reason: fmt.Sprint("worker ", i), Key: local.ID()})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	v, _ := m.PoolInspect(p.ID)
	if v.Pool.Capacity != 40 || v.Pool.Revision != 41 {
		t.Fatalf("lost concurrent updates: %+v", v)
	}
	request := model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(-1), Reason: "device removed", Key: "repeat-delta"}
	first, err := m.MutatePool(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.MutatePool(request)
	if err != nil || first.Sequence != second.Sequence || second.Pool.Capacity != 39 {
		t.Fatal("reapplied a retried delta", err)
	}
	request.Delta = count(1)
	if _, err = m.MutatePool(request); err == nil {
		t.Fatal("reused operation key for different parameters")
	}
	if _, err = m.MutatePool(model.PoolMutation{Operation: "set", ID: p.ID, Capacity: count(5), Revision: count(1), Reason: "stale edit", Key: local.ID()}); err == nil {
		t.Fatal("lost revision conflict")
	}
	history, err := m.PoolHistory(p.ID, 0)
	if err != nil || len(history.Entries) != 42 {
		t.Fatalf("journal missing changes: %d %v", len(history.Entries), err)
	}
	for i, entry := range history.Entries {
		if entry.Actor != m.owner || entry.Reason == "" || entry.Sequence == 0 || entry.Pool.Revision != int64(i+1) {
			t.Fatalf("journal attribution/revisions: %+v", entry)
		}
	}
}

func TestPoolsConcurrentGrantsAndCompleteBundles(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "slots", "global", 4)
	var wg sync.WaitGroup
	granted := make(chan model.Lease, 32)
	for range 32 {
		wg.Go(func() {
			if l, err := m.Acquire(poolRequest(p, 1), nil); err == nil {
				granted <- l
			}
		})
	}
	wg.Wait()
	close(granted)
	if len(granted) != 4 {
		t.Fatalf("over/under admitted: %d", len(granted))
	}
	other := makePool(t, m, "other", "host", 1)
	r := poolRequest(other, 1)
	r.Pools = append(r.Pools, model.PoolClaim{Pool: p.ID, Quantity: 1})
	r.Queue = true
	r.Resources.CPU = 1
	l, err := m.Acquire(r, nil)
	if err != nil || l.State != "queued" {
		t.Fatal(err)
	}
	v, _ := m.PoolInspect(other.ID)
	s, _ := m.Status()
	if v.Reserved != 0 || s.Reserved.CPU != 0 {
		t.Fatal("partial grant held a different resource")
	}
	for active := range granted {
		m.End(active.ID, "released")
	}
	m.tick()
	l, _ = m.Inspect(l.ID)
	if l.State != "active" || len(l.Request.Pools) != 2 {
		t.Fatal("complete grant did not become active")
	}
}

func TestPoolsLifecycleDrainDeleteAndExactInt64(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "exclusive", "global", 1)
	l, err := m.Acquire(poolRequest(p, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := poolRequest(p, 1)
	r.Queue = true
	queued, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	change, err := m.MutatePool(model.PoolMutation{Operation: "drain", ID: p.ID, Revision: count(p.Revision), Reason: "maintenance", Key: local.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(poolRequest(p, 1), nil); err == nil {
		t.Fatal("new request entered a draining pool")
	}
	if _, err = m.MutatePool(model.PoolMutation{Operation: "delete", ID: p.ID, Revision: count(change.Pool.Revision), Reason: "remove", Key: local.ID()}); err == nil {
		t.Fatal("deleted outstanding claims")
	}
	m.End(l.ID, "released")
	m.tick()
	queued, _ = m.Inspect(queued.ID)
	if queued.State != "active" {
		t.Fatal("drain abandoned an existing queued request")
	}
	m.End(queued.ID, "released")
	deleted, err := m.MutatePool(model.PoolMutation{Operation: "delete", ID: p.ID, Revision: count(change.Pool.Revision), Reason: "maintenance done", Key: local.ID()})
	if err != nil || deleted.Pool.State != "deleted" {
		t.Fatal(err)
	}
	if _, err = m.Acquire(poolRequest(p, 1), nil); err == nil {
		t.Fatal("deleted pool granted new claims")
	}
	repeated, err := m.Acquire(l.Request, nil)
	if err != nil || repeated.ID != l.ID {
		t.Fatal("tombstone broke a request retry", err)
	}
	if _, err = m.PoolHistory(p.ID, 0); err != nil {
		t.Fatal("lost tombstone history", err)
	}
	makePool(t, m, p.Name, "global", 2)
	large := makePool(t, m, "exact counts", "global", math.MaxInt64)
	claim := poolRequest(large, 1<<53+1)
	lease, err := m.Acquire(claim, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range s.Resources {
		if row.ID == large.ID {
			if row.Reserved.String() != "9007199254740993" || row.BudgetTotal.String() != "9223372036854775807" {
				t.Fatalf("counts rounded to float: %+v", row)
			}
			b, _ := json.Marshal(row)
			var copy model.ResourceView
			if json.Unmarshal(b, &copy) != nil || copy.Reserved != row.Reserved {
				t.Fatal("JSON lost integer precision")
			}
		}
	}
	m.End(lease.ID, "released")
}

func TestPoolTransactionsRollbackAndRecovery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.Close() }()
	p := makePool(t, m, "recovery", "global", 2)
	r := model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(1), Reason: "one more slot", Key: "recover-key"}
	if _, err = m.db.Exec(`CREATE TRIGGER fail_pool_event BEFORE INSERT ON events WHEN NEW.type='pool.adjust' BEGIN SELECT RAISE(ABORT,'fixture transaction failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.MutatePool(r); err == nil {
		t.Fatal("injected failure was not observed")
	}
	view, _ := m.PoolInspect(p.ID)
	history, _ := m.PoolHistory(p.ID, 0)
	if view.Pool.Capacity != 2 || view.Pool.Revision != 1 || len(history.Entries) != 1 {
		t.Fatal("partial pool state/journal committed")
	}
	m.db.Exec(`DROP TRIGGER fail_pool_event`)
	first, err := m.MutatePool(r)
	if err != nil {
		t.Fatal("failed transaction retained retry key", err)
	}
	lease, err := m.Acquire(poolRequest(p, 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	retried, err := m.MutatePool(r)
	if err != nil || retried.Sequence != first.Sequence {
		t.Fatal("retry across restart duplicated delta", err)
	}
	view, _ = m.PoolInspect(p.ID)
	if view.Reserved != 1 || view.Pool.Capacity != 3 {
		t.Fatal("restart lost pool/claim")
	}
	m.End(lease.ID, "released")
	expiry := poolRequest(p, 3)
	expiry.TTL = 30 * time.Millisecond
	exp, err := m.Acquire(expiry, nil)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { m.tick(); x, _ := m.Inspect(exp.ID); return x.State == "terminal" })
	view, _ = m.PoolInspect(p.ID)
	if view.Reserved != 0 {
		t.Fatal("cooperative expiry retained counted entitlement")
	}
}

func TestPoolValidationPaginationAndImmutableScope(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "safe", "global", 1)
	for _, r := range []model.PoolMutation{
		{Operation: "create", Name: "missing scope", Capacity: count(1)},
		{Operation: "create", Name: "bad host", Scope: "global", Host: "localhost", Capacity: count(1)},
		{Operation: "create", Name: "remote", Scope: "host", Host: "remote", Capacity: count(1)},
		{Operation: "adjust", ID: p.ID, Delta: count(math.MinInt64)},
		{Operation: "set", ID: p.ID, Capacity: count(-1), Revision: count(1)},
		{Operation: "set", ID: p.ID, Capacity: count(1)},
		{Operation: "adjust", ID: p.ID, Delta: count(0)},
		{Operation: "adjust", ID: p.ID, Delta: count(1), Host: "localhost"},
		{Operation: "adjust", ID: p.ID, Delta: count(1), Scope: "host"},
		{Operation: "adjust", ID: p.ID, Delta: count(1), Name: "ignored"},
		{Operation: "adjust", ID: p.ID, Delta: count(1), Agent: "unknown"},
	} {
		r.Key = local.ID()
		r.Reason = "reject invalid mutation"
		if _, err := m.MutatePool(r); err == nil {
			t.Fatalf("accepted invalid mutation: %+v", r)
		}
	}
	for _, claims := range [][]model.PoolClaim{
		{{Pool: p.ID, Quantity: 0}}, {{Pool: p.ID, Quantity: -1}},
		{{Pool: p.ID, Quantity: math.MaxInt64}, {Pool: p.ID, Quantity: 1}},
		{{Pool: p.ID, Quantity: 1, Scope: "host"}}, {{Pool: p.ID, Quantity: 1, Authority: "other"}},
	} {
		r := poolRequest(p, 1)
		r.Pools = claims
		if _, err := m.Acquire(r, nil); err == nil {
			t.Fatalf("invalid claim accepted: %+v", claims)
		}
	}
	for range 104 {
		if _, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(1), Reason: "history pagination", Key: local.ID()}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := m.PoolHistory(p.ID, 0)
	if err != nil || len(page.Entries) != 100 || !page.More {
		t.Fatalf("history pagination: %+v %v", page, err)
	}
	next, err := m.PoolHistory(p.ID, page.Cursor)
	if err != nil || len(next.Entries) != 5 || next.More || next.Entries[0].Sequence <= page.Cursor {
		t.Fatalf("history skipped changes: %+v %v", next, err)
	}
	maxed := makePool(t, m, "maxed", "global", math.MaxInt64)
	if _, err = m.MutatePool(model.PoolMutation{Operation: "adjust", ID: maxed.ID, Delta: count(1), Reason: "overflow", Key: local.ID()}); err == nil {
		t.Fatal("overflowed maximum capacity")
	}
	change, err := m.MutatePool(model.PoolMutation{Operation: "rename", ID: p.ID, Name: "renamed", Revision: count(105), Reason: "clear name", Key: local.ID()})
	if err != nil || change.Pool.ID != p.ID {
		t.Fatal("rename lost stable identity", err)
	}
	change, err = m.MutatePool(model.PoolMutation{Operation: "describe", ID: p.ID, Description: "new description", Revision: count(change.Pool.Revision), Reason: "explain resource", Key: local.ID()})
	if err != nil || change.Pool.Description != "new description" {
		t.Fatal(err)
	}
}

func TestPoolAuditRetainsAgentSessionAcrossContactReuse(t *testing.T) {
	m := newManager(t)
	agent := pollContact(t, m, "audited-agent")
	created, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: "attributed", Scope: "global", Capacity: count(1), Agent: agent.ID, Project: "fixture", Reason: "coordinate agents", Key: "agent-create"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Session != agent.Session || created.Registration != agent.Registration || created.Pool.Session != agent.Session {
		t.Fatal("audit omitted exact agent identity")
	}
	next := pollContact(t, m, agent.ID)
	changed, err := m.MutatePool(model.PoolMutation{Operation: "adjust", ID: created.Pool.ID, Delta: count(1), Agent: next.ID, Reason: "new session adjustment", Key: "agent-adjust"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := m.PoolHistory(created.Pool.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if history.Entries[0].Session != agent.Session || history.Entries[1].Session != next.Session || changed.Pool.Session != agent.Session {
		t.Fatal("reused contact rewrote historical or creator identity")
	}
}

func TestNativeBudgetCannotOverflowAlongsideCustomClaims(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = math.MaxInt64
	// Inject an exact boundary reading: never ask the real host for exabytes.
	m.memoryAvailability = model.MemoryAvailability{Total: count(math.MaxInt64), Available: count(math.MaxInt64), At: time.Now().UTC(), Quality: "estimated"}
	m.memoryObserved = time.Now()
	p := makePool(t, m, "room", "global", 2)
	r := poolRequest(p, 1)
	r.Resources.Memory = math.MaxInt64
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	r = poolRequest(p, 1)
	r.Resources.Memory = 1
	if _, err := m.Acquire(r, nil); err == nil {
		t.Fatal("integer overflow admitted a native budget overrun")
	}
	v, _ := m.PoolInspect(p.ID)
	if v.Reserved != 1 {
		t.Fatal("failed native bundle partially reserved custom capacity")
	}
}
