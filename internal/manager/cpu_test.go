package manager

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/model"
)

// Controlled admission evidence. Native accuracy is tested independently in
// observe; these fixtures never claim to measure the developer's actual host.
func cpuReading(m *Manager, eligible int, effective, busy, available float64, age time.Duration) {
	m.observationMu.Lock()
	defer m.observationMu.Unlock()
	m.cpuAvailability = model.CPUAvailability{Known: true, At: time.Now().UTC().Add(-age), Interval: time.Second, EligibleCPUs: &eligible,
		Capacity: &effective, Busy: &busy, Available: &available, Scope: "controlled-test-host", Method: "controlled-counter-interval", Quality: "estimated"}
	m.cpuObserved = time.Now().Add(-age)
}

func TestLiveCPUExternalActivityBlocksUnreservedClaimsAndRecoversFIFO(t *testing.T) {
	m := newManager(t)
	m.capacity.CPU = 4
	cpuReading(m, 8, 8, 7, 1, 0)
	wait := request("cpu-pressure-head")
	wait.Resources.CPU, wait.Queue = 2, true
	head, err := m.Acquire(wait, nil)
	if err != nil || head.State != "queued" {
		t.Fatal("external load admitted an unavailable CPU request", head, err)
	}
	wait.Key, wait.Resources.CPU = "cpu-pressure-tail", 1
	tail, err := m.Acquire(wait, nil)
	if err != nil || tail.State != "queued" {
		t.Fatal("fitting tail bypassed earlier CPU demand", tail, err)
	}
	m.tick()
	current, err := m.Inspect(head.ID)
	if err != nil || current.State != "queued" || current.QueueInfo == nil {
		t.Fatal("pressure queue state lost", current, err)
	}
	found := false
	for _, b := range current.QueueInfo.Blockers {
		found = found || b.Code == "cpu_headroom"
	}
	if !found {
		t.Fatal("queue did not explain outside CPU contention", current.QueueInfo)
	}
	cpuReading(m, 8, 8, 4, 4, 0)
	m.tick()
	for _, id := range []string{head.ID, tail.ID} {
		l, err := m.Inspect(id)
		if err != nil || l.State != "active" {
			t.Fatal("CPU recovery did not grant FIFO work", l, err)
		}
	}
	s, err := m.Status()
	if err != nil || s.CPU == nil || s.Reserved.CPU != 3 || s.CPU.Capacity != 4 || s.CPU.Available != 1 || !s.CPU.Known {
		t.Fatal("CPU promises or recovered headroom wrong", s.CPU, s.Reserved, err)
	}
}

func TestLiveCPUShrinkPreservesLeasesAndRenewalCountsSelfOnce(t *testing.T) {
	m := newManager(t)
	m.capacity.CPU = 4
	cpuReading(m, 8, 8, 4, 4, 0)
	r := request("cpu-existing")
	r.Resources.CPU = 3
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 8, 8, 7, 1, 0)
	s, err := m.Status()
	if err != nil || s.CPU.Capacity != 1 || s.CPU.Deficit != 2 || s.Reserved.CPU != 3 || s.CPU.Available != 0 {
		t.Fatal("pressure invented capacity or removed promises", s.CPU, s.Reserved, err)
	}
	if retry, err := m.Acquire(r, nil); err != nil || retry.ID != l.ID {
		t.Fatal("pressure broke original idempotent receipt", retry, err)
	}
	if _, err := m.Renew(l.ID, 2*time.Minute); errorCode(err) != "renewal_denied" {
		t.Fatal("renewal ignored live CPU deficit", err)
	}
	if old, _ := m.Inspect(l.ID); old.State != "active" {
		t.Fatal("pressure ended an existing lease", old)
	}
	cpuReading(m, 8, 8, 5, 3, 0)
	if _, err := m.Renew(l.ID, 2*time.Minute); err != nil {
		t.Fatal("renewal counted its own reservation twice", err)
	}
}

func TestLiveCPUUnknownStaleInvalidFailClosedWithoutBlockingUnrelatedResources(t *testing.T) {
	for _, kind := range []string{"unknown", "stale", "future", "missing", "nan", "overflow-headroom", "old-native-time"} {
		t.Run(kind, func(t *testing.T) {
			m := newManager(t)
			cpuReading(m, 8, 8, 0, 8, 0)
			m.observationMu.Lock()
			switch kind {
			case "unknown":
				m.cpuAvailability.Known = false
			case "stale":
				m.cpuObserved = time.Now().Add(-7 * time.Second)
			case "future":
				m.cpuAvailability.At = time.Now().Add(time.Minute)
			case "missing":
				m.cpuAvailability.Available = nil
			case "nan":
				*m.cpuAvailability.Available = math.NaN()
			case "overflow-headroom":
				*m.cpuAvailability.Available = 9
			case "old-native-time":
				m.cpuAvailability.At = time.Now().Add(-7 * time.Second)
			}
			m.observationMu.Unlock()
			if _, err := m.Acquire(request("blocked-cpu"), nil); errorCode(err) != "insufficient_capacity" {
				t.Fatal("unavailable CPU evidence admitted a claim", err)
			}
			if _, err := m.Acquire(model.Request{Reason: "unrelated memory", Resources: model.Resources{Memory: 1}, TTL: time.Minute}, nil); err != nil {
				t.Fatal("CPU evidence blocked an unrelated resource", err)
			}
		})
	}
}

