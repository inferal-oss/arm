package manager

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func memoryReading(m *Manager, total, available int64, age time.Duration) {
	m.observationMu.Lock()
	m.memoryAvailability = model.MemoryAvailability{At: time.Now().UTC(), Total: &total, Available: &available, Quality: "estimated", Scope: "fixture", Method: "injected"}
	m.memoryObserved = time.Now().Add(-age)
	m.observationMu.Unlock()
}

func TestLiveMemoryPoolShrinksPreservesClaimsAndRecoversQueue(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 0
	memoryReading(m, 10000, 8000, 0)
	r := model.Request{Reason: "live memory promise", Resources: model.Resources{Memory: 6000}, TTL: time.Minute, Key: "live-existing"}
	first, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	memoryReading(m, 10000, 4000, 0)
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Memory.Capacity != 4000 || s.Memory.Deficit != 2000 || s.Memory.Available != 0 || s.Reserved.Memory != 6000 || s.Memory.Ceiling != nil {
		t.Fatalf("shrunk pool: %+v", s.Memory)
	}
	if s.Resources[1].Deficit != "2000" || s.Resources[1].Available != "0" {
		t.Fatalf("missing resource shortfall: %+v", s.Resources[1])
	}
	if retry, err := m.Acquire(r, nil); err != nil || retry.ID != first.ID {
		t.Fatal("shrink broke exact request retry", err)
	}
	if _, err = m.Renew(first.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal("renewed during shortfall", err)
	}
	wait := model.Request{Reason: "wait for live headroom", Resources: model.Resources{Memory: 1000}, TTL: time.Minute, Queue: true}
	queued, err := m.Acquire(wait, nil)
	if err != nil || queued.State != "queued" {
		t.Fatalf("queue: %+v %v", queued, err)
	}
	m.tick()
	still, _ := m.Inspect(queued.ID)
	if still.State != "queued" {
		t.Fatal("queue ignored current shortage")
	}
	// A shortage does not invent a reason to deny an unrelated CPU-only claim.
	cpu, err := m.Acquire(model.Request{Reason: "unrelated CPU", Resources: model.Resources{CPU: 1}, TTL: time.Minute, Queue: true}, nil)
	if err != nil || cpu.State != "queued" {
		t.Fatal("expected existing FIFO queue to apply", err)
	}
	memoryReading(m, 10000, 9000, 0)
	m.tick()
	admitted, _ := m.Inspect(queued.ID)
	if admitted.State != "active" {
		t.Fatalf("queue did not wake after growth: %+v", admitted)
	}
	if current, _ := m.Inspect(first.ID); current.State != "active" {
		t.Fatal("shrinking pool revoked an existing lease")
	}
}

func TestLiveMemoryUnknownStaleAndConcurrentAdmission(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 0
	memoryReading(m, 1000, 1000, 4*time.Second)
	r := model.Request{Reason: "fresh estimate required", Resources: model.Resources{Memory: 100}, TTL: time.Minute}
	if _, err := m.Acquire(r, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("stale capacity admitted", err)
	}
	memoryReading(m, 1000, 1000, -time.Second)
	if _, err := m.Acquire(r, nil); err == nil {
		t.Fatal("future timestamp admitted")
	}
	memoryReading(m, 1000, 1000, 0)
	m.observationMu.Lock()
	m.memoryAvailability.Available = nil
	m.observationMu.Unlock()
	if _, err := m.Acquire(r, nil); err == nil {
		t.Fatal("unknown capacity admitted")
	}
	if _, err := m.Acquire(model.Request{Reason: "CPU with no memory claim", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil); err != nil {
		t.Fatal("unknown memory blocked an unrelated claim", err)
	}
	memoryReading(m, 1000, 1000, 0)
	var wg sync.WaitGroup
	var grants atomic.Int32
	for range 30 {
		wg.Go(func() {
			if _, err := m.Acquire(r, nil); err == nil {
				grants.Add(1)
			}
		})
	}
	wg.Wait()
	if grants.Load() != 10 {
		t.Fatalf("memory over/under-admitted: %d", grants.Load())
	}
}

func TestLiveMemoryCeilingFailureAndExactBoundary(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 500
	memoryReading(m, 10000, 8000, 0)
	if b := m.memoryBudget(0); b.Capacity != 500 || b.Ceiling == nil || *b.Ceiling != 500 {
		t.Fatal("ceiling ignored", b)
	}
	memoryReading(m, 10000, 200, 0)
	if b := m.memoryBudget(300); b.Capacity != 200 || b.Deficit != 100 {
		t.Fatal("ceiling invented headroom", b)
	}
	memoryReading(m, 10000, 10001, 0)
	if b := m.memoryBudget(0); b.Capacity != 0 {
		t.Fatal("invalid estimate admitted", b)
	}
	m.capacity.Memory = math.MaxInt64
	memoryReading(m, math.MaxInt64, math.MaxInt64, 0)
	if !m.fitsLiveResources(model.Resources{Memory: math.MaxInt64}, model.Resources{}) || m.fitsLiveResources(model.Resources{Memory: 1}, model.Resources{Memory: math.MaxInt64}) {
		t.Fatal("exact int64 memory boundary lost")
	}
}

func TestLiveMemoryRejectsImpossiblePhysicalRequestWithoutQueuing(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 0
	memoryReading(m, 10000, 8000, 0)
	r := model.Request{Reason: "larger than physical memory", Resources: model.Resources{Memory: 10001}, TTL: time.Minute, Queue: true}
	if _, err := m.Acquire(r, nil); errorCode(err) != "capacity_exceeded" {
		t.Fatal("physically impossible request was queued", err)
	}
	s, err := m.Status()
	if err != nil || len(s.Leases) != 0 {
		t.Fatal("impossible request left a lease", s.Leases, err)
	}
	// A request above today's free estimate but within physical capacity can
	// still wait for unrelated applications to relinquish their allocations.
	r.Resources.Memory = 9000
	l, err := m.Acquire(r, nil)
	if err != nil || l.State != "queued" {
		t.Fatal("temporary shortage confused with impossible capacity", l, err)
	}
}

func TestLiveMemoryFreshnessIncludesSuspendAndWallClockChanges(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 0
	for _, offset := range []time.Duration{-time.Minute, time.Minute} {
		memoryReading(m, 1000, 1000, 0)
		m.observationMu.Lock()
		// Simulate a collector whose monotonic elapsed clock stayed fresh while
		// the source timestamp became old after suspend, or lies in the future
		// following a backwards wall-clock adjustment.
		m.memoryAvailability.At = time.Now().UTC().Add(offset)
		m.observationMu.Unlock()
		if b := m.memoryBudget(100); !b.Stale || b.Capacity != 0 || b.Deficit != 100 {
			t.Fatal("wall-clock staleness did not fail closed", offset, b)
		}
		if _, err := m.Acquire(model.Request{Reason: "fresh source required", Resources: model.Resources{Memory: 1}, TTL: time.Minute}, nil); errorCode(err) != "insufficient_capacity" {
			t.Fatal("stale source timestamp admitted memory", offset, err)
		}
	}
	memoryReading(m, 1000, 1000, 0)
	if b := m.memoryBudget(100); b.Stale || b.Available != 900 {
		t.Fatal("fresh sample did not resume admission", b)
	}
}
