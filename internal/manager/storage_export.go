package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/storage"
)

func exportJobKey(lease string) string       { return "export:" + lease }
func exportOperationJobKey(id string) string { return "export-operation:" + id }

func (m *Manager) initStorageExports() error {
	m.storageExportSlots = make(chan struct{}, 2)
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS storage_exports (id TEXT PRIMARY KEY, request_key TEXT UNIQUE NOT NULL, lease TEXT NOT NULL, state TEXT NOT NULL, request TEXT NOT NULL, data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS storage_exports_lease ON storage_exports(lease,state)`,
		`CREATE INDEX IF NOT EXISTS storage_exports_state ON storage_exports(state)`,
	} {
		if _, err := m.db.Exec(statement); err != nil {
			return err
		}
	}
	rows, err := m.db.Query(`SELECT data FROM storage_exports WHERE state NOT IN ('published','handed_off','needs_attention')`)
	if err != nil {
		return err
	}
	var interrupted []model.StorageExport
	for rows.Next() {
		var data []byte
		var operation model.StorageExport
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &operation)
		}
		if err != nil {
			rows.Close()
			return err
		}
		interrupted = append(interrupted, operation)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, operation := range interrupted {
		operation.Error = "Backend restarted during " + operation.State + "; no automatic copy or publication was retried"
		operation.State = "needs_attention"
		operation.Detail = "Inspect the recorded paths; reconcile an already-published bundle or explicitly hand off remaining uncertain output before reclaiming its source"
		if err := m.saveStorageExport(&operation, "storage.export_interrupted", false); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) saveStorageExport(operation *model.StorageExport, kind string, create bool) error {
	operation.Updated = time.Now().UTC()
	stored := *operation
	stored.Running = false // runtime evidence never survives a backend restart
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	request, err := json.Marshal(operation.Request)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if create {
		_, err = tx.Exec(`INSERT INTO storage_exports(id,request_key,lease,state,request,data) VALUES(?,?,?,?,?,?)`, operation.ID, operation.Request.Key, operation.Request.Lease, operation.State, string(request), string(data))
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE storage_exports SET state=?,data=? WHERE id=?`, operation.State, string(data), operation.ID)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				err = fmt.Errorf("export record disappeared; no external action authorized")
			}
		}
	}
	if err != nil {
		return err
	}
	// Keep streamed/audit phase records compact; the complete manifest and
	// immutable request are committed in the operation row in this transaction.
	summary := map[string]any{"id": operation.ID, "lease": operation.Request.Lease, "state": operation.State, "owner": operation.Owner, "owner_username": operation.OwnerUsername, "reason": operation.Request.Reason, "destination": operation.Plan.Destination, "files": len(operation.Plan.Files), "bytes": operation.Plan.Bytes, "error": operation.Error}
	if operation.Handoff != nil {
		summary["handoff"] = operation.Handoff
	}
	if err := event(tx, kind, operation.ID, summary); err != nil {
		return err
	}
	return tx.Commit()
}

// A later export may use the same source lease; completed records never inherit
// that other operation's live worker projection.
func (m *Manager) storageExportRunning(operation model.StorageExport) bool {
	return m.storageJobs[exportOperationJobKey(operation.ID)]
}

