package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func exportRequestFixture(t *testing.T, m *Manager) (model.Lease, model.StorageExportRequest) {
	t.Helper()
	source, err := m.Acquire(storageRequest("export-source"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.ManagedDir, "program"), []byte("#!/bin/sh\necho preserved\n"), 0755); err != nil {
		t.Fatal(err)
	}
	return source, model.StorageExportRequest{Key: "export-operation", Lease: source.ID, Destination: filepath.Join(t.TempDir(), "output"), Files: []model.ExportSelection{{Source: "program", Path: "bin/program"}}, Reason: "Preserve requested executable before reclaiming intermediates"}
}

func awaitExport(t *testing.T, m *Manager, id string) model.StorageExport {
	t.Helper()
	var operation model.StorageExport
	storageEventually(t, func() bool {
		var err error
		operation, err = m.StorageExportInspect(id)
		return err == nil && !operation.Running && (operation.State == "published" || operation.State == "needs_attention")
	})
	return operation
}

func TestStorageExportDurabilityRetryAndExplicitRelease(t *testing.T) {
	m := newManager(t)
	source, request := exportRequestFixture(t, m)
	preview, err := m.StorageExportPreview(t.Context(), request)
	if err != nil || preview.LeaseRevision != source.Revision || preview.SourceExpires.IsZero() || preview.Bytes == 0 {
		t.Fatal("preview failed", preview, err)
	}
	before, _ := m.Events(0)
	operation, err := m.StorageExportCreate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	operation = awaitExport(t, m, operation.ID)
	if operation.State != "published" || operation.Owner != m.owner || operation.OwnerUsername != m.ownerUsername || operation.Plan.Files[0].OutputIdentity == nil || operation.Plan.Files[0].SHA256 == "" {
		t.Fatal("missing publication/ownership evidence", operation)
	}
	if current, err := m.Inspect(source.ID); err != nil || current.State != "active" {
		t.Fatal("export implicitly released the storage lease", current, err)
	}
	retry, err := m.StorageExportCreate(t.Context(), request)
	if err != nil || retry.ID != operation.ID || retry.State != "published" {
		t.Fatal("exact retry recopied output", retry, err)
	}
	request.Reason = "different intention"
	if _, err := m.StorageExportCreate(t.Context(), request); errorCode(err) != "idempotency_conflict" {
		t.Fatal("changed request reused export key", err)
	}
	operations, err := m.StorageExports(source.ID)
	if err != nil || len(operations) != 1 {
		t.Fatal("duplicate durable operation", operations, err)
	}
	after, _ := m.Events(0)
	if len(after)-len(before) != 5 {
		t.Fatal("missing/extraneous export phase audit records", len(before), len(after))
	}
	for _, event := range after[len(before):] {
		if !strings.HasPrefix(event.Type, "storage.export_") || event.Entity != operation.ID || len(event.Data) > 16384 {
			t.Fatal("unbounded or unrelated export event", event)
		}
	}
}

func TestStorageExportInterruptedPublicationRecoversWithoutRecopying(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if m != nil {
			m.Close()
		}
	}()
	source, request := exportRequestFixture(t, m)
	if _, err := m.db.Exec(`CREATE TRIGGER fail_publish BEFORE UPDATE ON storage_exports WHEN new.state='published' BEGIN SELECT RAISE(ABORT,'injected publication journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	operation, err := m.StorageExportCreate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	operation = awaitExport(t, m, operation.ID)
	if operation.State != "needs_attention" || operation.StageIdentity == nil {
		t.Fatal("uncertain publication was lost", operation)
	}
	if _, err := os.Stat(filepath.Join(operation.Plan.Destination, "bin/program")); err != nil {
		t.Fatal("external publication did not happen", err)
	}
	if _, err := m.End(source.ID, "released"); errorCode(err) != "operation_busy" {
		t.Fatal("uncertain export allowed source release", err)
	}
	if _, err := m.db.Exec(`DROP TRIGGER fail_publish`); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = nil
	m, err = Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := m.StorageExportReconcile(t.Context(), operation.ID)
	if err != nil || verified.State != "published" || verified.DestinationIdentity == nil {
		t.Fatal("publication recovery failed", verified, err)
	}
	if retry, err := m.StorageExportCreate(t.Context(), request); err != nil || retry.ID != operation.ID || retry.State != "published" {
		t.Fatal("restart retry reinvoked copy", retry, err)
	}
}

func TestStorageExportFailedCopyRequiresExplicitHandoffAndExpiryStaysEnded(t *testing.T) {
	m := newManager(t)
	source, request := exportRequestFixture(t, m)
	if _, err := m.db.Exec(`CREATE TRIGGER fail_copy BEFORE UPDATE ON storage_exports WHEN new.state='copying' BEGIN SELECT RAISE(ABORT,'injected pre-copy checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	operation, err := m.StorageExportCreate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	operation = awaitExport(t, m, operation.ID)
	if operation.State != "needs_attention" || operation.StageIdentity == nil || operation.Error == "" {
		t.Fatal("partial staging was not retained", operation)
	}
	if _, err := m.StorageExportReconcile(t.Context(), operation.ID); errorCode(err) != "export_unresolved" {
		t.Fatal("partial output was guessed complete", err)
	}
	preview, err := m.StoragePreview(t.Context(), source.ID)
	if err != nil || preview.CanClean {
		t.Fatal("unresolved export preview permits cleanup", preview, err)
	}
	if _, err := m.StorageHandoff(source.ID); errorCode(err) != "operation_busy" {
		t.Fatal("source handoff hid unresolved export", err)
	}
	// Entitlement ends at expiry even though conservative external-output
	// responsibility blocks verified reclamation pending operator disposition.
	m.mu.Lock()
	source, _ = m.get(source.ID)
	expired := time.Now().Add(-time.Second)
	source.Expires = &expired
	err = m.put(source, "fixture.expired")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.tick()
	storageEventually(t, func() bool { current, _ := m.Inspect(source.ID); return current.State == "needs_attention" })
	current, _ := m.Inspect(source.ID)
	if current.Outcome != "expired" || current.Expires.After(time.Now()) {
		t.Fatal("export silently extended entitlement", current)
	}
	handoff, err := m.StorageExportHandoff(operation.ID, "Keep partial output for inspection; allow source cleanup")
	if err != nil || handoff.State != "handed_off" {
		t.Fatal(handoff, err)
	}
	stage := filepath.Join(filepath.Dir(operation.Plan.Destination), operation.StageName)
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("export handoff deleted uncertain output", err)
	}
}

