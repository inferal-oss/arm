package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

// Explicit End/Close may withdraw only these already registered capabilities,
// even when the following SQL read fails. They do not reconstruct controls or
// grant mutation/settlement authority over the row that SQL later returns.
// Caller holds Manager.mu; no original Wait or worker join occurs here.
func (m *Manager) cancelRegisteredProfileOwners(id string) {
	if id == "" {
		return
	}
	if w := m.cargoWorkers[id]; w != nil && w.leaseID == id && w.leaseHost == m.host && w.leaseOwner == m.owner && w.cancel != nil {
		w.cancel()
	}
	for child, j := range m.jobs {
		if j == nil || !isGradleProfile(j.leaseRequest.Profile) || (child != id && j.leaseRequest.ServiceLease != id) ||
			j.leaseHost != m.host || j.leaseOwner != m.owner || j.childOwner == nil || j.cmd == nil || j.done == nil {
			continue
		}
		m.cancelRegisteredGradleJob(child, j)
	}
}

// Reached only after the ordinary storage operation_busy fence admits ending.
// Original request bindings supply dependency identity, never a saved PID.
func (m *Manager) cancelRegisteredStorageConsumers(id string) {
	if id == "" {
		return
	}
	for _, w := range m.cargoWorkers {
		if w != nil && m.cargoWorkers[w.leaseID] == w && w.leaseRequest.StorageLease == id && w.leaseHost == m.host && w.leaseOwner == m.owner && w.cancel != nil {
			w.cancel()
		}
	}
	for child, j := range m.jobs {
		if j == nil || j.leaseRequest.StorageLease != id || j.leaseHost != m.host || j.leaseOwner != m.owner ||
			j.leaseRequest.Profile == "rust-build" || j.childOwner == nil || j.cmd == nil || j.done == nil {
			continue // Cargo's original helper watcher owns its signal/IPC ordering.
		}
		_ = m.requestOriginalJobCancellation(child, j)
	}
}

func retainedLeaseBinding(id string, l, original model.Lease) bool {
	return id != "" && l.ID == id && original.ID == id && l.Host == original.Host && l.Owner == original.Owner &&
		reflect.DeepEqual(l.Request, original.Request) && l.PID == 0 && l.Process == nil
}

// The caller has freshly read the entire physical-keyed roster and verified a
// same-scope native sample after checkpoint refusal. Cancellation is volatile
// only: this method neither writes a row nor replaces any positive join proof.
func (m *Manager) cancelCheckpointExpiredHolders(sample leaseclock.Sample, rows []checkpointExpiryLease) error {
	if !sample.Valid() {
		return model.Err("lease_clock_unavailable", "valid native sample required for retained-holder cancellation")
	}
	var failures error
	keyed := keyedRetainedRows(rows)
	for _, row := range rows {
		l := row.lease
		if row.id == "" || l.ID != row.id {
			failures = errors.Join(failures, model.Err("needs_attention", "physical retained-holder key differs; obligations remain charged"))
			continue
		}
		if l.Host != m.host || l.Owner != m.owner || (l.State != "active" && l.State != "provisioning") {
			continue
		}
		remaining, err := l.Clock.Remaining(sample, l.Expires)
		if err != nil || remaining != 0 {
			continue
		}
		failures = errors.Join(failures, m.cancelBoundRetained(row))
		failures = errors.Join(failures, m.cancelExpiredRetainedDependents(row, keyed))
	}
	return failures
}

type retainedCancellationMode uint8

const (
	retainedValidateOnly retainedCancellationMode = iota
	retainedWithdrawOriginal
)

