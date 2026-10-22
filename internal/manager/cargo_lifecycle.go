package manager

import (
	"errors"
	"syscall"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func (m *Manager) cargoNoSpawnFailure(l model.Lease, rec cargoRecord, reason error) (model.Lease, error) {
	now := time.Now().UTC()
	rec.Phase = "finished"
	rec.Worker = ""
	rec.NoSpawn = true
	rec.NoPreflight = true
	rec.GroupAbsent = true
	rec.FinalAt = &now
	rec.Error = reason.Error()
	rec.Revision++
	l.State = "terminal"
	l.Outcome = "launch_failed"
	l.Failure = reason.Error()
	l.Ended = &now
	l.Revision++
	if e := m.putCargo(l, rec, "cargo.launch_failed"); e != nil {
		return l, e
	}
	return m.get(l.ID)
}
func (m *Manager) cargoRejectUnstarted(l model.Lease, reason *model.Error) (model.Lease, error) {
	wall, _ := m.schedulingTime()
	l.QueueInfo = rejectedQueueInfo(wall, reason)
	l.Failure = reason.Message
	delete(m.startClocks, l.ID)
	rec, e := m.getCargo(l.ID)
	if e != nil {
		return l, e
	}
	if m.cargoWorkers[l.ID] != nil {
		l.Failure = reason.Message
		if e = m.put(l, "cargo.start_rejected"); e != nil {
			return l, e
		}
		return m.endCargo(l, "rejected")
	}
	if rec.Phase != "admitted" {
		return l, model.Err("needs_attention", "Cargo spawn intent cannot be inferred unstarted from PID absence")
	}
	l, e = m.cargoNoSpawnFailure(l, rec, reason)
	if e != nil {
		return l, e
	}
	l.Outcome = "rejected"
	return l, m.put(l, "lease.rejected")
}
func (m *Manager) endCargo(l model.Lease, outcome string) (model.Lease, error) {
	if l.State == "terminal" {
		return l, nil
	}
	rec, e := m.getCargo(l.ID)
	if e != nil {
		return l, model.Err("needs_attention", "Cargo durability record unavailable; obligations remain charged")
	}
	if worker := m.cargoWorkers[l.ID]; worker != nil {
		if l.State != "ending" {
			l.State = "ending"
			l.Outcome = outcome
			l.Revision++
			rec.Revision++
			if e = m.putCargo(l, rec, "cargo.ending"); e != nil {
				return l, e
			}
		}
		worker.cancel()
		return m.get(l.ID)
	}
	if rec.Phase != "admitted" || rec.Worker != "" {
		return l, model.Err("needs_attention", "Cargo supervision is lost; inspect and explicitly reconcile verified group absence, without execution retry")
	}
	now := time.Now().UTC()
	rec.Phase = "finished"
	rec.NoSpawn, rec.GroupAbsent = true, true
	rec.NoPreflight = true
	rec.FinalAt = &now
	rec.Revision++
	l.State = "terminal"
	l.Outcome = outcome
	l.Ended = &now
	l.Revision++
	if e = m.putCargo(l, rec, "cargo."+outcome); e != nil {
		return l, e
	}
	return m.get(l.ID)
}
func (m *Manager) recoverCargo(l model.Lease) error {
	rec, e := m.getCargo(l.ID)
	if e != nil {
		return e
	}
	if l.State == "terminal" {
		if rec.Phase != "finished" || rec.Uncertain || !rec.GroupAbsent || rec.Worker != "" {
			return model.Err("cargo_state_corrupt", "terminal Cargo lease has unfinished durable obligations")
		}
		return nil
	}
	if l.State == "queued" && rec.Phase == "admitted" && rec.Worker == "" {
		return nil
	}
	if rec.Phase == "admitted" && rec.Worker == "" {
		_, e = m.cargoNoSpawnFailure(l, rec, model.Err("interrupted", "backend stopped before a Cargo spawn intent existed"))
		return e
	}
	rec.Phase = "needs_attention"
	rec.Uncertain = true
	rec.Revision++
	l.State = "needs_attention"
	l.Outcome = "cargo_supervision_lost"
	l.Ended = nil
	l.Failure = "Cargo helper/compile is never restarted automatically. Verify saved process-group absence before reconciliation; PID0 is not absence proof."
	l.Revision++
	return m.putCargo(l, rec, "cargo.recovery_required")
}
func (m *Manager) reconcileCargo(l model.Lease) (model.Lease, error) {
	if m.cargoWorkers[l.ID] != nil {
		return l, model.Err("operation_busy", "Cargo has a current live worker; wait for supervised termination")
	}
	rec, e := m.getCargo(l.ID)
	if e != nil {
		return l, e
	}
	if l.State != "needs_attention" || rec.Process == nil || rec.Process.PID <= 0 || rec.Process.PGID != rec.Process.PID {
		return l, model.Err("needs_attention", "Cargo has no durably verified native process group; unknown spawn responsibility remains charged")
	}
	if !errors.Is(syscall.Kill(-rec.Process.PGID, 0), syscall.ESRCH) {
		return l, model.Err("needs_attention", "saved Cargo process group exists or cannot be inspected; no signal or execution retry was sent")
	}
	now := time.Now().UTC()
	rec.Phase = "finished"
	rec.Uncertain = false
	rec.Worker = ""
	rec.GroupAbsent = true
	rec.FinalAt = &now
	rec.Revision++
	l.State = "terminal"
	l.Outcome = "reconciled"
	l.Ended = &now
	l.Revision++
	if e = m.putCargo(l, rec, "cargo.reconciled"); e != nil {
		return l, e
	}
	return m.get(l.ID)
}