func (m *Manager) storageExport(id string) (model.StorageExport, error) {
	var operation model.StorageExport
	var data []byte
	err := m.db.QueryRow(`SELECT data FROM storage_exports WHERE id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return operation, model.Err("not_found", "storage export operation does not exist")
	}
	if err == nil {
		err = json.Unmarshal(data, &operation)
	}
	operation.Running = m.storageExportRunning(operation)
	return operation, err
}

func (m *Manager) storageExportRetry(request model.StorageExportRequest) (model.StorageExport, bool, error) {
	var operation model.StorageExport
	var saved, data []byte
	err := m.db.QueryRow(`SELECT request,data FROM storage_exports WHERE request_key=?`, request.Key).Scan(&saved, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return operation, false, nil
	}
	if err != nil {
		return operation, false, err
	}
	current, err := json.Marshal(request)
	if err != nil {
		return operation, false, err
	}
	if !bytes.Equal(saved, current) {
		return operation, false, model.Err("idempotency_conflict", "export key already belongs to a different request; inspect the existing export before choosing a new key")
	}
	err = json.Unmarshal(data, &operation)
	operation.Running = m.storageExportRunning(operation)
	return operation, true, err
}

// Caller holds m.mu. Uncertain external output retains an explicit lock until
// reconciliation or operator handoff; a saved phase is not proof of live work.
func (m *Manager) storageExportBusy(lease string) (string, error) {
	var id string
	err := m.db.QueryRow(`SELECT id FROM storage_exports WHERE lease=? AND state NOT IN ('published','handed_off') ORDER BY rowid DESC LIMIT 1`, lease).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (m *Manager) exportSource(request model.StorageExportRequest) (model.Lease, error) {
	l, err := m.get(request.Lease)
	if err != nil {
		return l, err
	}

	if m.closing || m.draining {
		return l, model.Err("backend_stopping", "new exports are unavailable during shutdown/drain")
	}
	if len(m.storageExportSlots) == cap(m.storageExportSlots) {
		return l, model.Err("operation_busy", "both bounded export workers are occupied; inspect their outcomes and retry after they settle")
	}
	var unresolved int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM storage_exports WHERE state NOT IN ('published','handed_off')`).Scan(&unresolved); err != nil {
		return l, err
	}
	if unresolved >= 32 {
		return l, model.Err("operation_busy", "resolve or explicitly hand off existing uncertain exports before creating more")
	}
	if l.Request.Kind != "storage" || l.Owner != m.owner || l.Host != m.host || l.State != "active" || l.Expires == nil || !m.leaseLive(l) || l.Artifact == nil || l.Artifact.Phase != "owned" || m.storage == nil {
		return l, model.Err("storage_unavailable", "export requires active verified managed storage owned by this host/user")
	}
	if request.Revision != 0 && request.Revision != l.Revision {
		return l, model.Err("revision_conflict", "storage changed; inspect it before exporting")
	}
	busy, err := m.storageExportBusy(l.ID)
	if err != nil {
		return l, err
	}
	if busy != "" || m.storageJobs[l.ID] || m.storageJobs[exportJobKey(l.ID)] {
		return l, model.Err("operation_busy", "finish, reconcile or hand off the current storage operation before exporting")
	}
	leases, err := m.list()
	if err != nil {
		return l, err
	}
	if len(storageConsumers(leases, l.ID)) != 0 {
		return l, model.Err("storage_in_use", "finish or cancel all attached active and queued consumers before export")
	}
	return l, nil
}

func normalizeExportRequest(request model.StorageExportRequest, preview bool) (model.StorageExportRequest, error) {
	if request.Lease == "" || request.Revision < 0 || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 4096 || !utf8.ValidString(request.Reason) || len(request.Key) > 128 || !utf8.ValidString(request.Key) || !preview && request.Key == "" || !filepath.IsAbs(request.Destination) || len(request.Destination) > 4096 || !utf8.ValidString(request.Destination) {
		return request, model.Err("invalid_request", "export requires a storage lease, absolute new destination, reason up to 4096 bytes, and an idempotency key up to 128 bytes")
	}
	request.Destination = filepath.Clean(request.Destination)
	files, err := storage.NormalizeExportSelections(request.Files)
	if err != nil {
		return request, model.Err("invalid_request", err.Error())
	}
	request.Files = files
	return request, nil
}

func (m *Manager) StorageExportPreview(ctx context.Context, request model.StorageExportRequest) (model.StorageExportPlan, error) {
	request, err := normalizeExportRequest(request, true)
	if err != nil {
		return model.StorageExportPlan{}, err
	}
	m.mu.Lock()
	l, err := m.exportSource(request)
	p := m.storage
	if err == nil {
		m.wg.Add(1)
	}
	m.mu.Unlock()
	if err != nil {
		return model.StorageExportPlan{}, err
	}
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	plan, err := p.PlanExport(ctx, *l.Artifact, request.Destination, request.Files, l.Request.Resources.Disk, []string{m.home})
	plan.LeaseRevision, plan.SourceExpires = l.Revision, *l.Expires
	if err != nil {
		return plan, model.Err("export_refused", err.Error())
	}
	return plan, nil
}