func (m *Manager) cancelBoundRetained(row checkpointExpiryLease) error {
	return m.checkRetainedCancellation(row, retainedWithdrawOriginal)
}
func (m *Manager) checkRetainedCancellation(row checkpointExpiryLease, mode retainedCancellationMode) error {
	id, l := row.id, row.lease
	changed := func() error {
		return model.Err("needs_attention", "original retained-holder binding differs; obligations remain charged")
	}
	if w := m.cargoWorkers[id]; w != nil {
		if w.leaseID != id || w.id == "" || w.cancel == nil || l.Cargo == nil || l.Request.Profile != "rust-build" ||
			l.Host != w.leaseHost || l.Owner != w.leaseOwner || !reflect.DeepEqual(l.Request, w.leaseRequest) || !retainedDeadlineBinding(l, w.lease) {
			return changed()
		}
		rec, err := m.getCargo(id)
		if err != nil {
			return err
		}
		if rec.Worker != w.id || rec.Host != w.leaseHost || rec.Owner != w.leaseOwner || rec.Storage != w.leaseRequest.StorageLease {
			return changed()
		}
		if j := m.jobs[id]; j != nil {
			if !m.retainedJobBinding(id, l, j) {
				return changed()
			}
		} else if l.PID != 0 || l.Process != nil {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			w.cancel()
		} // The captured worker signals and joins its ORIGINAL helper.
		return nil
	}
	if j := m.jobs[id]; j != nil && isGradleProfile(l.Request.Profile) {
		if !m.retainedJobBinding(id, l, j) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			return m.requestOriginalJobCancellation(id, j)
		}
		return nil
	}
	if h := m.relayTransits[id]; h != nil {
		if h.manager != m || h.id != id || h.cancel == nil || h.settled || h.key != l.Request.Key ||
			!retainedLeaseBinding(id, l, h.result) || !reflect.DeepEqual(l.Clock, h.result.Clock) || !reflect.DeepEqual(l.Expires, h.result.Expires) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			h.cancel()
		}
	}
	if h := m.forwardListeners[id]; h != nil {
		if h.manager != m || h.id != id || h.cancel == nil || h.settled || h.key != l.Request.Key ||
			!retainedLeaseBinding(id, l, h.result) || !reflect.DeepEqual(l.Request, h.request) || !reflect.DeepEqual(l.Ports, h.result.Ports) ||
			!reflect.DeepEqual(l.Clock, h.result.Clock) || !reflect.DeepEqual(l.Expires, h.result.Expires) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			h.CloseOriginal()
		}
	}
	if h := m.syncWorkers[id]; h != nil {
		raw, err := json.Marshal(l)
		if err != nil || h.manager != m || h.lease != id || h.runtime == nil || !bytes.Equal(raw, h.leaseRecord) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			h.cancel()
		}
	}
	if h := m.syncSourceWorkers[id]; h != nil {
		raw, err := json.Marshal(l)
		if err != nil || h.manager != m || h.lease != id || h.cancelCtx == nil || !bytes.Equal(raw, h.leaseRecord) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			h.cancel()
		}
	}
	if h := m.storageExportCancels[id]; h != nil {
		if h.cancel == nil || h.operation == "" || !m.storageJobs[exportJobKey(id)] || !m.storageJobs[exportOperationJobKey(h.operation)] ||
			l.Request.Kind != "storage" || !retainedLeaseBinding(id, l, h.lease) || !retainedDeadlineBinding(l, h.lease) || l.ManagedDir != h.lease.ManagedDir || l.Artifact == nil || h.lease.Artifact == nil ||
			!reflect.DeepEqual(l.Artifact.Identity, h.lease.Artifact.Identity) {
			return changed()
		}
		if mode == retainedWithdrawOriginal {
			h.cancel()
		}
	}
	return nil
}

// The map pointer and captured immutable execution tuple authorize cancellation,
// not a new signal. The original cancellation helper/Owner controls retirement;
// this check must never wait on Owner.mu while Manager.mu is held.
func (m *Manager) retainedJobBinding(id string, l model.Lease, j *job) bool {
	return j != nil && m.jobs[id] == j && j.childOwner != nil && j.cmd != nil && j.cmd.Process != nil && j.done != nil &&
		l.ID == id && l.Host == m.host && l.Owner == m.owner && l.Host == j.leaseHost && l.Owner == j.leaseOwner &&
		reflect.DeepEqual(l.Request, j.leaseRequest) && reflect.DeepEqual(l.Clock, j.leaseDeadline) && reflect.DeepEqual(l.Expires, j.leaseExpires) && j.identity != nil && l.Process != nil && l.PID > 0 &&
		l.PID == j.cmd.Process.Pid && j.identity.PID == l.PID && j.identity.PGID == l.PID && j.identity.UID == l.Owner &&
		j.identity.Start != "" && j.identity.Namespace != "" && reflect.DeepEqual(l.Process, j.identity)
}