func TestStorageRetainShortensOnlyAndJournalsReason(t *testing.T) {
	m := newManager(t)
	source, _ := exportRequestFixture(t, m)
	retained, err := m.StorageRetain(source.ID, source.Revision, 30*time.Second, "One brief incremental follow-up")
	if err != nil || retained.Artifact.Retention == nil || retained.Artifact.Retention.Reason != "One brief incremental follow-up" || !retained.Expires.Before(*source.Expires) {
		t.Fatal("short retention not recorded", retained, err)
	}
	if _, err := m.StorageRetain(source.ID, source.Revision, 10*time.Second, "stale review"); errorCode(err) != "revision_conflict" {
		t.Fatal("stale retention changed lease", err)
	}
	if _, err := m.StorageRetain(source.ID, retained.Revision, time.Minute, "would extend"); errorCode(err) != "renewal_required" {
		t.Fatal("retain bypassed renewal admission", err)
	}
	request := request("queued-storage-consumer")
	request.StorageLease, request.TTL = source.ID, 10*time.Second
	consumer, err := m.Acquire(request, &RunRequest{Request: request, CWD: t.TempDir(), Command: []string{"/bin/sleep", "5"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.End(consumer.ID, "cancelled")
	if _, err := m.StorageRetain(source.ID, retained.Revision, time.Second, "would expire under consumer"); errorCode(err) != "storage_in_use" {
		t.Fatal("short retention cut through live consumer", err)
	}
}

func TestStorageExportWorkerBoundRejectsBeforeIntentOrFileChanges(t *testing.T) {
	m := newManager(t)
	_, request := exportRequestFixture(t, m)
	m.storageExportSlots <- struct{}{}
	m.storageExportSlots <- struct{}{}
	defer func() { <-m.storageExportSlots; <-m.storageExportSlots }()
	if _, err := m.StorageExportCreate(t.Context(), request); errorCode(err) != "operation_busy" {
		t.Fatal("worker bound ignored", err)
	}
	if operations, err := m.StorageExports(""); err != nil || len(operations) != 0 {
		t.Fatal("rejected work left a durable intent", operations, err)
	}
	if _, err := os.Stat(request.Destination); !os.IsNotExist(err) {
		t.Fatal("rejected work created output", err)
	}
}

func TestStorageRenewalSupersedesEarlierShortRetentionRecord(t *testing.T) {
	m := newManager(t)
	source, _ := exportRequestFixture(t, m)
	retained, err := m.StorageRetain(source.ID, source.Revision, 20*time.Second, "Only one brief follow-up")
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := m.Renew(source.ID, time.Minute)
	if err != nil || renewed.Artifact.Retention != nil || !renewed.Expires.After(*retained.Expires) {
		t.Fatal("renewal left a misleading earlier retention policy", renewed, err)
	}
}

func TestStorageExportCompletedOperationDoesNotInheritLaterWorker(t *testing.T) {
	m := newManager(t)
	_, request := exportRequestFixture(t, m)
	old, err := m.StorageExportCreate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	old = awaitExport(t, m, old.ID)
	if old.State != "published" {
		t.Fatal(old)
	}
	storageEventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return !m.storageJobs[exportJobKey(request.Lease)]
	})
	// Model a distinct later operation's durable intent and runtime worker on
	// the same source, without timing the test against large filesystem copies.
	next := old
	next.ID, next.Request.Key, next.State = "later-export", "later-key", "copying"
	m.mu.Lock()
	err = m.saveStorageExport(&next, "storage.export_copying", true)
	m.storageJobs[exportJobKey(request.Lease)] = true
	m.storageJobs[exportOperationJobKey(next.ID)] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageJobs, exportJobKey(request.Lease))
		delete(m.storageJobs, exportOperationJobKey(next.ID))
		m.mu.Unlock()
	}()
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := m.StorageExportInspect(old.ID)
	if err != nil || inspected.Running {
		t.Fatal("old inspection inherited later worker", inspected, err)
	}
	retried, err := m.StorageExportCreate(t.Context(), request)
	if err != nil || retried.Running || retried.ID != old.ID {
		t.Fatal("completed exact retry inherited later worker", retried, err)
	}
	listed, err := m.StorageExports(request.Lease)
	if err != nil || len(listed) != 2 || listed[0].ID != next.ID || !listed[0].Running || listed[1].ID != old.ID || listed[1].Running {
		t.Fatal("list misattributed live export", listed, err)
	}
}
