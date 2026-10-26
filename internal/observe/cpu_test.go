package observe

import (
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func cpuFixture(at time.Time, busy, idle uint64) cpuAvailabilityRaw {
	return cpuAvailabilityRaw{at: at, capacity: 1, signature: "same", rows: []cpuCounter{{id: "cpu0", busy: busy, idle: idle, parts: [8]uint64{busy, idle}}}}
}
func TestCPUAvailabilityIntervalAndExternalWork(t *testing.T) {
	at := time.Now()
	before := cpuFixture(at, 10, 90)
	after := cpuFixture(at.Add(time.Second), 70, 130)
	r := cpuAvailabilityBetween(before, after)
	if !r.Known || r.Available == nil || math.Abs(*r.Available-.4) > 1e-9 || *r.Busy != .6 || *r.Capacity != 1 || *r.EligibleCPUs != 1 {
		t.Fatalf("external busy time did not reduce headroom: %+v", r)
	}
	idle := cpuFixture(at.Add(time.Second), 10, 190)
	if r := cpuAvailabilityBetween(before, idle); !r.Known || *r.Available != 1 {
		t.Fatal(r)
	}
	for _, mutate := range []struct {
		name string
		f    func(*cpuAvailabilityRaw)
	}{
		{"reset", func(r *cpuAvailabilityRaw) { r.rows[0].parts[0] = 1 }},
		{"identity", func(r *cpuAvailabilityRaw) { r.rows[0].id = "cpu1" }},
		{"topology", func(r *cpuAvailabilityRaw) { r.signature = "changed" }},
		{"limit", func(r *cpuAvailabilityRaw) { r.capacity = .5 }},
		{"short", func(r *cpuAvailabilityRaw) { r.at = at.Add(time.Millisecond) }},
		{"gap", func(r *cpuAvailabilityRaw) { r.at = at.Add(CPUObservationMaxGap + time.Nanosecond) }},
		{"clock", func(r *cpuAvailabilityRaw) { r.at = at.Add(-time.Second) }},
		{"no-progress", func(r *cpuAvailabilityRaw) { r.rows[0] = before.rows[0] }},
		{"nan", func(r *cpuAvailabilityRaw) { r.capacity = math.NaN() }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			bad := after
			bad.rows = append([]cpuCounter{}, after.rows...)
			mutate.f(&bad)
			r := cpuAvailabilityBetween(before, bad)
			if r.Known || r.Available != nil || r.Busy != nil {
				t.Fatalf("unreliable interval became known: %+v", r)
			}
		})
	}
	if r := cpuAvailabilityBetween(cpuAvailabilityRaw{}, after); r.Known || r.Available != nil {
		t.Fatal("first observation supplied headroom", r)
	}
}
func TestCPUAncestorUsageIncludesSiblingOutsideAffinity(t *testing.T) {
	at := time.Now()
	before := cpuFixture(at, 0, 100)
	after := cpuFixture(at.Add(time.Second), 0, 200)
	before.capacity, after.capacity = .5, .5
	before.quotas = []cpuQuotaCounter{{id: "parent", capacity: .5, used: 100, started: at, ended: at}}
	after.quotas = []cpuQuotaCounter{{id: "parent", capacity: .5, used: 500000100, started: at.Add(time.Second), ended: at.Add(time.Second)}}
	r := cpuAvailabilityBetween(before, after)
	if !r.Known || *r.Available != 0 || *r.Busy != 0 {
		t.Fatalf("idle eligible CPU hid sibling quota consumption: %+v", r)
	}
	after.quotas[0].used = 250000100
	if r := cpuAvailabilityBetween(before, after); !r.Known || *r.Available != .25 {
		t.Fatal(r)
	}
	// A tighter parent remains binding even if a child's own usage is zero.
	before.quotas = append(before.quotas, cpuQuotaCounter{id: "child", capacity: .5, started: at, ended: at})
	after.quotas = append(after.quotas, cpuQuotaCounter{id: "child", capacity: .5, started: at.Add(time.Second), ended: at.Add(time.Second)})
	if r := cpuAvailabilityBetween(before, after); !r.Known || *r.Available != .25 {
		t.Fatal(r)
	}
	for _, kind := range []string{"regressed", "replaced", "limit", "missing", "overlap", "stale"} {
		t.Run(kind, func(t *testing.T) {
			bad := after
			bad.quotas = append([]cpuQuotaCounter{}, after.quotas...)
			switch kind {
			case "regressed":
				bad.quotas[0].used = 0
			case "replaced":
				bad.quotas[0].id = "new"
			case "limit":
				bad.quotas[0].capacity = .25
			case "missing":
				bad.quotas = bad.quotas[:1]
			case "overlap":
				bad.quotas[0].started = at
			case "stale":
				bad.quotas[0].started = at.Add(time.Hour)
			}
			r := cpuAvailabilityBetween(before, bad)
			if r.Known || r.Available != nil {
				t.Fatal("bad quota interval accepted", r)
			}
		})
	}
}
func TestCPUSamplerFailureBreaksInterval(t *testing.T) {
	at := time.Now()
	step := 0
	s := &CPUSampler{collect: func(context.Context) (cpuAvailabilityRaw, error) {
		step++
		if step == 2 {
			return cpuAvailabilityRaw{}, errors.New("native scope unavailable")
		}
		return cpuFixture(at.Add(time.Duration(step)*time.Second), uint64(step), uint64(step*10)), nil
	}}
	for i := 0; i < 3; i++ {
		r := s.Sample(t.Context())
		if r.Known || r.Available != nil {
			t.Fatal("failed or incomplete native pair supplied capacity", i, r)
		}
	}
	if r := s.Sample(t.Context()); !r.Known {
		t.Fatal("fresh pair did not recover", r)
	}
}