func retainedDeadlineBinding(l, original model.Lease) bool {
	return reflect.DeepEqual(l.Clock, original.Clock) && reflect.DeepEqual(l.Expires, original.Expires)
}

func (m *Manager) cancelRegisteredExport(id string) {
	if id == "" {
		return
	}
	h := m.storageExportCancels[id]
	if h != nil && h.lease.ID == id && h.lease.Host == m.host && h.lease.Owner == m.owner && h.operation != "" &&
		h.cancel != nil && m.storageJobs[exportJobKey(id)] && m.storageJobs[exportOperationJobKey(h.operation)] {
		h.cancel()
	}
}

// Renew may extend these existing owners, never replace them. Only the ACKed
// ordinary mutation updates the expected native deadline used by expiry.
func (m *Manager) refreshRetainedDeadline(l model.Lease) {
	if j := m.jobs[l.ID]; j != nil && l.Host == j.leaseHost && l.Owner == j.leaseOwner && reflect.DeepEqual(l.Request, j.leaseRequest) {
		j.leaseDeadline, j.leaseExpires = l.Clock, l.Expires
	}
	for _, j := range m.jobs {
		if j != nil && retainedLeaseBinding(l.ID, l, j.storageParent) {
			j.storageParent.Clock, j.storageParent.Expires = l.Clock, l.Expires
		}
	}
	if w := m.cargoWorkers[l.ID]; w != nil && l.Host == w.leaseHost && l.Owner == w.leaseOwner && reflect.DeepEqual(l.Request, w.leaseRequest) {
		w.lease.Clock, w.lease.Expires = l.Clock, l.Expires
	}
	for _, w := range m.cargoWorkers {
		if w != nil && retainedLeaseBinding(l.ID, l, w.storageParent) {
			w.storageParent.Clock, w.storageParent.Expires = l.Clock, l.Expires
		}
	}
	if h := m.storageExportCancels[l.ID]; h != nil && retainedLeaseBinding(l.ID, l, h.lease) {
		h.lease.Clock, h.lease.Expires = l.Clock, l.Expires
	}
}

// A parent's proven expiry only withdraws original dependents carrying that
// captured parent binding. It neither ends them durably nor cleans the parent.
func (m *Manager) cancelExpiredRetainedDependents(row checkpointExpiryLease, current map[string]model.Lease) error {
	l := row.lease
	if l.Request.Kind == "storage" {
		for _, w := range m.cargoWorkers {
			if w != nil && w.cancel != nil && w.leaseID != "" && m.cargoWorkers[w.leaseID] == w && w.leaseRequest.StorageLease == row.id && retainedLeaseBinding(row.id, l, w.storageParent) &&
				retainedDeadlineBinding(l, w.storageParent) && l.ManagedDir == w.storageParent.ManagedDir &&
				l.Artifact != nil && w.storageParent.Artifact != nil && reflect.DeepEqual(l.Artifact.Identity, w.storageParent.Artifact.Identity) {
				child, exists := current[w.leaseID]
				if exists && child.ID == w.leaseID && m.checkRetainedCancellation(checkpointExpiryLease{id: w.leaseID, lease: child}, retainedValidateOnly) == nil {
					w.cancel()
				}
			}
		}
		for id, j := range m.jobs {
			if j != nil && j.childOwner != nil && j.cmd != nil && j.done != nil && j.leaseHost == m.host && j.leaseOwner == m.owner &&
				j.leaseRequest.Profile != "rust-build" && j.leaseRequest.StorageLease == row.id &&
				retainedLeaseBinding(row.id, l, j.storageParent) && retainedDeadlineBinding(l, j.storageParent) &&
				l.ManagedDir == j.storageParent.ManagedDir && l.Artifact != nil && j.storageParent.Artifact != nil &&
				reflect.DeepEqual(l.Artifact.Identity, j.storageParent.Artifact.Identity) {
				child, exists := current[id]
				if exists && child.ID == id && m.retainedJobBinding(id, child, j) {
					_ = m.requestOriginalJobCancellation(id, j)
				}
			}
		}
	}
	if l.Request.Profile == "gradle-service" {
		parent := m.jobs[row.id]
		if !m.retainedJobBinding(row.id, l, parent) {
			return model.Err("needs_attention", "original service parent binding differs; obligations remain charged")
		}
		for id, j := range m.jobs {
			if j != nil && isGradleProfile(j.leaseRequest.Profile) && j.leaseRequest.ServiceLease == row.id &&
				j.leaseHost == m.host && j.leaseOwner == m.owner {
				child, exists := current[id]
				if exists && child.ID == id && m.retainedJobBinding(id, child, j) {
					_ = m.requestOriginalJobCancellation(id, j)
				}
			}
		}
	}
	return nil
}

