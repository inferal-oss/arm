package manager

import (
	"database/sql"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

// commitShareTransaction is called under Manager.mu only for transactions that
// atomically change ordinary lease and optional share evidence. Commit failure
// cannot establish rollback: SQLite may have persisted the data before the
// caller lost its acknowledgment. Do not publish stale in-memory absence/phase
// or run any native worker based on that cache. No database reload or native I/O
// occurs under the lock; startup's complete reader is the recovery boundary.
func (m *Manager) commitShareTransaction(tx *sql.Tx) error {
	err := tx.Commit()
	if err == nil && m.shares.commitAckFailure != nil {
		// A test-only one-shot value simulates lost acknowledgment AFTER the real
		// durable transaction. Wire JSON cannot construct a shareBoundary.
		err = m.shares.commitAckFailure
		m.shares.commitAckFailure = nil
	}
	if err == nil {
		return nil
	}
	m.shares.failure = fmt.Errorf("share transaction outcome is uncertain; durable lease/intent may differ from cache: %w", err)
	// Request cancellation with private nonblocking channels. Watchers execute
	// cancellation outside Mu; occupied native work remains charged until join.
	for _, worker := range m.shares.workers {
		worker.requestStop()
	}
	return model.Err("share_outcome_unknown", "share transaction may have committed; exact-key inspection is unavailable until the complete ledger is revalidated on backend restart; no execution or mutation is retried")
}