func (m *Manager) StorageExportCreate(ctx context.Context, request model.StorageExportRequest) (model.StorageExport, error) {
	request, err := normalizeExportRequest(request, false)
	if err != nil {
		return model.StorageExport{}, err
	}
	m.mu.Lock()
	if prior, found, err := m.storageExportRetry(request); found || err != nil {
		m.mu.Unlock()
		return prior, err
	}
	l, err := m.exportSource(request)
	p := m.storage
	if err == nil {
		m.wg.Add(1)
	}
	m.mu.Unlock()
	if err != nil {
		return model.StorageExport{}, err
	}
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	plan, err := p.PlanExport(ctx, *l.Artifact, request.Destination, request.Files, l.Request.Resources.Disk, []string{m.home})
	if err != nil {
		return model.StorageExport{}, model.Err("export_refused", err.Error())
	}
	plan.LeaseRevision, plan.SourceExpires = l.Revision, *l.Expires
	m.mu.Lock()
	defer m.mu.Unlock()
	if prior, found, err := m.storageExportRetry(request); found || err != nil {
		return prior, err
	}
	current, err := m.exportSource(request)
	if err != nil {
		return model.StorageExport{}, err
	}
	if current.Revision != plan.LeaseRevision {
		return model.StorageExport{}, model.Err("revision_conflict", "storage changed during export preview; inspect and retry")
	}
	remaining, clockErr := m.leaseRemaining(current)
	if clockErr != nil || remaining <= 0 {
		return model.StorageExport{}, model.Err("storage_unavailable", "elapsed export entitlement unavailable")
	}
	select {
	case m.storageExportSlots <- struct{}{}:
	default:
		return model.StorageExport{}, model.Err("operation_busy", "both export workers became occupied; no output was created")
	}
	id := local.ID()
	operation := model.StorageExport{ID: id, Request: request, Host: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Plan: plan, State: "planned", StageName: ".arm-export-" + id, Created: time.Now().UTC(), Detail: "Bounded independent copy is queued; source cleanup and new attached consumers remain blocked until it settles"}
	operation.StagePath = filepath.Join(filepath.Dir(plan.Destination), operation.StageName)
	if err := m.saveStorageExport(&operation, "storage.export_planned", true); err != nil {
		<-m.storageExportSlots
		return operation, err
	}
	m.storageJobs[exportJobKey(l.ID)] = true
	m.storageJobs[exportOperationJobKey(operation.ID)] = true
	operation.Running = true
	worker := operation
	// The response can be encoded while the worker records output digests.
	// Keep the worker's mutable manifest separate from the returned snapshot.
	worker.Plan.Files = append([]model.ExportFile(nil), operation.Plan.Files...)
	workerCtx, workerCancel := context.WithTimeout(context.Background(), min(2*time.Minute, remaining))
	if m.storageExportCancels == nil {
		m.storageExportCancels = map[string]context.CancelFunc{}
	}
	m.storageExportCancels[l.ID] = workerCancel
	m.wg.Go(func() { m.runStorageExport(workerCtx, workerCancel, worker, p) })
	return operation, nil
}