// The worker owns this watcher and joins it before returning. A complete keyed
// roster and one fresh native sample precede each cancellation; failed reads
// confer no automatic authority. No SQL writes or joined witnesses occur here.
func (m *Manager) watchCargoEntitlement(ctx context.Context, w *cargoWorker, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		if m.cargoWorkers[w.leaseID] != w {
			m.mu.Unlock()
			return
		}
		rows, err := m.checkpointExpiryLeases()
		var sample leaseclock.Sample
		if err == nil {
			sample, err = m.readLeaseClock()
		}
		if err == nil {
			for _, row := range rows {
				if row.id != w.leaseID && row.id != w.leaseRequest.StorageLease {
					continue
				}
				if row.id == "" || row.id != row.lease.ID || row.lease.Host != m.host || row.lease.Owner != m.owner ||
					(row.lease.State != "active" && row.lease.State != "provisioning") {
					continue
				}
				if remaining, e := row.lease.Clock.Remaining(sample, row.lease.Expires); e == nil && remaining == 0 {
					if row.id == w.leaseID {
						_ = m.cancelBoundRetained(row)
					} else {
						_ = m.cancelExpiredRetainedDependents(row, keyedRetainedRows(rows))
					}
				}
			}
		}
		m.mu.Unlock()
	}
}

// Intrinsic original registration, never a recovered row or log PID. The
// common cancellation helper owns once-only escalation and sole native Wait.
func (m *Manager) cancelRegisteredGradleJob(id string, j *job) {
	if j != nil && m.jobs[id] == j && isGradleProfile(j.leaseRequest.Profile) && j.leaseHost == m.host &&
		j.leaseOwner == m.owner && j.cmd != nil && j.childOwner != nil && j.done != nil {
		_ = m.requestOriginalJobCancellation(id, j)
	}
}

// An automatic cause is minted only after a complete physical-keyed roster
// and fresh native sample. It is never inferred from an outcome/reason string.
// The roster travels through dependent endings; they cannot regain explicit
// End's unconditional pre-read withdrawal authority.
type automaticExpiryRoster struct {
	rows      map[string]model.Lease
	sample    leaseclock.Sample
	validated map[string]bool
}
type automaticExpiryEnding struct {
	roster      *automaticExpiryRoster
	parent      string
	expiredRoot string
}

func (a *automaticExpiryEnding) dependent(parent string) *automaticExpiryEnding {
	return &automaticExpiryEnding{roster: a.roster, parent: parent, expiredRoot: a.expiredRoot}
}