// The optional native check takes three short intervals and at most one busy
// goroutine. It checks real counters and records controlled process CPU usage;
// the pure controls above assert the deterministic headroom relationship.
func TestNativeCPUAvailability(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU") == "" {
		t.Skip("enable bounded native CPU observation")
	}
	s := NewCPUSampler()
	first := s.Sample(t.Context())
	if first.Known || first.Available != nil {
		t.Fatal("first native sample invented an interval", first)
	}
	time.Sleep(250 * time.Millisecond)
	idle := s.Sample(t.Context())
	if !idle.Known || idle.Available == nil || idle.Capacity == nil {
		t.Fatalf("native CPU pair unavailable: %+v", idle)
	}
	var beforeUsage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &beforeUsage); err != nil {
		t.Fatal(err)
	}
	loadStarted := time.Now()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var n uint64
		for {
			select {
			case <-stop:
				runtime.KeepAlive(n)
				return
			default:
			}
			for i := 0; i < 10000; i++ {
				n = n*1664525 + 1013904223
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	busy := s.Sample(t.Context())
	close(stop)
	<-done
	var afterUsage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &afterUsage); err != nil {
		t.Fatal(err)
	}
	cpuDuration := time.Duration(afterUsage.Utime.Nano() + afterUsage.Stime.Nano() - beforeUsage.Utime.Nano() - beforeUsage.Stime.Nano())
	if cpuDuration <= 0 {
		t.Fatal("controlled native worker consumed no measurable process CPU")
	}
	t.Logf("controlled process CPU=%s over %s (one busy goroutine)", cpuDuration, time.Since(loadStarted))
	if !busy.Known || busy.Busy == nil || busy.Available == nil || *busy.Available < 0 || *busy.Available > *busy.Capacity || *busy.Busy < 0 || *busy.Busy > float64(*busy.EligibleCPUs) {
		t.Fatalf("native loaded interval unavailable or invalid: %+v", busy)
	}
	t.Logf("native eligible=%d capacity=%.3f idle-before=%.3f idle-during=%.3f busy-during=%.3f scope=%s", *busy.EligibleCPUs, *busy.Capacity, *idle.Available, *busy.Available, *busy.Busy, busy.Scope)
}

func TestCPUIntervalRejectsSuspendAndWallDiscontinuity(t *testing.T) {
	for _, p := range [][2]time.Duration{{2 * time.Second, time.Hour + 2*time.Second}, {2 * time.Second, -time.Second}, {2 * time.Second, 3 * time.Second}, {time.Hour, 2 * time.Second}, {0, time.Second}} {
		if cpuIntervalReliable(p[0], p[1], cpuObservationMinInterval) {
			t.Fatal("suspend/clock discontinuity accepted", p)
		}
	}
	if !cpuIntervalReliable(2*time.Second, 2*time.Second+time.Millisecond, cpuObservationMinInterval) {
		t.Fatal("ordinary paired clocks rejected")
	}
}