func (m *Manager) runStorageExport(ctx context.Context, cancel context.CancelFunc, operation model.StorageExport, provider *storage.Provider) {
	defer func() { <-m.storageExportSlots }()
	defer cancel()
	go func() {
		select {
		case <-m.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	final, err := provider.Export(ctx, operation, func(next model.StorageExport) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.saveStorageExport(&next, "storage.export_"+next.State, false)
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.storageJobs, exportJobKey(operation.Request.Lease))
	delete(m.storageExportCancels, operation.Request.Lease)
	delete(m.storageJobs, exportOperationJobKey(operation.ID))
	if err != nil {
		final.State, final.Error = "needs_attention", err.Error()
		final.NextCommand = []string{"arm", "storage", "exports", "reconcile", operation.ID}
		final.Detail = "Export failed or its publication is uncertain; source and partial outputs are preserved. Reconcile a fully published bundle or explicitly hand off uncertain output. Expiry ends entitlement even while reclamation is blocked."
		_ = m.saveStorageExport(&final, "storage.export_needs_attention", false)
	}
	if lease, e := m.get(operation.Request.Lease); e == nil && lease.State == "ending" {
		m.scheduleStorage(lease.ID)
	}
}

func (m *Manager) StorageExportInspect(id string) (model.StorageExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storageExport(id)
}

func (m *Manager) StorageExports(lease string) ([]model.StorageExportSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.db.Query(`SELECT data FROM storage_exports WHERE (?='' OR lease=?) ORDER BY rowid DESC LIMIT 100`, lease, lease)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	operations := []model.StorageExportSummary{}
	for rows.Next() {
		var data []byte
		var op model.StorageExport
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, err
		}
		reason, reasonTruncated := exportSnippet(op.Request.Reason)
		failure, errorTruncated := exportSnippet(op.Error)
		operations = append(operations, model.StorageExportSummary{ID: op.ID, Key: op.Request.Key, Lease: op.Request.Lease, State: op.State, Running: m.storageExportRunning(op), Owner: op.Owner, OwnerUsername: op.OwnerUsername, Destination: op.Plan.Destination, Files: len(op.Plan.Files), Bytes: op.Plan.Bytes, Reason: reason, ReasonTruncated: reasonTruncated, Error: failure, ErrorTruncated: errorTruncated, Created: op.Created, Updated: op.Updated})
	}
	return operations, rows.Err()
}

func exportSnippet(text string) (string, bool) {
	runes := []rune(text)
	if len(runes) > 512 {
		return string(runes[:512]) + "…", true
	}
	return text, false
}

func (m *Manager) StorageExportReconcile(ctx context.Context, id string) (model.StorageExport, error) {
	m.mu.Lock()
	operation, err := m.storageExport(id)
	if err != nil {
		m.mu.Unlock()
		return operation, err
	}
	if operation.Owner != m.owner || operation.Host != m.host {
		m.mu.Unlock()
		return operation, model.Err("ownership_mismatch", "export belongs to another host/user; no recovery attempted")
	}
	if operation.State == "published" || operation.State == "handed_off" {
		m.mu.Unlock()
		return operation, nil
	}
	if operation.Running || m.closing {
		m.mu.Unlock()
		return operation, model.Err("operation_busy", "wait for the active export or backend shutdown before reconciliation")
	}
	p := m.storage
	if p == nil {
		m.mu.Unlock()
		return operation, model.Err("storage_unavailable", "verified managed provider is unavailable")
	}
	m.storageJobs[exportJobKey(operation.Request.Lease)] = true
	m.storageJobs[exportOperationJobKey(operation.ID)] = true
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	verified, verifyErr := p.ReconcileExport(ctx, operation)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.storageJobs, exportJobKey(operation.Request.Lease))
	delete(m.storageJobs, exportOperationJobKey(operation.ID))
	if verifyErr != nil {
		return operation, model.Err("export_unresolved", verifyErr.Error())
	}
	if err := m.saveStorageExport(&verified, "storage.export_reconciled", false); err != nil {
		return verified, err
	}
	m.resumeStorageAfterExport(operation.Request.Lease)
	return verified, nil
}

