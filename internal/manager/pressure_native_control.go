package manager

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"getarm.org/internal/pressure"
)

var errPressureControlRefused = errors.New("native pressure control authority is unavailable")

// pressureNativeControlInput keeps the ordinary host samples and all warning
// recipients, replacing only the exact original native candidate's governor
// identity. It never assigns a process identity or capability to a recovered
// lease. Caller holds Manager.mu.
func (m *Manager) pressureNativeControlInput(now time.Time, tick time.Duration, candidate pressureNativeCandidate, policy pressure.Policy) (pressure.Input, error) {
	in, _, err := m.hostPressureInput(now, tick)
	if err != nil {
		return in, err
	}
	for i := range in.Jobs {
		if in.Jobs[i].LeaseID != candidate.id.LeaseID {
			continue
		}
		current, ok := m.pressureOriginalNativeCandidate(candidate.id.LeaseID, policy)
		if !ok || current.id != candidate.id || current.job != candidate.job || current.leaseRevision != candidate.leaseRevision {
			return in, errPressureControlRefused
		}
		in.Jobs[i].ExecutionID = candidate.id.ExecutionID
		in.Jobs[i].Generation = candidate.id.Generation
		in.Jobs[i].OwnershipID = candidate.id.OwnershipID
		in.Jobs[i].Supervised = true
		in.Jobs[i].StillOwned = true
		in.Jobs[i].PauseCapability = true
		in.Jobs[i].PauseAllowed = true
		in.Jobs[i].Paused = candidate.job.childOwner.Paused()
		in.Jobs[i].Stopping = candidate.job.originalCancellationRequested
		in.Jobs[i].ExpiresAt = now.Add(current.remaining)
		if in.Jobs[i].Paused {
			// A stopped owner retains its CPU promise but cannot lend possible
			// compliant CPU use to a recovery decision. Require the same fresh
			// physical observation that produced the host-pressure sample;
			// concurrent publication makes this effect fence unknown.
			m.observationMu.RLock()
			cpu, observedTick := m.cpuAvailability, m.cpuPressureTick
			m.observationMu.RUnlock()
			sample := in.Samples[pressure.CPU]
			if cpu.Available == nil || !finiteCPU(*cpu.Available) || *cpu.Available < 0 ||
				!cpu.At.Equal(sample.ObservedAt) || observedTick != sample.ObservedTick {
				sample.Known = false
			} else {
				sample.Available = uint64(math.Floor(cpuWarningProduct(*cpu.Available, 1000, false)))
			}
			in.Samples[pressure.CPU] = sample
		}
		return in, nil
	}
	return in, errPressureControlRefused
}

// applyPressureNativeControl is deliberately disconnected from the warning
// loop. A future caller must persist its action before this method and must
// durably record the exact returned result before feeding it to Advance.
// Unknown effects cannot be retried automatically. The original owner alone
// sends STOP/CONT; this method never uses a stored numeric PID.
func (m *Manager) applyPressureNativeControl(a pressure.Action, policy pressure.Policy) (pressure.ControlResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := pressure.ControlResult{Key: a.Key}
	if a.Kind != pressure.RequestPause && a.Kind != pressure.RequestResume {
		return result, errPressureControlRefused
	}
	candidate, ok := m.pressureOriginalNativeCandidate(a.Job.LeaseID, policy)
	if !ok || candidate.id != a.Job {
		return result, errPressureControlRefused
	}
	var durable []byte
	if err := m.db.QueryRow(`SELECT data FROM host_pressure_state WHERE id='current'`).Scan(&durable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, errPressureControlRefused
		}
		return result, err
	}
	current, err := json.Marshal(m.pressureState)
	if err != nil || !bytes.Equal(durable, current) {
		return result, errPressureControlRefused
	}
	now := time.Now().UTC()
	tick, err := pressureNativeTick()
	if err != nil {
		return result, err
	}
	in, err := m.pressureNativeControlInput(now, tick, candidate, policy)
	if err != nil || !pressure.ControlAllowed(m.pressureState, a, in, policy) ||
		!m.pressureCandidateStillCurrent(candidate, policy) {
		return result, errPressureControlRefused
	}
	if a.Kind == pressure.RequestPause {
		err = candidate.job.childOwner.Pause()
	} else {
		err = candidate.job.childOwner.Resume()
	}
	if err != nil {
		return result, err
	}
	result.Applied = true
	return result, nil
}