func (m *Manager) endAutomaticExpiry(id, outcome string, a *automaticExpiryEnding) (model.Lease, error) {
	var l model.Lease
	refused := func() (model.Lease, error) {
		return l, model.Err("needs_attention", "automatic expiry original/physical binding differs; obligations remain charged")
	}
	if a == nil || a.roster == nil || id == "" {
		return refused()
	}
	expected, ok := a.roster.rows[id]
	if !ok || expected.ID != id {
		return refused()
	}
	current, err := m.checkpointExpiryLeaseRows(id)
	if err != nil {
		return l, err
	}
	if len(current) != 1 || current[0].id != id {
		return refused()
	}
	l = current[0].lease
	if a.roster.validated[id] && (l.State == "ending" || l.State == "terminal") && l.ID == id && l.Host == expected.Host && l.Owner == expected.Owner &&
		reflect.DeepEqual(l.Request, expected.Request) && retainedDeadlineBinding(l, expected) {
		return l, nil // Already handled through this roster's validated parent.
	}
	eligibleState := l.State == "active" || l.State == "provisioning" || (a.parent != "" && l.State == "queued")
	if !reflect.DeepEqual(l, expected) || l.Host != m.host || l.Owner != m.owner || !eligibleState {
		return refused()
	}
	sample, err := m.readLeaseClock()
	if err != nil {
		return l, err
	}
	if sample.Boot != a.roster.sample.Boot || sample.Tick < a.roster.sample.Tick {
		return refused()
	}
	if a.parent == "" {
		remaining, e := l.Clock.Remaining(sample, l.Expires)
		if e != nil || remaining != 0 {
			return refused()
		}
	} else {
		root, exists := a.roster.rows[a.expiredRoot]
		if !exists || !a.roster.validated[a.expiredRoot] || !a.roster.validated[a.parent] || (l.Request.StorageLease != a.parent && l.Request.ServiceLease != a.parent) {
			return refused()
		}
		remaining, e := root.Clock.Remaining(sample, root.Expires)
		if e != nil || remaining != 0 {
			return refused()
		}
		parent := a.roster.rows[a.parent]
		if l.Request.StorageLease == a.parent {
			if w := m.cargoWorkers[id]; w != nil {
				if !retainedStorageParentBinding(parent, w.storageParent) {
					return refused()
				}
			} else if j := m.jobs[id]; j != nil {
				if !retainedStorageParentBinding(parent, j.storageParent) {
					return refused()
				}
			} else if k := m.kubernetesSources[id]; k != nil {
				if k.manager != m || !retainedStorageParentBinding(parent, k.parent) {
					return refused()
				}
			}
		}
	}
	if j := m.jobs[id]; j != nil {
		if !m.retainedJobBinding(id, l, j) {
			return refused()
		}
	} else if l.PID != 0 || l.Process != nil {
		return refused()
	}
	row := checkpointExpiryLease{id: id, lease: l}
	if err = m.checkRetainedCancellation(row, retainedValidateOnly); err != nil {
		return l, err
	}
	// All current immutable/native bindings and the fresh elapsed proof precede
	// this first automatic withdrawal. Cancellation is still not settlement.
	if err = m.cancelBoundRetained(row); err != nil {
		return l, err
	}
	if err = m.cancelExpiredRetainedDependents(row, a.roster.rows); err != nil {
		return l, err
	}
	a.roster.validated[id] = true
	next := *a
	if next.parent == "" {
		next.expiredRoot = id
	}
	return m.endLease(id, l, outcome, &next)
}

func retainedStorageParentBinding(current, original model.Lease) bool {
	return retainedLeaseBinding(current.ID, current, original) && retainedDeadlineBinding(current, original) &&
		current.ManagedDir == original.ManagedDir && current.Artifact != nil && original.Artifact != nil && reflect.DeepEqual(current.Artifact.Identity, original.Artifact.Identity)
}

func keyedRetainedRows(rows []checkpointExpiryLease) map[string]model.Lease {
	keyed := make(map[string]model.Lease, len(rows))
	for _, row := range rows {
		keyed[row.id] = row.lease
	}
	return keyed
}

