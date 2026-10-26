package observe

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const CPUObservationCadence = 2 * time.Second
const CPUObservationMaxGap = 3 * CPUObservationCadence
const cpuObservationMinInterval = 100 * time.Millisecond
const cpuObservationMaxCPUs = 8192

// CPUSampler has its own complete native CPU collector; the monitor's bounded
// display inventory and its treatment of I/O wait are deliberately independent.
// Sample never sleeps to obtain an interval. The caller supplies the cadence.
type CPUSampler struct {
	mu       sync.Mutex
	previous cpuAvailabilityRaw
	collect  func(context.Context) (cpuAvailabilityRaw, error)
}

type cpuAvailabilityRaw struct {
	at                    time.Time    // retain the monotonic component for interval calculation
	rows                  []cpuCounter // only eligible CPUs, in stable ID order
	capacity              float64
	signature             string // inventory, affinity and quota identity/value
	scope, method, detail string
	quotas                []cpuQuotaCounter
}

type cpuQuotaCounter struct {
	id             string
	capacity       float64
	used           uint64    // nanoseconds of CPU used by the exact ancestor scope
	started, ended time.Time // bounds of the actual native counter read
}

func NewCPUSampler() *CPUSampler { return &CPUSampler{collect: collectCPUAvailability} }

func (s *CPUSampler) Sample(ctx context.Context) model.CPUAvailability {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.collect(ctx)
	if err != nil {
		s.previous = cpuAvailabilityRaw{}
		return model.CPUAvailability{At: time.Now().UTC(), Scope: raw.scope, Method: raw.method, Quality: "unknown", Detail: err.Error()}
	}
	before := s.previous
	s.previous = raw
	return cpuAvailabilityBetween(before, raw)
}

func cpuAvailabilityBetween(before, after cpuAvailabilityRaw) model.CPUAvailability {
	n, capacity := len(after.rows), after.capacity
	r := model.CPUAvailability{At: after.at.UTC(), EligibleCPUs: &n, Capacity: &capacity, Scope: after.scope, Method: after.method + ":counter-delta", Quality: "unknown", Detail: after.detail}
	unknown := func(reason string) model.CPUAvailability { r.Detail = reason; return r }
	if validateCPURows(after.rows) != nil || math.IsNaN(capacity) || math.IsInf(capacity, 0) || capacity <= 0 || capacity > float64(n) {
		r.Capacity = nil
		if n == 0 || n > cpuObservationMaxCPUs {
			r.EligibleCPUs = nil
		}
		return unknown("effective CPU inventory or capacity is unavailable")
	}
	if before.at.IsZero() {
		return unknown("waiting for a second native CPU sample")
	}
	dt := after.at.Sub(before.at)
	r.Interval = dt
	if !cpuIntervalReliable(dt, after.at.UTC().Sub(before.at.UTC()), cpuObservationMinInterval) {
		return unknown("CPU sample interval is too short or stale; waiting for a fresh interval")
	}
	if before.signature != after.signature || before.capacity != after.capacity || len(before.rows) != n || len(before.quotas) != len(after.quotas) {
		return unknown("CPU eligibility, topology or limits changed; waiting for a stable interval")
	}
	busy := 0.0
	for i, current := range after.rows {
		old := before.rows[i]
		if old.id != current.id || !cpuMonotone(current, old) || current.busy < old.busy || current.idle < old.idle {
			return unknown("CPU counters reset, regressed or changed identity; waiting for a fresh interval")
		}
		used, idle := current.busy-old.busy, current.idle-old.idle
		total, err := checkedAdd(used, idle)
		if err != nil || total == 0 {
			return unknown("CPU counter interval has no reliable progress")
		}
		busy += float64(used) / float64(total)
	}
	// Native affinity/cpuset idle and cgroup ancestor usage are different scopes.
	// Each finite ancestor includes all its descendants, including siblings that
	// run outside this process's affinity. Never substitute eligible-core busy
	// for that ancestor's usage.
	available := float64(n) - busy
	for i, quota := range after.quotas {
		old := before.quotas[i]
		if quota.id != old.id || quota.capacity != old.capacity || quota.used < old.used {
			return unknown("CPU quota scope or usage counters changed; waiting for a fresh interval")
		}
		// The native read occurs between started and ended. Using the minimum
		// possible elapsed interval avoids overstating quota headroom because
		// file-read latency made its usage window shorter than the CPU window.
		quotaInterval := quota.started.Sub(old.ended)
		if !cpuIntervalReliable(quotaInterval, quota.started.UTC().Sub(old.ended.UTC()), time.Nanosecond) || quota.ended.Before(quota.started) || old.ended.Before(old.started) {
			return unknown("CPU ancestor usage interval is unavailable or stale")
		}
		used := float64(quota.used-old.used) / float64(quotaInterval)
		available = math.Min(available, math.Max(0, quota.capacity-used))
	}
	available = math.Max(0, math.Min(capacity, available))
	r.Known, r.Busy, r.Available, r.Quality = true, &busy, &available, "estimated"
	return r
}

func validateCPURows(rows []cpuCounter) error {
	seen := map[string]bool{}
	for _, c := range rows {
		if c.id == "" || seen[c.id] {
			return fmt.Errorf("duplicate or missing native CPU identity")
		}
		seen[c.id] = true
	}
	if len(rows) == 0 || len(rows) > cpuObservationMaxCPUs {
		return fmt.Errorf("complete native CPU inventory unavailable")
	}
	return nil
}

// Go's monotonic clock may exclude suspend on some platforms. A recent wall
// timestamp alone must not relabel a pre-suspend counter baseline as fresh.
// Wall jumps also make the interval uncertain, so disagreement fails closed.
func cpuIntervalReliable(elapsed, wall, minimum time.Duration) bool {
	if elapsed < minimum || elapsed > CPUObservationMaxGap || wall < minimum || wall > CPUObservationMaxGap {
		return false
	}
	difference := elapsed - wall
	return difference >= -100*time.Millisecond && difference <= 100*time.Millisecond
}
