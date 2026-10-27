package manager

import (
	"context"
	"math"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/usage"
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
	warningOwners := m.captureCPUWarningOwners()
	var reading model.CPUAvailability
	var frame observe.CPUCreditFrame
	var credits []cpuCreditSample
	if observer, ok := m.cpuObserver.(cpuManagedObserver); ok {
		owners, jobs := m.cpuOwners()
		reading, frame = observer.SampleManaged(ctx, func(ctx context.Context) usage.CPUCut { return usage.CollectCPU(ctx, owners) })
		credits = bindCPUCredits(frame, jobs)
	} else {
		reading = m.cpuObserver.Sample(ctx)
	}
	m.observationMu.Lock()
	m.cpuAvailability, m.cpuObserved = reading, at
	m.cpuPressureTick, _ = pressureNativeTick()
	m.cpuCredits, m.cpuCreditFrame = credits, frame
	m.cpuWarningOwners = warningOwners
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

// Caller holds m.mu. Full promises remain charged. Only bounded, contained
// native usage from the exact currently owned jobs can offset double charging.
func (m *Manager) cpuBudget(reserved float64) model.CPUBudget {
	m.observationMu.RLock()
	reading, at := m.cpuAvailability, m.cpuObserved
	credits := m.cpuCredits
	m.observationMu.RUnlock()
	age, wallAge := time.Since(at), time.Since(reading.At)
	ceiling := m.capacity.CPU
	r := model.CPUBudget{Observation: reading, Ceiling: &ceiling, Reserved: reserved, Age: max(age, wallAge), StaleAfter: cpuStaleAfter,
		Accounting: "Current native idle headroom plus conservative aligned usage from currently owned jobs, capped by effective capacity and the configured ceiling. Full outstanding promises remain charged. Unsupported accounting scopes earn no credit; unverified or unobserved work may still be counted twice."}
	r.Stale = at.IsZero() || reading.At.IsZero() || age < 0 || wallAge < 0 || r.Age > cpuStaleAfter
	valid := reading.Known && !r.Stale && reading.Interval >= 100*time.Millisecond && reading.Interval <= observe.CPUObservationMaxGap &&
		reading.EligibleCPUs != nil && *reading.EligibleCPUs > 0 && reading.Capacity != nil && finiteCPU(*reading.Capacity) && *reading.Capacity > 0 && *reading.Capacity <= float64(*reading.EligibleCPUs) &&
		reading.Available != nil && finiteCPU(*reading.Available) && *reading.Available >= 0 && *reading.Available <= *reading.Capacity &&
		reading.Busy != nil && finiteCPU(*reading.Busy) && *reading.Busy >= 0 && *reading.Busy <= float64(*reading.EligibleCPUs) && finiteCPU(ceiling) && ceiling >= 0 && finiteCPU(reserved) && reserved >= 0
	if valid {
		r.Known = true
		r.ManagedCredit = m.currentCPUCredit(credits, reserved, *reading.Busy)
		r.Capacity = min(ceiling, *reading.Capacity, *reading.Available+r.ManagedCredit)
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