// Retry only evidence retained by the actual original waiter after a refused
// durable ACK. This is completion recovery, not PID-derived expiry control.
// Caller holds Manager.mu; no signal, observer or Wait is invoked here.
func (m *Manager) retryOriginalJobSettlement(row checkpointExpiryLease, j *job) error {
	d := j.failedSettlement
	refused := func() error {
		return errors.Join(d.failure, model.Err("needs_attention", "original completion predecessor changed; obligations remain charged"))
	}
	if d == nil || d.failure == nil || m.jobs[row.id] != j || d.job != j || d.owner != j.childOwner || d.cmd != j.cmd || j.sshRuntime != nil {
		return model.Err("needs_attention", "original failed settlement unavailable")
	}
	select {
	case <-j.nativeDone:
	default:
		return refused()
	}
	select {
	case <-j.done:
	default:
		return refused()
	}
	var expected, candidate model.Lease
	if json.Unmarshal([]byte(d.expected), &expected) != nil || json.Unmarshal([]byte(d.candidate), &candidate) != nil ||
		row.id == "" || row.lease.ID != row.id || expected.ID != row.id || candidate.ID != row.id || !m.originalCompletionBinding(row.id, expected, j) ||
		candidate.Host != expected.Host || candidate.Owner != expected.Owner || !reflect.DeepEqual(candidate.Request, expected.Request) ||
		!retainedDeadlineBinding(candidate, expected) {
		return refused()
	}
	if candidate.State == "terminal" {
		if !d.reaped || !d.nativeSafe || !j.cpuExited.Load() || j.cmd.ProcessState == nil || candidate.Ended == nil {
			return refused()
		}
	} else if candidate.State != "needs_attention" || candidate.Ended != nil {
		return refused()
	}
	if err := m.commitOriginalJobSettlement(row.id, d); err != nil {
		return errors.Join(d.failure, err)
	}
	j.failedSettlement = nil
	if d.reaped {
		delete(m.jobs, row.id)
	}
	return nil
}

// Completion is authorized by the original command/Owner and sole waiter,
// including when optional process-usage observation was unavailable at start.
// It does not grant new signal authority or reconstruct a missing native owner.
func (m *Manager) originalCompletionBinding(id string, l model.Lease, j *job) bool {
	return j != nil && m.jobs[id] == j && j.childOwner != nil && j.cmd != nil && j.cmd.Process != nil && j.done != nil && j.nativeDone != nil && j.sshRuntime == nil &&
		l.ID == id && l.Host == m.host && l.Owner == m.owner && l.Host == j.leaseHost && l.Owner == j.leaseOwner &&
		reflect.DeepEqual(l.Request, j.leaseRequest) && reflect.DeepEqual(l.Clock, j.leaseDeadline) && reflect.DeepEqual(l.Expires, j.leaseExpires) &&
		l.PID == j.cmd.Process.Pid && l.PID > 0 && reflect.DeepEqual(l.Process, j.identity)
}

// Match the database proof reader's current per-record limit. The finite
// aggregate includes predecessor, candidate, saved run and key/identity text.
const (
	originalSettlementRecordBytes = database.MaxRecordBytes
	originalSettlementTupleBytes  = 2*originalSettlementRecordBytes + 256
	originalSettlementDebtBytes   = 3*originalSettlementRecordBytes + 512
	originalSettlementEventSpan   = 256
)

type originalSettlementLaunch struct {
	key, run sql.NullString
}

type originalSettlementTuple struct {
	id, data string
	key, run sql.NullString
}

type settlementQuerier interface {
	QueryRow(string, ...any) *sql.Row
}

