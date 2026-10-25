package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
	"github.com/yrashk/arm/internal/storage"
)

type snapshotPreflight struct {
	source      string
	provider    *storage.Provider
	observation storage.SnapshotReservationObservation
	returned    bool
	unknown     bool
}
type snapshotBoundary struct {
	runtime          string
	state            snapshotlease.Snapshot
	failure          error
	controls         map[string]*snapshotPreflight
	commitPending    bool  // private publication fence; never a durable key cancellation
	commitAckFailure error // test-only report AFTER a real commit; no wire selector
	preflight        func(context.Context, *storage.Provider, model.Artifact) (storage.SnapshotReservationObservation, error)
}

func (m *Manager) initSnapshots() error {
	b := &snapshotBoundary{runtime: local.ID(), controls: map[string]*snapshotPreflight{}}
	m.snapshots = b
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var e error
	b.state, e = snapshotlease.Load(ctx, m.db, m.host, m.owner, m.home)
	b.failure = e
	m.traceSnapshotCut("sql-owner-loaded", "", snapshotTraceCut{APIError: snapshotTraceError(e)})
	// This runs before ANY generic storage/queue recovery. Partial optional
	// evidence or orphan internal work refuses startup rather than provisioning.
	return e
}
func (m *Manager) snapshotPolicyError() error {
	if m.snapshots == nil {
		return model.Err("snapshot_unavailable", "private snapshot reservation boundary unavailable")
	}
	if m.snapshots.commitPending {
		return model.Err("snapshot_outcome_unknown", "original transaction/cache publication is incomplete; read exact key only")
	}
	if m.snapshots.failure != nil {
		return model.Err("snapshot_evidence_unavailable", m.snapshots.failure.Error()+"; reservations remain held and no work is retried")
	}
	return nil
}
func (m *Manager) snapshotStorageBusy(id, ignoreControl string) (string, error) {
	if m.snapshots == nil {
		return "", nil
	} // only minimal test Managers lack initialization
	if e := m.snapshotPolicyError(); e != nil {
		return "", e
	}
	for key, c := range m.snapshots.controls {
		if key != ignoreControl && c.source == id {
			return key, nil
		}
	}
	for key, r := range m.snapshots.state.Copies {
		if r.Phase == "reserved-no-work" && (r.Source == id || r.DestinationLease == id) {
			return key, nil
		}
	}
	return "", nil
}
func (m *Manager) snapshotLease(id string) (snapshotlease.Record, bool) {
	if m.snapshots != nil {
		for _, r := range m.snapshots.state.Copies {
			if r.WorkLease == id || r.DestinationLease == id {
				return r, true
			}
		}
	}
	return snapshotlease.Record{}, false
}
func (m *Manager) snapshotSource(ctx context.Context, q snapshotlease.Request, ignoreControl string) (model.Lease, error) {
	l, e := snapshotlease.ReadSourceLease(ctx, m.db, q.Source, m.host, m.owner, m.home)
	if e != nil {
		return l, e
	}
	if e = m.sharePolicyError(); e != nil {
		return l, e
	}
	if m.closing || m.draining {
		return l, model.Err("backend_stopping", "snapshot reservations are unavailable during shutdown")
	}
	if l.Host != m.host || l.Owner != m.owner || l.State != "active" || l.Revision != q.SourceRevision || l.Request.Kind != "storage" || l.Artifact == nil || l.Artifact.Identity == nil || l.Artifact.Phase != "owned" || l.Expires == nil || !l.Expires.After(time.Now().UTC().Add(q.WorkTTL)) || m.storage == nil || m.storageErr != nil {
		return l, model.Err("storage_unavailable", "exact active existing source/provider must cover the work deadline")
	}
	if busy, e := m.snapshotStorageBusy(l.ID, ignoreControl); e != nil {
		return l, e
	} else if busy != "" {
		return l, model.Err("storage_in_use", "snapshot reservation/control "+busy+" retains this allocation")
	}
	for _, p := range m.shares.state.Publications {
		if p.Request.Storage == l.ID && p.Retired == nil {
			return l, model.Err("storage_in_use", "strict share retains the source")
		}
	}
	if busy, e := m.storageExportBusy(l.ID); e != nil {
		return l, e
	} else if busy != "" {
		return l, model.Err("storage_in_use", "an export retains the source")
	}
	if m.storageJobs[l.ID] || m.storageJobs[exportJobKey(l.ID)] {
		return l, model.Err("operation_busy", "source cleanup/export is running")
	}
	leases, e := m.list()
	if e != nil {
		return l, e
	}
	if len(storageConsumers(leases, l.ID)) != 0 {
		return l, model.Err("storage_in_use", "queued or active ARM source writers remain")
	}
	return l, nil
}

