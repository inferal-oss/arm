package manager

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

func TestForwardUnixPairGuardRetainsBothChargesForOldReader(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "arm")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	storage, err := m.Acquire(model.Request{Kind: "storage", Key: "forward-unix-storage:guard-fixture", Reason: "forward Unix original socket metadata", Resources: model.Resources{Disk: 1 << 20}, TTL: 2 * time.Minute}, nil)
	if err != nil || storage.State != "active" {
		t.Fatalf("charged storage not active: %+v %v", storage, err)
	}
	joined := make(chan struct{})
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "forward Unix guard fixture", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		t.Fatal(err)
	}
	// The future issuer must make this attachment and pair insertion atomic.
	// Here the two writes are synthetic so the retention triggers can be tested
	// without publishing an issuer or creating a native Unix socket.
	m.mu.Lock()
	source, err := m.get(h.LeaseID())
	if err == nil {
		source.Request.StorageLease = storage.ID
		source.Revision++
		err = m.put(source, "forward.unix_guard_fixture_attachment")
	}
	if err == nil {
		h.request, h.result = source.Request, source
		_, err = m.db.Exec(`INSERT INTO forward_unix_pairs(source_id,storage_id,phase,source_key,storage_key,socket_name,disk_bytes,data) VALUES(?,?,?,?,?,?,?,?)`, source.ID, storage.ID, "planned", source.Request.Key, storage.Request.Key, "forward-"+strings.Repeat("e", 32)+".sock", storage.Request.Resources.Disk, `{}`)
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(storage.ID, "cancelled"); err == nil {
		t.Fatal("unfinished pair allowed storage cleanup")
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err == nil {
		t.Fatal("unfinished pair allowed original source settlement")
	}
	if status, e := m.Status(); e != nil || status.Reserved.CPU < .01 || status.Reserved.Disk < 1<<20 {
		t.Fatalf("unfinished pair lost a charge: %+v %v", status.Reserved, e)
	}
	_ = m.Close() // Durable pair intentionally remains unfinished.
	offline, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	proof, proofErr := database.ProveDatabaseSettled(t.Context(), offline, m.owner)
	if err = offline.Close(); err != nil {
		t.Fatal(err)
	}
	if proof.Settled || proofErr == nil && proof.Complete {
		t.Fatalf("reset proof accepted unfinished pair: %+v %v", proof, proofErr)
	}

	// A prior binary has no pair-aware Go code. The database guard still
	// prevents its ordinary SQL settlement and storage deletion transitions.
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, attempt := range []struct{ id, query string }{
		{source.ID, `UPDATE leases SET data=json_set(data,'$.state','terminal') WHERE id=?`},
		{source.ID, `UPDATE leases SET data=json_set(data,'$.request.resources.cpu',0) WHERE id=?`},
		{storage.ID, `UPDATE leases SET data=json_set(data,'$.state','ending') WHERE id=?`},
		{storage.ID, `UPDATE leases SET data=json_set(data,'$.request.resources.disk_bytes',0) WHERE id=?`},
		{source.ID, `DELETE FROM leases WHERE id=?`},
		{storage.ID, `DELETE FROM leases WHERE id=?`},
	} {
		if _, err := db.Exec(attempt.query, attempt.id); err == nil {
			t.Fatalf("old-reader SQL changed unfinished pair member %s", attempt.id)
		}
	}
	if _, err = Open(home, model.Resources{CPU: 1, Memory: 16 << 20, Disk: 8 << 20}); errorCode(err) != "forward_unix_pair_unavailable" {
		t.Fatalf("this cut adopted an unfinished pair without an issuer/reader: %v", err)
	}
}