func TestLiveCPUConcurrentAdmissionCannotSpendTheSameIdleCoreTwice(t *testing.T) {
	m := newManager(t)
	m.capacity.CPU = 8
	cpuReading(m, 8, 8, 7, 1, 0)
	var wg sync.WaitGroup
	var granted atomic.Int32
	for range 24 {
		wg.Go(func() {
			if _, err := m.Acquire(request(""), nil); err == nil {
				granted.Add(1)
			}
		})
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatal("overspent native CPU headroom", granted.Load())
	}
}

func TestCPUCeilingRejectsInvalidStartup(t *testing.T) {
	for _, ceiling := range []float64{math.NaN(), math.Inf(1), -1, 65537} {
		if m, err := Open(filepath.Join(t.TempDir(), "invalid-cpu"), model.Resources{CPU: ceiling}); err == nil {
			m.Close()
			t.Fatal("invalid CPU ceiling accepted", ceiling)
		}
	}
}

func TestCPUConfiguredShrinkDoesNotBlockZeroCPUWorkAfterReopen(t *testing.T) {
	home := filepath.Join(t.TempDir(), "cpu-reopen")
	var first model.Lease
	func() {
		m, err := Open(home, model.Resources{CPU: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		cpuReading(m, 8, 8, 0, 8, 0)
		r := request("cpu-preserved-across-reopen")
		r.Resources.CPU = 3
		first, err = m.Acquire(r, nil)
		if err != nil {
			t.Fatal(err)
		}
	}()
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	cpuReading(m, 8, 8, 0, 8, 0)
	if _, err := m.Acquire(model.Request{Reason: "memory-only after CPU ceiling shrink", Resources: model.Resources{Memory: 1}, TTL: time.Minute}, nil); err != nil {
		t.Fatal("CPU deficit denied unrelated work", err)
	}
	if l, err := m.Inspect(first.ID); err != nil || l.State != "active" {
		t.Fatal("reopen discarded CPU promises", l, err)
	}
	s, err := m.Status()
	if err != nil || s.Reserved.CPU != 3 || s.CPU.Deficit != 2 {
		t.Fatal("configured shrink lost deficit", s.CPU, err)
	}
}

func TestCPUQueueDoesNotPredictPressureRecoveryFromLeaseExpiry(t *testing.T) {
	m := newManager(t)
	m.capacity.CPU = 4
	cpuReading(m, 8, 8, 0, 8, 0)
	r := request("cpu-eta-holder")
	r.Resources.CPU = 3
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 8, 8, 7, 1, 0)
	r = request("cpu-eta-waiter")
	r.Resources.CPU, r.Queue = 2, true
	l, err := m.Acquire(r, nil)
	if err != nil || l.State != "queued" || l.QueueInfo == nil || l.QueueInfo.Estimate.Kind != "unknown" {
		t.Fatal("predicted external CPU recovery from a lease deadline", l, err)
	}
}

func TestCPUExecutionCannotBypassHeadroomWithOnlyMemoryClaim(t *testing.T) {
	m := newManager(t)
	m.observationMu.Lock()
	m.cpuAvailability.Known = false
	m.observationMu.Unlock()
	r := model.Request{Reason: "execution needs CPU admission", Resources: model.Resources{Memory: 1}, TTL: time.Minute}
	if _, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/true"}, CWD: t.TempDir()}); errorCode(err) != "invalid_request" {
		t.Fatal("execution bypassed CPU admission with a memory-only promise", err)
	}
	if status, err := m.Status(); err != nil || len(status.Leases) != 0 {
		t.Fatal("refused CPU-less execution created a lease", status.Leases, err)
	}
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal("bare memory-only lease lost its independent behavior", err)
	}
}

func TestCPUExecutionRecoveryRejectsPersistedCPUlessQueue(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Memory: 1024}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if m != nil {
			if err := m.Close(); err != nil {
				t.Error("original Manager cleanup", err)
			}
		}
	})
	cpuReading(m, 1, 1, 0, 1, 0)
	holder, err := m.Acquire(request("recovery-cpu-holder"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := request("persisted-cpuless-run")
	r.Queue, r.Resources.Memory = true, 1
	marker := filepath.Join(t.TempDir(), "must-not-run")
	run := &RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/sh", "-c", `printf started > "$1"`, "arm-test", marker}}
	l, err := m.Acquire(r, run)
	if err != nil || l.State != "queued" {
		t.Fatal(l, err)
	}
	// Simulate a durable execution accepted before CPU became mandatory.
	l.Request.Resources.CPU, run.Request.Resources.CPU = 0, 0
	data, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE leases SET data=?,run=? WHERE id=?`, string(data), string(raw), l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(holder.ID, "released"); err != nil {
		t.Fatal(err)
	}
	original := m
	m = nil
	if err = original.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	m.observationMu.Lock()
	m.cpuAvailability.Known = false
	m.observationMu.Unlock()
	m.tick()
	got, err := m.Inspect(l.ID)
	if err != nil || got.Outcome != "rejected" || got.Started != nil || got.Process != nil || got.QueueInfo == nil || got.QueueInfo.Rejection == nil || got.QueueInfo.Rejection.Code != "invalid_request" {
		t.Fatal("persisted CPU-less execution was not durably refused", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("refused command ran", err)
	}
}
