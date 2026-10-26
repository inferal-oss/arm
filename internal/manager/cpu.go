package manager

import (
	"context"
	"math"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
)

const cpuStaleAfter = 3 * observe.CPUObservationCadence

type cpuObserver interface {
	Sample(context.Context) model.CPUAvailability
}

// Sampling performs native work outside the admission lock. Its timestamp and
// the native UTC timestamp are both retained so suspension or a clock change
// cannot extend the lifetime of cached admission evidence.
func (m *Manager) sampleCPU() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	at := time.Now()
	reading := m.cpuObserver.Sample(ctx)
	m.observationMu.Lock()
	m.cpuAvailability, m.cpuObserved = reading, at
	m.observationMu.Unlock()
}

// Do not publish an immediately usable backend from a since-boot CPU average.
// Obtain a short real interval first. Unsupported native collection remains
// unknown; the periodic sampler can recover it later without blocking startup.
func (m *Manager) initializeCPU() {
	m.sampleCPU()
	m.observationMu.RLock()
	reading := m.cpuAvailability
	m.observationMu.RUnlock()
	if reading.Known || reading.EligibleCPUs == nil || reading.Capacity == nil {
		return
	}
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	m.sampleCPU()
}

func finiteCPU(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Caller holds m.mu. The initial protection deliberately grants no credit for
// unrelated-window process samples. Full promises remain charged even when
// their workload contributes to observed activity. Aligned, verified managed
// consumption is required before any such credit can be introduced.
func (m *Manager) cpuBudget(reserved float64) model.CPUBudget {
	m.observationMu.RLock()
	reading, at := m.cpuAvailability, m.cpuObserved
	m.observationMu.RUnlock()
	age, wallAge := time.Since(at), time.Since(reading.At)
	ceiling := m.capacity.CPU
	r := model.CPUBudget{Observation: reading, Ceiling: &ceiling, Reserved: reserved, Age: max(age, wallAge), StaleAfter: cpuStaleAfter,
		Accounting: "Current native idle headroom, capped by the configured ceiling; full outstanding CPU promises remain charged. Managed usage is not credited without aligned verification and may be conservatively counted twice."}
	r.Stale = at.IsZero() || reading.At.IsZero() || age < 0 || wallAge < 0 || r.Age > cpuStaleAfter
	valid := reading.Known && !r.Stale && reading.Interval >= 100*time.Millisecond && reading.Interval <= observe.CPUObservationMaxGap &&
		reading.EligibleCPUs != nil && *reading.EligibleCPUs > 0 && reading.Capacity != nil && finiteCPU(*reading.Capacity) && *reading.Capacity > 0 && *reading.Capacity <= float64(*reading.EligibleCPUs) &&
		reading.Available != nil && finiteCPU(*reading.Available) && *reading.Available >= 0 && *reading.Available <= *reading.Capacity &&
		reading.Busy != nil && finiteCPU(*reading.Busy) && *reading.Busy >= 0 && *reading.Busy <= float64(*reading.EligibleCPUs) && finiteCPU(ceiling) && ceiling >= 0 && finiteCPU(reserved) && reserved >= 0
	if valid {
		r.Known = true
		r.Capacity = min(ceiling, *reading.Available)
	}
	r.Available = max(0, r.Capacity-reserved)
	r.Deficit = max(0, reserved-r.Capacity)
	traceCPUBudget(m, r)
	return r
}

func (m *Manager) startCPUSampling() {
	m.wg.Go(func() {
		ticker := time.NewTicker(observe.CPUObservationCadence)
		defer ticker.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-ticker.C:
				m.sampleCPU()
			}
		}
	})
}
