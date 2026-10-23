package manager

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
	"github.com/yrashk/arm/internal/usage"
	"golang.org/x/sys/unix"
)

func (m *Manager) endShareIfNeeded(l model.Lease, outcome string) (bool, model.Lease, error) {
	if !m.shareNonRuntimeLease(l) {
		return false, l, nil
	}
	if err := m.sharePolicyError(); err != nil {
		return true, l, err
	}
	id, ok := m.shares.byLease[l.ID]
	if !ok {
		return true, l, model.Err("share_evidence_unavailable", "this share dependency has no recognized preparation lifetime; obligations remain charged")
	}
	p := m.shares.state.Publications[id]
	if p.Retired != nil {
		return true, l, nil
	}
	if p.WorkerState != "joined" {
		worker := m.shares.workers[id]
		if worker == nil {
			return true, l, model.Err("needs_attention", "preparation supervision is unknown; inspect and reconcile the exact prior native worker without PID signaling or execution retry")
		}
		if p.State != "ending" {
			p.State = "ending"
			p.Revision++
			p.JournalSlots--
			p.Updated = shareMaxTime(p.Updated, time.Now().UTC())
			p.Reason = "Stop requested; CPU/memory and storage pin remain until native preparation joins"
			l.State = "ending"
			l.Outcome = outcome
			l.Revision++
			if err := m.putSharePublication(p, l, "ending", nil); err != nil {
				return true, l, err
			}
		}
		// A private channel is nonblocking. A separate worker watcher invokes
		// context/native cancellation outside Manager.mu before any join.
		worker.requestStop()
		return true, l, nil
	}
	if p.Published != nil {
		// A committed retiring decision survives lost acknowledgment and restart.
		// A later release/cancel/expiry only finishes that exact prior decision.
		if p.State == "retiring" {
			if l.State != "ending" || l.Ended != nil || !validShareEndOutcome(l.Outcome) {
				return true, l, model.Err("share_evidence_unavailable", "retiring publication lacks its exact recorded ordinary end decision; obligations remain charged")
			}
		} else if !validShareEndOutcome(outcome) {
			return true, l, model.Err("invalid_request", "publication retirement requires a recognized ordinary end outcome")
		}

		if err := m.sharePublicationConsumersEmpty(p); err != nil {
			return true, l, err
		}
		if m.shares.workers[id] != nil {
			return true, l, model.Err("operation_busy", "publication supervisor still holds its runtime handle")
		}
		if p.State != "retiring" {
			p.State = "retiring"
			p.Revision++
			p.JournalSlots--
			p.Updated = shareMaxTime(p.Updated, time.Now().UTC())
			p.Reason = "Retirement admitted with positively empty consumers and no native worker handle"
			l.State = "ending"
			l.Outcome = outcome
			l.Revision++
			if err := m.putSharePublication(p, l, "retiring", nil); err != nil {
				return true, l, err
			}
		}
		at := shareMaxTime(p.Updated, time.Now().UTC())
		p.State = "retired"
		p.Revision++
		p.Updated = at
		p.Retired = &at
		p.JournalSlots = 0
		p.Reason = "Published generation retired after joined verifier and positively empty consumers"
		l.State = "terminal"
		// Keep the exact committed retiring outcome, including after recovery.
		l.Ended = &at
		l.Revision++
		if err := m.putSharePublication(p, l, "retired", nil); err != nil {
			return true, l, err
		}
		m.resumeStorageAfterShare(p.Request.Storage)
		return true, l, nil
	}
	now := shareMaxTime(p.Updated, time.Now().UTC())
	p.State = "cancelled"
	p.Revision++
	p.Updated = now
	p.Retired = &now
	p.JournalSlots = 0
	p.Reason = "Private preparation retired after its native worker joined"
	l.State = "terminal"
	l.Outcome = outcome
	l.Ended = &now
	l.Revision++
	if err := m.putSharePublication(p, l, "cancelled", nil); err != nil {
		return true, l, err
	}
	m.resumeStorageAfterShare(p.Request.Storage)
	return true, l, nil
}
func (m *Manager) recoverShares() {
	if m.sharePolicyError() != nil {
		return
	}
	for id, p := range m.shares.state.Publications {
		if p.Retired != nil || p.WorkerState == "joined" || p.State == "needs_attention" && p.WorkerState == "unknown" {
			continue
		}
		l, err := m.get(p.Lease)
		if err != nil {
			m.shares.failure = err
			return
		}
		p.State = "needs_attention"
		p.WorkerState = "unknown"
		p.Revision++
		p.JournalSlots--
		p.Updated = shareMaxTime(p.Updated, time.Now().UTC())
		p.Reason = "Backend restarted; prior native worker has no live handle and was not replayed"
		l.State = "needs_attention"
		l.Outcome = "share_worker_unknown"
		l.Failure = p.Reason
		l.Revision++
		if err = m.putSharePublication(p, l, "needs_attention", nil); err != nil {
			m.shares.failure = err
			return
		}
		m.shares.byLease[p.Lease] = id
	}
	// Any consumer history is read/validated but this private checkpoint does not
	// create or reconstruct runtime streams; no consumer can be settled by PID0.
}

