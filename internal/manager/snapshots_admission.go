package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/snapshotlease"
)

// reserveSnapshot is deliberately private and starts NO planner/copy/native
// worker. A later reviewed CAS+original owner is required before any such work.
func (m *Manager) reserveSnapshot(ctx context.Context, q snapshotlease.Request) (result snapshotlease.Record, resultErr error) {
	var callRequest []byte
	defer func() {
		m.traceSnapshotMethod("reserve-call-return", snapshotTraceCut{Action: "reserve", Request: callRequest, Key: q.Key, Record: &result, APIError: snapshotTraceError(resultErr)})
	}()
	var zero snapshotlease.Record
	if e := q.Validate(); e != nil {
		return zero, e
	}
	encoded, e := snapshotlease.Canonical(q, snapshotlease.MaxRequestBytes)
	if e != nil {
		return zero, e
	}
	q, e = snapshotlease.Decode[snapshotlease.Request](encoded, snapshotlease.MaxRequestBytes)
	if e != nil {
		return zero, e
	}
	m.mu.Lock()
	if e = m.snapshotPolicyError(); e != nil {
		m.mu.Unlock()
		return zero, e
	}
	if e = ctx.Err(); e != nil {
		m.mu.Unlock()
		return zero, e
	}
	q.Budget = m.canonicalBudgetSelector(q.Budget)
	encoded, e = snapshotlease.Canonical(q, snapshotlease.MaxRequestBytes)
	callRequest = encoded
	if e != nil {
		m.mu.Unlock()
		return zero, e
	}
	if saved, present, lookupErr := m.snapshotKnownRequestLocked(q.Key, encoded); present {
		m.mu.Unlock()
		return saved, lookupErr
	}
	if e = m.snapshotCancelledLocked(ctx, q.Key); e != nil {
		m.mu.Unlock()
		return zero, e
	}
	source, e := m.snapshotSource(ctx, q, "")
	if e != nil {
		m.mu.Unlock()
		return zero, e
	}
	if len(m.snapshots.controls) >= 2 {
		m.mu.Unlock()
		return zero, model.Err("operation_busy", "both snapshot preplanning control slots are retained")
	}
	active := 0
	for _, r := range m.snapshots.state.Copies {
		if r.Phase == "reserved-no-work" {
			active++
		}
	}
	if active+len(m.snapshots.controls) >= 2 {
		m.mu.Unlock()
		return zero, model.Err("operation_busy", "both snapshot reservation slots are charged")
	}
	provider := m.storage
	control := &snapshotPreflight{source: source.ID, provider: provider}
	key := local.ID()
	m.snapshots.controls[key] = control
	m.wg.Add(1)
	m.traceSnapshotLocked("preflight-registered", key)
	m.mu.Unlock()
	defer m.finishSnapshotPreflight(key, control)
	preflight := m.snapshots.preflight
	var observed = control.observation
	if preflight == nil {
		observed, e = provider.SnapshotReservationPreflight(ctx, *source.Artifact)
	} else {
		observed, e = preflight(ctx, provider, *source.Artifact)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e = m.snapshotPreflightResult(control, observed, e); e != nil {
		return zero, e
	}
	if e = m.snapshotPolicyError(); e != nil {
		return zero, e
	}
	if e = ctx.Err(); e != nil {
		return zero, e
	}
	// Another source's off-lock preflight may have admitted this same key.
	// Resolve it under the original manager lock before source/capacity checks
	// or any SQL insert, so a known conflict is never a raw UNIQUE failure.
	if saved, present, lookupErr := m.snapshotKnownRequestLocked(q.Key, encoded); present {
		return saved, lookupErr
	}
	if e = m.snapshotCancelledLocked(ctx, q.Key); e != nil {
		return zero, e
	}
	current, e := m.snapshotSource(ctx, q, key)
	if e != nil {
		return zero, e
	}
	if provider != m.storage || !snapshotSameSource(source, current) || observed.Provider != provider.Identity() || observed.Source != *current.Artifact.Identity {
		return zero, model.Err("revision_conflict", "source/provider changed during original preflight")
	}
	return m.admitSnapshotLocked(ctx, q, encoded, current, observed.Provider)
}

// Caller holds Manager.mu. Exact saved request bytes include the source, policy,
// selections, budget and lifetime; finding a key never starts another operation.
func (m *Manager) snapshotKnownRequestLocked(key string, encoded []byte) (snapshotlease.Record, bool, error) {
	for id, saved := range m.snapshots.state.Copies {
		if saved.Key != key {
			continue
		}
		if !bytes.Equal(encoded, m.snapshots.state.RequestRaw[id]) {
			return snapshotlease.Record{}, true, model.Err("idempotency_conflict", "snapshot key already names another immutable request")
		}
		return saved, true, nil
	}
	return snapshotlease.Record{}, false, nil
}

func (m *Manager) snapshotCancelledLocked(ctx context.Context, key string) error {
	var n int
	e := m.db.QueryRowContext(ctx, `SELECT count(*) FROM cancelled_requests WHERE key=?`, snapshotlease.LeaseKey(m.host, key, "request")).Scan(&n)
	if e != nil {
		return e
	}
	if n != 0 {
		return model.Err("request_cancelled", "snapshot key was durably cancelled before admission")
	}
	return nil
}

func (m *Manager) admitSnapshotLocked(ctx context.Context, q snapshotlease.Request, request []byte, source model.Lease, provider model.FileIdentity) (snapshotlease.Record, error) {
	var zero snapshotlease.Record
	b := m.snapshots
	active := 0
	for _, saved := range b.state.Copies {
		if saved.Phase == "reserved-no-work" {
			active++
		}
	}
	if active >= 2 {
		return zero, model.Err("operation_busy", "both snapshot reservation slots remain charged at admission")
	}
	if len(b.state.Copies) >= snapshotlease.MaxCopies || b.state.RequestBytes+len(request) > snapshotlease.MaxRequestTotal || len(b.state.Journal)+b.state.ReservedSlots+2 > snapshotlease.MaxJournalRows || b.state.JournalBytes+(b.state.ReservedSlots+2)*snapshotlease.MaxJournalRecord > snapshotlease.MaxJournalBytes {
		return zero, model.Err("snapshot_capacity_exceeded", "retained request/history/finalization reservation is full")
	}
	if e := m.budgetPolicyError(); e != nil {
		return zero, e
	}
	if e := m.poolPolicyError(); e != nil {
		return zero, e
	}
	leases, e := m.list()
	if e != nil {
		return zero, e
	}
	var used model.Resources
	for _, l := range leases {
		if l.State == "queued" {
			return zero, model.Err("insufficient_capacity", "queued work has admission priority")
		}
		if consuming(l.State) {
			sum, e := addBudgetAmounts(model.BudgetAmounts{Resources: used}, model.BudgetAmounts{Resources: l.Request.Resources})
			if e != nil {
				return zero, e
			}
			used = sum.Resources
		}
	}
	used.Disk, e = diskPromises(leases)
	if e != nil {
		return zero, e
	}
	wq, dq := snapshotlease.Requests(q, m.host)
	sum, e := addBudgetAmounts(model.BudgetAmounts{Resources: wq.Resources}, model.BudgetAmounts{Resources: dq.Resources})
	if e != nil {
		return zero, e
	}
	if !m.requestFitsCeiling(sum.Resources) || !m.fitsLiveResources(sum.Resources, used) {
		return zero, model.Err("insufficient_capacity", "both snapshot promises must fit current host and dynamic headroom together")
	}
	charges, _, e := m.budgetCharges(leases)
	if e != nil {
		return zero, e
	}
	at := time.Now().UTC()
	workEnd := at.Add(q.WorkTTL)
	destinationEnd := at.Add(q.Retention)
	if source.Expires == nil || !source.Expires.After(workEnd) {
		return zero, model.Err("lease_inactive", "source no longer covers snapshot work deadline")
	}
	r := snapshotlease.Record{Schema: 1, ID: local.ID(), Authority: m.host, Owner: m.owner, Runtime: b.runtime, Key: q.Key, RequestSHA256: snapshotlease.Hash(request), RequestBytes: len(request), RequestParts: (len(request) + snapshotlease.PartBytes - 1) / snapshotlease.PartBytes, Source: q.Source, SourceRevision: q.SourceRevision, SourceIdentity: *source.Artifact.Identity, ProviderIdentity: provider, WorkLease: local.ID(), DestinationLease: local.ID(), Created: at, Updated: at, WorkExpires: workEnd, DestinationExpires: destinationEnd, Revision: 1, Phase: "reserved-no-work", FinalSlots: 1}
	work := model.Lease{ID: r.WorkLease, Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Request: wq, Created: at, Started: &at, Expires: &workEnd, Revision: 1, State: "active", Capabilities: []string{"snapshot-reservation-no-work"}}
	destination := model.Lease{ID: r.DestinationLease, Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Request: dq, Created: at, Started: &at, Expires: &destinationEnd, Revision: 1, State: "provisioning", Capabilities: []string{"snapshot-reservation-no-work"}}
	destination.ManagedDir = filepath.Join(m.home, "storage", destination.ID)
	destination.Artifact = &model.Artifact{Name: destination.ID, Path: destination.ManagedDir, Phase: "creating", Policy: "snapshot-reservation-no-work/v1; no allocation attempted", Updated: at}
	for _, l := range []*model.Lease{&work, &destination} {
		binding, e := m.resolveNewBudgetBinding(*l, q.Budget)
		if e != nil {
			return zero, e
		}
		want, e := m.requestedBudgetAmounts(l.Request)
		if e != nil {
			return zero, e
		}
		blockers, policies, e := m.budgetAdmissionBlockers(binding, want, charges, false)
		if e != nil {
			return zero, e
		}
		if len(blockers) != 0 {
			return zero, model.Err("insufficient_capacity", "both snapshot requests must fit every budget ancestor")
		}
		l.Budget = &binding
		l.BudgetPolicy = policies
		for _, id := range binding.Path {
			charges[id], e = addBudgetAmounts(charges[id], want)
			if e != nil {
				return zero, e
			}
		}
	}
	if e = r.Validate(); e != nil {
		return zero, e
	}
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer m.rollbackSnapshotLocked(tx, "admit", r.ID)
	txSource, e := snapshotlease.ReadSourceLease(ctx, tx, q.Source, m.host, m.owner, m.home)
	if e != nil {
		return zero, e
	}
	if txSource.State != "active" || !snapshotSameSource(source, txSource) {
		return zero, model.Err("revision_conflict", "source changed before atomic reservation snapshot")
	}
	if !b.state.Enabled {
		if e = snapshotlease.Initialize(ctx, tx); e != nil {
			return zero, e
		}
	}
	for _, l := range []model.Lease{work, destination} {
		raw, e := json.Marshal(l)
		if e != nil {
			return zero, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO leases(id,request_key,data,run) VALUES(?,?,?,NULL)`, l.ID, l.Request.Key, string(raw)); e != nil {
			return zero, e
		}
		if e = insertBudgetBinding(tx, l.ID, *l.Budget, "budget.bound"); e != nil {
			return zero, e
		}
		if e = event(tx, "lease.created", l.ID, l); e != nil {
			return zero, e
		}
	}
	if e = snapshotlease.WriteAdmission(ctx, tx, r, request); e != nil {
		return zero, e
	}
	m.traceSnapshotTransactionLocked(tx, "admission-writes-staged", r.ID)
	if e = event(tx, "snapshot.reserved_no_work", r.ID, r); e != nil {
		return zero, e
	}
	// Preserve the original short-circuit clock-read order. The second read is
	// absent when the first check fails, rather than synthesized by a trace reader.
	workObserved := time.Now().UTC()
	var sourceObserved *time.Time
	valid := workObserved.Before(workEnd) && source.Expires != nil
	if valid {
		observed := time.Now().UTC()
		sourceObserved = &observed
		valid = observed.Before(*source.Expires)
	}
	cut := snapshotTraceCut{Action: "admit", Deadline: &snapshotDeadlineCut{
		Source: source.ID, SourceRevision: source.Revision, RequestSHA256: r.RequestSHA256,
		WorkExpires: workEnd, SourceExpires: source.Expires,
		WorkObserved: workObserved, SourceObserved: sourceObserved,
	}}
	if !valid {
		m.traceSnapshotCut("admission-check-refused", r.ID, cut)
		return zero, model.Err("lease_inactive", "source/work deadline ended before reservation commit attempt")
	}
	m.traceSnapshotCut("admission-check-accepted", r.ID, cut)
	if e = m.commitSnapshotLocked(ctx, tx, "admit", r.ID); e != nil {
		return zero, e
	}
	state, e := snapshotlease.Load(ctx, m.db, m.host, m.owner, m.home)
	if e != nil {
		b.failure = fmt.Errorf("committed snapshot reservation revalidation failed: %w", e)
		m.traceSnapshotCut("publication-read-refused", r.ID, snapshotTraceCut{APIError: e.Error()})
		return zero, model.Err("snapshot_outcome_unknown", b.failure.Error())
	}
	b.state = state
	b.commitPending = false
	m.traceSnapshotLocked("admission-published", r.ID)
	return r, nil
}
