package manager

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func diskReading(m *Manager, total, available int64, age time.Duration) {
	m.observationMu.Lock()
	defer m.observationMu.Unlock()
	m.diskAvailability = model.DiskAvailability{At: time.Now().UTC().Add(-age), Total: &total, Available: &available, Scope: "fixture-state-filesystem", Volume: "fixture", Method: "fixture", Quality: "reported"}
	m.diskObserved = time.Now().Add(-age)
}
func diskRequest(key string, n int64) model.Request {
	r := request(key)
	r.Resources = model.Resources{Disk: n}
	return r
}
func TestLiveDiskAutoMutableHeadroomCeilingAndNoTotalImpossibility(t *testing.T) {
	m := newManager(t)
	m.capacity.Disk = 0
	diskReading(m, 100, 50, 0)
	r := diskRequest("larger-than-current-filesystem", 150)
	r.Queue = true
	waiting, err := m.Acquire(r, nil)
	if err != nil || waiting.State != "queued" || waiting.QueueInfo.Estimate.Kind != "unknown" {
		t.Fatal(waiting, err)
	}
	found := false
	for _, b := range waiting.QueueInfo.Blockers {
		if b.Code == "disk_headroom" {
			found = true
			if b.Available == nil || b.Available.String() != "50" {
				t.Fatal(b)
			}
		}
	}
	if !found {
		t.Fatal(waiting.QueueInfo)
	}
	diskReading(m, 400, 300, 0)
	m.tick()
	granted, err := m.Inspect(waiting.ID)
	if err != nil || granted.State != "active" {
		t.Fatal(granted, err)
	}
	status, err := m.Status()
	if err != nil || status.Disk == nil || status.Disk.Ceiling != nil || status.Capacity.Disk != 300 || status.Disk.Reserved != 150 || status.Disk.Available != 150 {
		t.Fatal(status.Disk, err)
	}
	m2 := newManager(t)
	m2.capacity.Disk = 50
	diskReading(m2, 1000, 900, 0)
	r = diskRequest("above-explicit-ceiling", 51)
	r.Queue = true
	if _, err = m2.Acquire(r, nil); errorCode(err) != "capacity_exceeded" {
		t.Fatal("explicit ceiling lost permanent rejection", err)
	}
}
func TestLiveDiskFullPromisesDeficitRenewAndZeroDiskIndependence(t *testing.T) {
	m := newManager(t)
	m.capacity.Disk = 0
	diskReading(m, 1000, 100, 0)
	active, err := m.Acquire(diskRequest("full-promise", 80), nil)
	if err != nil {
		t.Fatal(err)
	}
	// A workload may already have consumed some physical blocks. Keep charging
	// its entire promise rather than crediting those blocks as exclusive usage.
	diskReading(m, 1000, 60, 0)
	budget := m.diskBudget(80)
	if !budget.Known || budget.Deficit != 20 || budget.Available != 0 {
		t.Fatal(budget)
	}
	if _, err = m.Renew(active.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal(err)
	}
	if _, err = m.Acquire(diskRequest("pressure", 1), nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal(err)
	}
	cpu, err := m.Acquire(request("cpu-independent"), nil)
	if err != nil {
		t.Fatal("zero-disk work depends on disk pressure", err)
	}
	m.End(cpu.ID, "released")
	diskReading(m, 1000, 100, 0)
	if _, err = m.Renew(active.ID, 2*time.Minute); err != nil {
		t.Fatal("renewal double-counted own disk allocation", err)
	}
}
func TestLiveDiskStaleUnknownKnownZeroAndClockRollback(t *testing.T) {
	m := newManager(t)
	m.capacity.Disk = 0
	for _, age := range []time.Duration{4 * time.Second, -time.Second} {
		diskReading(m, 1000, 900, age)
		budget := m.diskBudget(10)
		if budget.Known || !budget.Stale || budget.Deficit != 0 {
			t.Fatal("unknown disk became measured shortage", budget)
		}
		if _, err := m.Acquire(diskRequest("stale", 1), nil); errorCode(err) != "insufficient_capacity" {
			t.Fatal(err)
		}
	}
	cpu, err := m.Acquire(request("disk-unavailable-cpu"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.End(cpu.ID, "released")
	diskReading(m, 1000, 0, 0)
	budget := m.diskBudget(10)
	if !budget.Known || budget.Stale || budget.Deficit != 10 {
		t.Fatal("known full disk became unavailable", budget)
	}
	diskReading(m, math.MaxInt64, math.MaxInt64, 0)
	budget = m.diskBudget(9007199254740993)
	if budget.Reserved != 9007199254740993 || budget.Available != math.MaxInt64-9007199254740993 {
		t.Fatal(budget)
	}
}
func TestLiveDiskUnresolvedPromisesRemainCharged(t *testing.T) {
	m := newManager(t)
	m.capacity.Disk = 0
	diskReading(m, 1000, 100, 0)
	for i, state := range []string{"active", "ending", "needs_attention", "reclaiming"} {
		l, err := m.Acquire(diskRequest(string(rune('a'+i)), 10), nil)
		if err != nil {
			t.Fatal(err)
		}
		l.State = state
		if err = m.put(l, "disk.fixture"); err != nil {
			t.Fatal(err)
		}
	}
	status, err := m.Status()
	if err != nil || status.Disk.Reserved != 40 || status.Disk.Available != 60 {
		t.Fatal(status.Disk, err)
	}
}
func TestLiveDiskCollectorBoundedAndIdentityChangeUnavailable(t *testing.T) {
	m := newManager(t)
	m.observationMu.Lock()
	m.diskScopeVolume = "expected"
	m.observationMu.Unlock()
	m.diskCollector = func(context.Context, string) model.DiskAvailability {
		total, available := int64(100), int64(90)
		return model.DiskAvailability{At: time.Now().UTC(), Total: &total, Available: &available, Volume: "replacement", Quality: "reported"}
	}
	m.sampleDisk()
	if budget := m.diskBudget(0); budget.Known || budget.Observation.Available != nil {
		t.Fatal("replacement volume inherited scope", budget)
	}
	hung := make(chan struct{})
	var calls atomic.Int32
	m.diskCollector = func(context.Context, string) model.DiskAvailability {
		calls.Add(1)
		<-hung
		return model.DiskAvailability{}
	}
	start := time.Now()
	m.sampleDisk()
	for range 10 {
		m.sampleDisk()
	}
	if calls.Load() != 1 || time.Since(start) > 2*time.Second {
		t.Fatal("unbounded collector", calls.Load(), time.Since(start))
	}
	if budget := m.diskBudget(0); budget.Known {
		t.Fatal(budget)
	}
	close(hung)
}

func TestLiveDiskDaemonAutoDoesNotBroadenPeerOrPolicyZero(t *testing.T) {
	m := newManager(t)
	m.capacity.Disk = 0
	diskReading(m, 1000, 900, 0)
	req := diskRequest("peer-no-disk-grant", 1)
	if _, err := m.AcquirePeer("peer", req, nil, model.Resources{CPU: 1}, time.Hour); errorCode(err) != "forbidden" {
		t.Fatal("peer grant zero became auto", err)
	}
	node := makePolicyBudget(t, m, "self", "zero-disk-policy")
	zero := int64(0)
	setPolicy(t, m, node, model.BudgetLimits{Disk: &zero})
	req.Budget = node.ID
	if _, err := m.Acquire(req, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("policy zero became auto", err)
	}
}