// lookupSnapshotReservation is exact read-only SQL recovery even when a commit
// acknowledgement was lost. It never mutates the boundary or starts work.
func (m *Manager) lookupSnapshotReservation(ctx context.Context, key string) (snapshotlease.Record, bool, error) {
	v, e := m.lookupSnapshotRequest(ctx, key)
	m.traceSnapshotMethod("lookup-call-return", snapshotTraceCut{Action: "lookup", Key: key, Record: &v.Record, Present: &v.Present, APIError: snapshotTraceError(e)})
	return v.Record, v.Present, e
}

type snapshotRequestLookup struct {
	Record        snapshotlease.Record
	Present       bool
	RequestFenced bool
}

// A pre-admission cancellation has no copy row. Its durable fence is a distinct
// read result, including after a lost acknowledgement; absence is never guessed
// from the original boundary cache. Both facts come from one SQL snapshot.
func (m *Manager) lookupSnapshotRequest(ctx context.Context, key string) (out snapshotRequestLookup, resultErr error) {
	if !budgetText(key, 128, true) {
		return out, model.Err("invalid_request", "exact bounded snapshot request key required")
	}
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			resultErr = errors.Join(resultErr, rollbackErr)
		}
		if resultErr != nil && !errors.Is(resultErr, context.Canceled) && !errors.Is(resultErr, context.DeadlineExceeded) {
			m.mu.Lock()
			if m.snapshots != nil {
				m.snapshots.failure = errors.Join(m.snapshots.failure, fmt.Errorf("snapshot exact-key read failed: %w", resultErr))
			}
			m.mu.Unlock()
		}
	}()
	state, e := snapshotlease.ReadTransaction(ctx, tx, m.host, m.owner, m.home)
	if e != nil {
		return out, e
	}
	for _, r := range state.Copies {
		if r.Key == key {
			out.Record = r
			out.Present = true
			break
		}
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM cancelled_requests WHERE key=?`, snapshotlease.LeaseKey(m.host, key, "request")).Scan(&count); e != nil {
		return out, e
	}
	if count > 1 {
		return out, model.Err("snapshot_evidence_unavailable", "duplicate request fence")
	}
	out.RequestFenced = count == 1
	return out, nil
}

func snapshotSameSource(a, b model.Lease) bool {
	return a.ID == b.ID && a.Host == b.Host && a.Owner == b.Owner && a.ManagedDir == b.ManagedDir && reflect.DeepEqual(a.Request, b.Request) && a.Created.Equal(b.Created) && a.State == b.State && a.Revision == b.Revision && reflect.DeepEqual(a.Artifact, b.Artifact) && a.Expires != nil && b.Expires != nil && a.Expires.Equal(*b.Expires)
}
func (m *Manager) finishSnapshotPreflight(key string, c *snapshotPreflight) {
	m.mu.Lock()
	if !c.returned {
		c.unknown = true
		m.snapshots.failure = fmt.Errorf("original snapshot preflight did not return a receipt")
	}
	if m.snapshots.controls[key] == c && !c.unknown {
		delete(m.snapshots.controls, key)
	}
	m.traceSnapshotLocked("preflight-owner-return", key)
	m.mu.Unlock()
	m.wg.Done()
}
func (m *Manager) snapshotPreflightResult(c *snapshotPreflight, out storage.SnapshotReservationObservation, e error) error {
	c.observation = out
	c.returned = true
	if out.Native.Unknown || !out.Native.Closed {
		c.unknown = true
		m.snapshots.failure = errors.Join(fmt.Errorf("original snapshot reservation preflight close is unresolved"), e)
		return model.Err("snapshot_preflight_unknown", "original control remains retained; no close retry or planning is permitted")
	}
	if e == nil {
		return out.Validate()
	}
	return e
}
