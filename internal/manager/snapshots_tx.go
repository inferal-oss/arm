package manager

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

func (m *Manager) commitSnapshotLocked(ctx context.Context, tx *sql.Tx, action, id string) error {
	if e := ctx.Err(); e != nil {
		m.traceSnapshotCut("commit-context-refused", id, snapshotTraceCut{Action: action, APIError: e.Error()})
		return e
	}
	m.traceSnapshotTransactionLocked(tx, "before-"+action+"-commit", id)
	m.snapshots.commitPending = true
	m.traceSnapshotCut("commit-call-entry", id, snapshotTraceCut{Action: action})
	apiError := tx.Commit()
	var reportError error
	if apiError == nil && m.snapshots.commitAckFailure != nil {
		reportError = m.snapshots.commitAckFailure
		m.snapshots.commitAckFailure = nil
	}
	m.traceSnapshotCut("commit-call-return", id, snapshotTraceCut{Action: action,
		APIError: snapshotTraceError(apiError), ReportError: snapshotTraceError(reportError)})
	e := apiError
	if e == nil {
		e = reportError
	}
	if e == nil {
		if action == "pre-cancel" {
			m.snapshots.commitPending = false
		}
		m.traceSnapshotLocked(action+"-commit-return", id)
		return nil
	}
	m.snapshots.failure = fmt.Errorf("snapshot transaction acknowledgment unknown: %w", e)
	m.traceSnapshotLocked(action+"-commit-unknown", id)
	return model.Err("snapshot_outcome_unknown", "durable reservation/cancel may differ; exact-key read only, no worker or allocation retry")
}

// Exactly the original deferred Rollback call, with its actual result retained.
// ErrTxDone is not proof that a failed Commit rolled back or persisted.
func (m *Manager) rollbackSnapshotLocked(tx *sql.Tx, action, id string) {
	e := tx.Rollback()
	m.traceSnapshotCut("rollback-call-return", id, snapshotTraceCut{Action: action, APIError: snapshotTraceError(e)})
}