// SQLite performs type/byte/aggregate checks before any field is copied across
// Scan. In particular a BLOB or oversized value cannot become retained debt.
func readOriginalSettlementTuple(q settlementQuerier, id string) (originalSettlementTuple, error) {
	var out originalSettlementTuple
	if len(id) == 0 || len(id) > 128 {
		return out, model.Err("needs_attention", "original settlement physical identity exceeds bound")
	}
	var it, kt, dt, rt string
	var il, kl, dl, rl int64
	var ib, kb, db, rb []byte
	err := q.QueryRow(`WITH limits(im,km,rm,tm) AS (VALUES(?,?,?,?)), r AS (
 SELECT id,request_key,data,run,typeof(id) it,typeof(request_key) kt,typeof(data) dt,typeof(run) rt,
 length(CAST(id AS BLOB)) il,coalesce(length(CAST(request_key AS BLOB)),0) kl,
 length(CAST(data AS BLOB)) dl,coalesce(length(CAST(run AS BLOB)),0) rl FROM leases WHERE id=?)
 SELECT it,il,CASE WHEN it='text' AND il BETWEEN 1 AND im AND il+kl+dl+rl<=tm THEN substr(CAST(id AS BLOB),1,im) END,
 kt,kl,CASE WHEN kt='text' AND kl<=km AND il+kl+dl+rl<=tm THEN substr(CAST(request_key AS BLOB),1,km) END,
 dt,dl,CASE WHEN dt='text' AND dl BETWEEN 1 AND rm AND il+kl+dl+rl<=tm THEN substr(CAST(data AS BLOB),1,rm) END,
 rt,rl,CASE WHEN rt='text' AND rl BETWEEN 1 AND rm AND il+kl+dl+rl<=tm THEN substr(CAST(run AS BLOB),1,rm) END FROM r,limits`,
		128, 128, originalSettlementRecordBytes, originalSettlementTupleBytes, id).Scan(&it, &il, &ib, &kt, &kl, &kb, &dt, &dl, &db, &rt, &rl, &rb)
	if err != nil {
		return out, err
	}
	if it != "text" || il < 1 || il > 128 || int64(len(ib)) != il || string(ib) != id ||
		(dt != "text" || dl < 1 || dl > originalSettlementRecordBytes || int64(len(db)) != dl) ||
		(kt != "null" && kt != "text") || kl > 128 || int64(len(kb)) != kl ||
		(rt != "null" && rt != "text") || rl > originalSettlementRecordBytes || int64(len(rb)) != rl ||
		(rt == "text" && rl == 0) || il+kl+dl+rl > originalSettlementTupleBytes {
		return out, model.Err("needs_attention", "original settlement physical tuple type/size differs")
	}
	out.id, out.data = id, string(db)
	out.key = sql.NullString{String: string(kb), Valid: kt == "text"}
	out.run = sql.NullString{String: string(rb), Valid: rt == "text"}
	return out, nil
}

func originalSettlementLaunchMatches(row originalSettlementTuple, launch *originalSettlementLaunch) bool {
	return launch != nil && row.key == launch.key && row.run == launch.run
}

func (m *Manager) captureOriginalSettlementLaunch(l model.Lease, r RunRequest) (*originalSettlementLaunch, error) {
	row, err := readOriginalSettlementTuple(m.db, l.ID)
	if err != nil {
		return nil, err
	}
	key := sql.NullString{String: l.Request.Key, Valid: l.Request.Key != ""}
	if row.key != key || !row.run.Valid || len(r.originalRun) == 0 || len(r.originalRun) > originalSettlementRecordBytes || row.run.String != r.originalRun {
		return nil, model.Err("needs_attention", "original settlement launch tuple differs")
	}
	return &originalSettlementLaunch{key: row.key, run: row.run}, nil
}