func (m *Manager) StorageExportHandoff(id, reason string) (model.StorageExport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, err := m.storageExport(id)
	if err != nil {
		return operation, err
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 || !utf8.ValidString(reason) {
		return operation, model.Err("invalid_request", "handoff requires a reason up to 4096 bytes")
	}
	if operation.Owner != m.owner || operation.Host != m.host {
		return operation, model.Err("ownership_mismatch", "export belongs to another host/user; no handoff attempted")
	}
	if operation.State == "handed_off" || operation.State == "published" {
		return operation, nil
	}
	if operation.Running || m.closing {
		return operation, model.Err("operation_busy", "wait for the active export before handing off uncertain output")
	}
	operation.State = "handed_off"
	operation.Handoff = &model.ExportHandoff{Actor: m.owner, ActorUsername: m.ownerUsername, At: time.Now().UTC(), Reason: reason}
	operation.Detail = storage.ExportHandoffDetail(operation) + ". Operator reason: " + reason
	operation.NextCommand = []string{"arm", "release", operation.Request.Lease}
	if err := m.saveStorageExport(&operation, "storage.export_handed_off", false); err != nil {
		return operation, err
	}
	m.resumeStorageAfterExport(operation.Request.Lease)
	return operation, nil
}

func (m *Manager) resumeStorageAfterExport(id string) {
	lease, err := m.get(id)
	if err != nil {
		return
	}
	if lease.State == "ending" {
		m.scheduleStorage(id)
	} else if lease.State == "needs_attention" && lease.Outcome == "expired" {
		_, _ = m.storageCleanup(id)
	}
}

// StorageRetain shortens an existing managed lifetime and records why artifacts
// remain useful. Increasing entitlement still requires ordinary renewal.
func (m *Manager) StorageRetain(id string, revision int, ttl time.Duration, reason string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(id)
	if err != nil {
		return l, err
	}

	if m.closing || m.draining {
		return l, model.Err("backend_stopping", "retention changes are unavailable during shutdown/drain")
	}
	if ttl <= 0 || ttl > 24*time.Hour || revision <= 0 || strings.TrimSpace(reason) == "" || len(reason) > 4096 || !utf8.ValidString(reason) {
		return l, model.Err("invalid_request", "retention requires a positive reviewed revision, TTL at most 24h and reason up to 4096 bytes")
	}
	if l.Revision != revision {
		return l, model.Err("revision_conflict", "storage changed; inspect it before changing retention")
	}
	if l.Request.Kind != "storage" || l.State != "active" || l.Artifact == nil || l.Artifact.Phase != "owned" || l.Expires == nil || !m.leaseLive(l) || l.Owner != m.owner || l.Host != m.host {
		return l, model.Err("storage_unavailable", "retention changes require active owned managed storage")
	}
	if m.storage == nil {
		return l, model.Err("storage_unavailable", "verified storage provider is unavailable")
	}
	if err := m.storage.Check(*l.Artifact); err != nil {
		return l, model.Err("storage_unavailable", err.Error())
	}
	sample, clockErr := m.readLeaseClock()
	if clockErr != nil {
		return l, clockErr
	}
	remaining, clockErr := l.Clock.Remaining(sample, l.Expires)
	if clockErr != nil || remaining <= 0 {
		return l, model.Err("storage_unavailable", "elapsed entitlement unavailable")
	}
	deadline, clockErr := leaseclock.New(sample, ttl)
	if clockErr != nil {
		return l, clockErr
	}
	expires := deadline.Wall
	if ttl > remaining {
		return l, model.Err("renewal_required", "retain only shortens the current deadline; use ordinary renew when more time is needed")
	}
	busy, err := m.storageExportBusy(id)
	if err != nil {
		return l, err
	}
	if busy != "" || m.storageJobs[id] {
		return l, model.Err("operation_busy", "finish or resolve the storage operation before changing retention")
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	for _, consumer := range ls {
		if consumer.Request.StorageLease != id || !consuming(consumer.State) && consumer.State != "queued" {
			continue
		}
		_, childClockErr := consumer.Clock.Remaining(sample, consumer.Expires)
		if childClockErr != nil || consumer.Clock.End > deadline.End {
			return l, model.Err("storage_in_use", "retention cannot shorten past an attached consumer's deadline or queued lifetime")
		}
	}
	l.Expires = &expires
	l.Clock = deadline
	l.Artifact.Retention = &model.StorageRetention{At: time.Now().UTC(), Expires: expires, Reason: reason}
	l.Revision++
	return l, m.put(l, "storage.retained")
}
