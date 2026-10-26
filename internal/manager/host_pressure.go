package manager

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/pressure"
)

// The first runtime slice warns and asks for cooperation. CPU uses a coverage
// metric: native idle plus all current CPU promises, capped at effective
// capacity. Compliant managed use cannot create a CPU incident by itself.
// A shortage is an uncovered busy fraction, not proof of an external culprit.
func hostPressurePolicy(cpu model.CPUAvailability, memory model.MemoryAvailability, disk model.DiskAvailability) pressure.Policy {
	cpuLow, cpuHigh := uint64(1), uint64(2)
	if cpu.Capacity != nil && finiteCPU(*cpu.Capacity) && *cpu.Capacity > 0 && *cpu.Capacity <= 65536 {
		cpuLow = max(1, uint64(math.Floor(*cpu.Capacity*750)))
		cpuHigh = max(cpuLow+1, uint64(math.Floor(*cpu.Capacity*900)))
	}
	memLow, memHigh := uint64(1), uint64(2)
	if memory.Total != nil && *memory.Total > 0 {
		memLow = max(1, uint64(*memory.Total)/20)
		memHigh = max(memLow+1, uint64(*memory.Total)/10)
	}
	diskLow, diskHigh := uint64(1), uint64(2)
	if disk.Total != nil && *disk.Total > 0 {
		diskLow = max(1, uint64(*disk.Total)/20)
		diskHigh = max(diskLow+1, uint64(*disk.Total)/10)
	}
	return pressure.Policy{Resources: map[pressure.Resource]pressure.Threshold{
		pressure.CPU:    {EnterBelow: cpuLow, ExitAbove: cpuHigh},
		pressure.Memory: {EnterBelow: memLow, ExitAbove: memHigh},
		pressure.Disk:   {EnterBelow: diskLow, ExitAbove: diskHigh},
	}, MaxAge: 4 * time.Second, MaxSampleGap: 6 * time.Second,
		SustainFor: 6 * time.Second, Grace: 12 * time.Second, WarningEvery: 4 * time.Second,
		RecoverFor: 6 * time.Second, MinPause: 0, MaxPause: 30 * time.Second,
		MaxPauseJobs: 0, MaxJobs: 256, MaxWarnings: 3, PendingTimeout: 12 * time.Second}
}

func (m *Manager) loadHostPressure() error {
	var raw []byte
	err := m.db.QueryRow(`SELECT data FROM host_pressure_state WHERE id='current'`).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &m.pressureState); err != nil {
			return fmt.Errorf("host pressure state is invalid: %w", err)
		}
		if len(m.pressureState.Pauses) != 0 || len(m.pressureState.Unresolved) != 0 {
			return errors.New("host pressure native control obligations require a controller capable of reconciliation")
		}
	}
	// Native CLI delivery may have happened before a crash. The durable inbox
	// remains available; an uncertain attempt is never replayed automatically.
	_, err = m.db.Exec(`UPDATE host_pressure_outbox SET state='unknown' WHERE state='attempting'`)
	return err
}

func hostSample(at time.Time, tick, nowTick time.Duration, now time.Time, epoch string, available uint64, valid bool) pressure.Sample {
	return pressure.Sample{Known: valid && tick >= 0 && tick <= nowTick && !at.IsZero() && !at.After(now),
		ObservedAt: at, ObservedTick: tick, Epoch: epoch, Available: available}
}

// Caller holds m.mu. No PID, capacity credit, or provider signal authority is
// inferred from a native observation or from a persisted lease record.
func (m *Manager) hostPressureInput(now time.Time, tick time.Duration) (pressure.Input, pressure.Policy, error) {
	m.observationMu.RLock()
	cpu, cpuTick := m.cpuAvailability, m.cpuPressureTick
	memory, memoryTick := m.memoryAvailability, m.memoryPressureTick
	disk, diskTick := m.diskAvailability, m.diskPressureTick
	m.observationMu.RUnlock()
	p := hostPressurePolicy(cpu, memory, disk)
	in := pressure.Input{Epoch: m.pressureEpoch, Tick: tick, Now: now, Stopping: m.closing || m.draining,
		Samples: map[pressure.Resource]pressure.Sample{}}
	if memory.Available != nil && memory.Total != nil && *memory.Total >= 20 && *memory.Available >= 0 && *memory.Available <= *memory.Total {
		in.Samples[pressure.Memory] = hostSample(memory.At, memoryTick, tick, now, m.pressureEpoch, uint64(*memory.Available), true)
	}
	if disk.Available != nil && disk.Total != nil && *disk.Total >= 20 && *disk.Available >= 0 && *disk.Available <= *disk.Total && disk.Volume != "" {
		in.Samples[pressure.Disk] = hostSample(disk.At, diskTick, tick, now, m.pressureEpoch, uint64(*disk.Available), true)
	}
	ls, err := m.list()
	if err != nil {
		return in, p, err
	}
	promisedCPU := 0.0
	validPromises := true
	for _, l := range ls {
		if !consuming(l.State) || l.Expires == nil || !l.Expires.After(now) || l.ID == "" {
			continue
		}
		if len(in.Jobs) == p.MaxJobs {
			return in, p, errors.New("host pressure roster exceeds bounded governor capacity")
		}
		in.Jobs = append(in.Jobs, pressure.Job{LeaseID: l.ID, ExecutionID: l.ID, Generation: "warning-only", OwnershipID: m.host,
			AgentID: l.Request.Agent, StillOwned: l.Host == m.host && l.Owner == m.owner, ExpiresAt: *l.Expires,
			Supervised: m.jobs[l.ID] != nil, PauseCapability: false, PauseAllowed: false, Stopping: l.State == "ending"})
		if !finiteCPU(l.Request.Resources.CPU) || l.Request.Resources.CPU < 0 {
			validPromises = false
		} else {
			promisedCPU += l.Request.Resources.CPU
		}
	}
	if validPromises && finiteCPU(promisedCPU) && cpu.Known && cpu.Available != nil && cpu.Capacity != nil && cpu.EligibleCPUs != nil && *cpu.EligibleCPUs > 0 &&
		finiteCPU(*cpu.Available) && finiteCPU(*cpu.Capacity) && *cpu.Available >= 0 && *cpu.Available <= *cpu.Capacity && *cpu.Capacity >= 0.002 &&
		cpu.Interval >= 100*time.Millisecond && cpu.Interval <= 6*time.Second {
		covered := min(*cpu.Capacity, *cpu.Available+promisedCPU)
		in.Samples[pressure.CPU] = hostSample(cpu.At, cpuTick, tick, now, m.pressureEpoch, uint64(math.Floor(covered*1000)), true)
	}
	return in, p, nil
}