func settlementSQLText(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

func settlementSQLType(s sql.NullString) string {
	if s.Valid {
		return "text"
	}
	return "null"
}

// The original transaction retains its inserted sequence/time before Commit.
// A lost ACK can be observed only within a complete finite post-insert journal
// window. Beyond it we retain charge, rather than scan an unbounded journal.
func observeOriginalSettlementEvent(tx *sql.Tx, id string, d *originalJobSettlement) error {
	if d.eventSequence <= 0 {
		return model.Err("needs_attention", "original completion event insertion unavailable")
	}
	var high int64
	if err := tx.QueryRow(`SELECT coalesce(max(sequence),0) FROM events`).Scan(&high); err != nil {
		return err
	}
	if high < d.eventSequence || high-d.eventSequence >= originalSettlementEventSpan {
		return model.Err("needs_attention", "original completion event window unavailable")
	}
	rows, err := tx.Query(`SELECT sequence,typeof(type),length(CAST(type AS BLOB)),CASE WHEN length(CAST(type AS BLOB))<=128 THEN substr(CAST(type AS BLOB),1,128) END,
 typeof(entity),length(CAST(entity AS BLOB)),CASE WHEN length(CAST(entity AS BLOB))<=128 THEN substr(CAST(entity AS BLOB),1,128) END,
 typeof(at),length(CAST(at AS BLOB)),CASE WHEN length(CAST(at AS BLOB))<=64 THEN substr(CAST(at AS BLOB),1,64) END,
 typeof(data),length(CAST(data AS BLOB)),CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=? THEN substr(CAST(data AS BLOB),1,?) END
 FROM events WHERE sequence>=? AND sequence<=? AND entity=? AND type=? ORDER BY sequence LIMIT 2`, originalSettlementRecordBytes, originalSettlementRecordBytes, d.eventSequence, high, id, d.kind)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var seq, kl, il, al, dl int64
		var kt, it, at, dt string
		var kb, ib, ab, db []byte
		if err = rows.Scan(&seq, &kt, &kl, &kb, &it, &il, &ib, &at, &al, &ab, &dt, &dl, &db); err != nil {
			return err
		}
		if count != 1 || seq != d.eventSequence || kt != "text" || it != "text" || at != "text" || dt != "text" ||
			kl != int64(len(kb)) || il != int64(len(ib)) || al != int64(len(ab)) || dl != int64(len(db)) ||
			string(kb) != d.kind || string(ib) != id || string(ab) != d.eventAt || string(db) != d.candidate {
			return model.Err("needs_attention", "original completion event changed or duplicated")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if count != 1 {
		return model.Err("needs_attention", "original completion event missing")
	}
	return rows.Close()
}

// Compare the whole original physical tuple and write the fixed candidate plus
// event atomically. An identical candidate alone is never a durable ACK.
func (m *Manager) commitOriginalJobSettlement(id string, d *originalJobSettlement) error {
	if d == nil || d.job == nil || d.job.settlementLaunch == nil || len(d.expected) < 1 || len(d.expected) > originalSettlementRecordBytes ||
		len(d.candidate) < 1 || len(d.candidate) > originalSettlementRecordBytes || len(d.kind) < 1 || len(d.kind) > 128 || len(d.eventAt) < 1 || len(d.eventAt) > 64 ||
		len(d.expected)+len(d.candidate)+len(d.job.settlementLaunch.run.String)+len(d.job.settlementLaunch.key.String)+len(id)+len(d.kind)+len(d.eventAt) > originalSettlementDebtBytes {
		return model.Err("needs_attention", "original settlement retained evidence exceeds bound")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := readOriginalSettlementTuple(tx, id)
	if err != nil {
		return err
	}
	if !originalSettlementLaunchMatches(row, d.job.settlementLaunch) {
		return model.Err("needs_attention", "original settlement physical launch tuple changed")
	}
	if row.data == d.candidate {
		if err = observeOriginalSettlementEvent(tx, id, d); err != nil {
			return err
		}
		return tx.Commit()
	}
	if row.data != d.expected {
		return model.Err("needs_attention", "original settlement row changed")
	}
	if d.eventSequence > 0 {
		var exists bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE sequence=? AND entity=? AND type=?)`, d.eventSequence, id, d.kind).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return model.Err("needs_attention", "original completion event exists without its candidate")
		}
	}
	launch := d.job.settlementLaunch
	result, err := tx.Exec(`UPDATE leases SET data=? WHERE id=? AND typeof(id)='text' AND data=? AND typeof(data)='text' AND request_key IS ? AND typeof(request_key)=? AND run IS ? AND typeof(run)=?`, d.candidate, id, d.expected, settlementSQLText(launch.key), settlementSQLType(launch.key), settlementSQLText(launch.run), settlementSQLType(launch.run))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return model.Err("needs_attention", "original settlement comparison lost")
	}
	result, err = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, d.kind, id, d.eventAt, d.candidate)
	if err != nil {
		return err
	}
	d.eventSequence, err = result.LastInsertId()
	if err != nil {
		return err
	}
	return tx.Commit()
}