// ReconcileShare proves only old same-namespace worker ABSENCE, never signals or
// restarts it. Exact revision/identity is checked again after off-lock native
// observation. Existing private prepared manifests can be retired via Release.
func (m *Manager) ReconcileShare(ctx context.Context, id string, revision int64, reason string) (sharelease.Publication, error) {
	var empty sharelease.Publication
	if sharelease.ValidateReason(reason) != nil {
		return empty, model.Err("invalid_request", "required reconciliation reason up to4096bytes")
	}
	m.mu.Lock()
	if err := m.sharePolicyError(); err != nil {
		m.mu.Unlock()
		return empty, err
	}
	p, ok := m.shares.state.Publications[id]
	if !ok {
		m.mu.Unlock()
		return empty, model.Err("share_not_found", "preparation not found")
	}
	if p.Revision != revision {
		m.mu.Unlock()
		return clonePublication(p), model.Err("revision_conflict", "preparation changed; inspect before reconciling")
	}
	if p.Retired != nil {
		m.mu.Unlock()
		return clonePublication(p), nil
	}
	if m.shares.workers[id] != nil || p.Published != nil || p.WorkerState == "joined" {
		m.mu.Unlock()
		return clonePublication(p), model.Err("operation_busy", "reconciliation requires an unexposed lost worker; use ordinary release for a joined private preparation")
	}
	control, finish, err := m.beginShareControl(ctx)
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	m.mu.Unlock()
	defer finish()
	ctx = control
	current, err := usage.Capture(os.Getpid())
	if err != nil || current.Namespace != p.Worker.Namespace {
		return clonePublication(p), model.Err("observation_unavailable", "prior worker namespace cannot be observed; no absence or cleanup is inferred")
	}
	actual, probeErr := usage.Capture(p.Worker.PID)
	absent := errors.Is(probeErr, os.ErrNotExist) || errors.Is(probeErr, unix.ESRCH)
	// Darwin libproc deliberately returns an unspecific unavailable observation.
	// Signal0 is a read-only positive-PID existence probe, NEVER a delivered
	// signal or persisted process-group control authority. All other failures hold.
	if probeErr != nil && errors.Is(unix.Kill(p.Worker.PID, 0), unix.ESRCH) {
		absent = true
	}

	if probeErr == nil {
		absent = actual.Namespace == p.Worker.Namespace && actual.Start != p.Worker.Start
	}
	if !absent {
		return clonePublication(p), model.Err("needs_attention", "prior worker process may still be alive or observation is incomplete; no process is signaled and obligations stay charged")
	}
	if err = ctx.Err(); err != nil {
		return clonePublication(p), err
	}
	m.traceShareNative(id, "observed_absent")
	m.traceShareBarrier(ctx, id, "reconcile")
	m.mu.Lock()
	defer m.mu.Unlock()
	now, ok := m.shares.state.Publications[id]
	if m.closing || !ok || now.Revision != p.Revision || now.Worker != p.Worker || m.shares.workers[id] != nil {
		return clonePublication(now), model.Err("revision_conflict", "preparation changed during native absence observation")
	}
	l, err := m.get(p.Lease)
	if err != nil {
		return clonePublication(p), err
	}
	at := shareMaxTime(p.Updated, time.Now().UTC())
	p.State = "cancelled"
	p.WorkerState = "joined"
	p.Revision++
	p.Updated = at
	p.Retired = &at
	p.Reason = reason
	p.JournalSlots = 0
	l.State = "terminal"
	l.Outcome = "interrupted"
	l.Ended = &at
	l.Revision++
	if err = m.putSharePublication(p, l, "cancelled", nil); err != nil {
		return clonePublication(p), err
	}
	m.resumeStorageAfterShare(p.Request.Storage)
	return clonePublication(p), nil
}

// A private share can retire only with an explicit ordinary end decision.
func validShareEndOutcome(outcome string) bool {
	switch outcome {
	case "released", "cancelled", "expired", "revoked", "interrupted", "failed":
		return true
	}
	return false
}