func (m *Manager) hostPressureLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			err := m.advanceHostPressure()
			m.mu.Lock()
			m.pressureLastError = ""
			if err != nil {
				m.pressureLastError = err.Error()
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) advanceHostPressure() error {
	now := time.Now().UTC()
	tick, err := pressureNativeTick()
	if err != nil {
		return err
	}
	return m.advanceHostPressureAt(now, tick)
}

func (m *Manager) advanceHostPressureAt(now time.Time, tick time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.draining {
		return nil
	}
	in, p, err := m.hostPressureInput(now, tick)
	if err != nil {
		return err
	}
	next, actions, err := pressure.Advance(m.pressureState, in, p)
	if err != nil {
		return err
	}
	for _, a := range actions {
		if a.Kind != pressure.WarnOperator && a.Kind != pressure.WarnAgent && a.Kind != pressure.RequestCooperation {
			return fmt.Errorf("host pressure warning runtime refused native action %s", a.Kind)
		}
	}
	contacts := map[string]*agents.Contact{}
	for _, a := range actions {
		if a.Kind != pressure.WarnAgent {
			continue
		}
		if _, checked := contacts[a.AgentID]; checked {
			continue
		}
		c, contactErr := m.contact(a.AgentID)
		if errors.Is(contactErr, sql.ErrNoRows) {
			contacts[a.AgentID] = nil
			continue
		}
		if contactErr != nil {
			return contactErr
		}
		contacts[a.AgentID] = &c
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO host_pressure_state(id,data) VALUES('current',?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, string(raw)); err != nil {
		return err
	}
	created := false
	for _, a := range actions {
		actionID := fmt.Sprintf("%x", sha256.Sum256([]byte(a.Key)))
		if err = event(tx, "host_pressure."+string(a.Kind), actionID, a); err != nil {
			return err
		}
		if a.Kind == pressure.RequestCooperation {
			continue // The matching agent warning already includes the request.
		}
		metric := "native available headroom"
		explanation := ""
		if a.Reason == pressure.CPU {
			metric = "covered CPU headroom"
			explanation = " CPU coverage is native idle plus current CPU promises, capped by effective capacity."
		}
		body := fmt.Sprintf("Host %s pressure: %s is %d %s; warning sequence %d, incident %d.%s This is host pressure and does not assert a lease violation or identify a culprit.", a.Reason, metric, a.Sample.Available, pressureUnit(a.Reason), a.Warning, a.Incident, explanation)
		msg := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "arm-host-pressure", Created: now,
			Body: body, Deliveries: map[string]agents.Delivery{}}
		var target []agents.Contact
		if a.Kind == pressure.WarnOperator {
			msg.Audience = "operator"
		} else {
			c := contacts[a.AgentID]
			if c == nil {
				if err = event(tx, "host_pressure.contact_missing", actionID, a); err != nil {
					return err
				}
				continue
			}
			msg.Audience, msg.Lease = c.ID, a.Job.LeaseID
			msg.Body += " Please checkpoint, reduce resource use, or release capacity when safe; ARM has not paused this execution."
			state := "pending"
			if c.Delivery == "poll" {
				state = "available"
			}
			msg.Deliveries[c.ID] = agents.Delivery{State: state, Mode: c.Delivery, Registration: c.Registration}
			target = []agents.Contact{*c}
		}
		if err = insertMessage(tx, msg, target); err != nil {
			return err
		}
		created = created || len(target) > 0
		if len(target) > 0 && target[0].Delivery != "poll" {
			item := hostPressureDelivery{Target: target[0], Epoch: a.Epoch, Reason: a.Reason, Incident: a.Incident, Lease: a.Job.LeaseID}
			b, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				return marshalErr
			}
			if _, err = tx.Exec(`INSERT INTO host_pressure_outbox(message,state,data) VALUES(?,'queued',?)`, msg.ID, string(b)); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	m.pressureState = next
	if created {
		m.notifyInbox()
	}
	return nil
}

func pressureUnit(r pressure.Resource) string {
	if r == pressure.CPU {
		return "millicores"
	}
	return "bytes"
}
