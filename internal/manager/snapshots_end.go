package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
)

// A key fence is durable even before optional snapshot admission exists. It is
// never synthesized in memory from a failed/unknown SQL acknowledgment.
func (m *Manager) cancelSnapshotReservation(ctx context.Context, key string, revision int64, reason string) (resultErr error) {
	defer func() {
		m.traceSnapshotMethod("cancel-call-return", snapshotTraceCut{Action: "cancel", Key: key, Revision: revision, Reason: reason, APIError: snapshotTraceError(resultErr)})
	}()
	if len(key) == 0 || len(key) > 128 || len(reason) == 0 || len(reason) > 4096 || revision < 0 {
		return model.Err("invalid_request", "exact bounded cancellation required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.snapshotPolicyError(); e != nil {
		return e
	}
	for _, r := range m.snapshots.state.Copies {
		if r.Key == key {
			if revision != 1 {
				return model.Err("revision_conflict", "original no-work reservation revision required")
			}
			if r.Phase == "cancelled-no-work" {
				if r.Decision == reason {
					return nil
				}
				return model.Err("idempotency_conflict", "original cancellation reason differs")
			}
			return m.cancelSnapshotLocked(ctx, r, reason)
		}
	}
	if revision != 0 {
		return model.Err("revision_conflict", "no admitted reservation exists")
	}
	var count int
	fence := snapshotlease.LeaseKey(m.host, key, "request")
	if e := m.db.QueryRowContext(ctx, `SELECT count(*) FROM cancelled_requests WHERE key=?`, fence).Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return nil
	}
	if e := m.db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT key FROM cancelled_requests LIMIT 257)`).Scan(&count); e != nil {
		return e
	}
	if count >= 256 {
		return model.Err("snapshot_capacity_exceeded", "shared durable cancellation fence history is full for new snapshot keys")
	}
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer m.rollbackSnapshotLocked(tx, "pre-cancel", key)
	if _, e = tx.ExecContext(ctx, `INSERT INTO cancelled_requests(key) VALUES(?)`, fence); e != nil {
		return e
	}
	m.traceSnapshotTransactionLocked(tx, "pre-cancel-fence-staged", key)
	if e = event(tx, "snapshot.request_cancelled", fence, map[string]any{"key": key, "reason": reason, "planning_attempted": false}); e != nil {
		return e
	}
	return m.commitSnapshotLocked(ctx, tx, "pre-cancel", key)
}

func (m *Manager) cancelSnapshotLocked(ctx context.Context, r snapshotlease.Record, reason string) error {
	if e := m.snapshotPolicyError(); e != nil {
		return e
	}
	if r.Phase == "cancelled-no-work" {
		return nil
	}
	if r.Validate() != nil || r.Phase != "reserved-no-work" || r.PlanningAttempted || r.AllocationAttempted {
		return model.Err("snapshot_evidence_unavailable", "only exact known never-started reservation can settle")
	}
	// Fresh consistent revalidation prevents a stale cache authorizing release.
	state, e := snapshotlease.Load(ctx, m.db, m.host, m.owner, m.home)
	if e != nil {
		m.snapshots.failure = fmt.Errorf("complete snapshot evidence revalidation failed: %w", e)
		return e
	}
	old, ok := state.Copies[r.ID]
	if !ok || old != r {
		return model.Err("revision_conflict", "snapshot reservation changed")
	}
	work, e := m.get(r.WorkLease)
	if e != nil {
		return e
	}
	destination, e := m.get(r.DestinationLease)
	if e != nil {
		return e
	}
	next := r
	next.Revision++
	next.Phase = "cancelled-no-work"
	next.FinalSlots = 0
	next.Decision = reason
	next.Updated = maxSnapshotTime(r.Updated, time.Now().UTC())
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer m.rollbackSnapshotLocked(tx, "cancel", r.ID)
	if e = snapshotlease.WriteCancel(ctx, tx, r, next); e != nil {
		return e
	}
	for _, l := range []model.Lease{work, destination} {
		before, e := json.Marshal(l)
		if e != nil {
			return e
		}
		l.Revision++
		l.State = "terminal"
		l.Outcome = "cancelled"
		l.Ended = &next.Updated
		raw, e := json.Marshal(l)
		if e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, `UPDATE leases SET data=? WHERE id=? AND data=?`, string(raw), l.ID, string(before))
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return model.Err("revision_conflict", "snapshot promise changed during cancellation")
		}
		if e = event(tx, "lease.snapshot_cancelled_no_work", l.ID, l); e != nil {
			return e
		}
	}
	m.traceSnapshotTransactionLocked(tx, "cancellation-writes-staged", r.ID)
	if e = event(tx, "snapshot.cancelled_no_work", r.ID, next); e != nil {
		return e
	}
	if e = m.commitSnapshotLocked(ctx, tx, "cancel", r.ID); e != nil {
		return e
	}
	state, e = snapshotlease.Load(ctx, m.db, m.host, m.owner, m.home)
	if e != nil {
		m.snapshots.failure = fmt.Errorf("committed snapshot cancellation cannot be revalidated: %w", e)
		m.traceSnapshotCut("publication-read-refused", r.ID, snapshotTraceCut{APIError: e.Error()})
		return model.Err("snapshot_outcome_unknown", m.snapshots.failure.Error())
	}
	m.snapshots.state = state
	m.snapshots.commitPending = false
	m.traceSnapshotLocked("cancellation-published", r.ID)
	return nil
}
func maxSnapshotTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (m *Manager) endSnapshotIfNeeded(l model.Lease, outcome string) (bool, model.Lease, error) {
	r, found := m.snapshotLease(l.ID)
	if !found {
		if l.Request.Kind == snapshotlease.WorkKind {
			return true, l, model.Err("snapshot_evidence_unavailable", "orphan internal promise remains charged")
		}
		if m.snapshots != nil {
			for _, r := range m.snapshots.state.Copies {
				if r.Source == l.ID && r.Phase == "reserved-no-work" {
					if e := m.cancelSnapshotLocked(context.Background(), r, "Source ending: "+outcome); e != nil {
						return true, l, e
					}
				}
			}
		}
		return false, l, nil
	}
	if e := m.cancelSnapshotLocked(context.Background(), r, "Ordinary end requested: "+outcome); e != nil {
		return true, l, e
	}
	latest, e := m.get(l.ID)
	return true, latest, e
}

func (m *Manager) snapshotCloseError() error {
	if m.snapshots == nil {
		return nil
	}
	if len(m.snapshots.controls) != 0 {
		return model.Err("snapshot_preflight_unknown", "original preflight controls remain unresolved after real owners returned")
	}
	return m.snapshotPolicyError()
}
