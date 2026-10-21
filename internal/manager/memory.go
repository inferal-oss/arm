package manager

import (
	"context"
	"math"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
)

const memoryStaleAfter = 3 * time.Second

func (m *Manager) sampleMemory() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	at := time.Now()
	reading := observe.MemoryAvailability(ctx)
	m.observationMu.Lock()
	m.memoryAvailability = reading
	m.memoryObserved = at
	m.observationMu.Unlock()
}

// Caller holds m.mu; source collection never holds the admission lock.
func (m *Manager) memoryBudget(reserved int64) model.MemoryBudget {
	m.observationMu.RLock()
	reading, at := m.memoryAvailability, m.memoryObserved
	m.observationMu.RUnlock()
	age := time.Since(at)
	// Go's monotonic clock need not advance while the machine is suspended.
	// The native reading's UTC timestamp has no monotonic component, so also
	// require wall-clock freshness. A clock change conservatively pauses memory
	// admission until the next sample instead of extending an old observation.
	wallAge := time.Since(reading.At)
	r := model.MemoryBudget{Observation: reading, Reserved: reserved, Age: age, StaleAfter: memoryStaleAfter, Accounting: "Full outstanding memory promises are charged conservatively; shared/compressed workload footprints are not credited as exclusive resident bytes"}
	r.Age = max(age, wallAge)
	r.Stale = at.IsZero() || reading.At.IsZero() || age < 0 || wallAge < 0 || r.Age > memoryStaleAfter
	if m.capacity.Memory > 0 {
		ceiling := m.capacity.Memory
		r.Ceiling = &ceiling
	}
	if !r.Stale && reading.Available != nil && *reading.Available >= 0 && reading.Total != nil && *reading.Total > 0 && *reading.Available <= *reading.Total {
		r.Capacity = *reading.Available
		if r.Ceiling != nil {
			r.Capacity = min(r.Capacity, *r.Ceiling)
		}
	}
	r.Available = max(0, r.Capacity-reserved)
	r.Deficit = max(0, reserved-r.Capacity)
	return r
}

func (m *Manager) requestFitsCeiling(r model.Resources) bool {
	capacity := m.capacity
	if capacity.Memory == 0 {
		capacity.Memory = math.MaxInt64
	}
	// A moving headroom shortage can recover; a request larger than the host's
	// known physical RAM cannot. Reject it rather than leave it queued forever.
	// Unknown/stale inventory still uses the configured ceiling and fails live
	// admission separately, without guessing the host's possible capacity.
	budget := m.memoryBudget(0)
	if !budget.Stale && budget.Observation.Total != nil && *budget.Observation.Total > 0 {
		capacity.Memory = min(capacity.Memory, *budget.Observation.Total)
	}
	return r.Fits(capacity)
}

func (m *Manager) fitsLiveMemory(r, used model.Resources) bool {
	capacity := m.capacity
	capacity.Memory = math.MaxInt64
	if !r.FitsRemaining(used, capacity) {
		return false
	}
	if r.Memory == 0 {
		return true
	}
	budget := m.memoryBudget(used.Memory)
	return r.Memory <= budget.Available
}
