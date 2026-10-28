package manager

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
)

func chargedReaderFixture(t *testing.T) (*Manager, string, *controlledLeaseClock, string, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "arm")
	capacity := model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20}
	clock := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "charged-reader", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithSyncSchema(home, capacity, clock.read, nil, "3")
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 8, 8, 0, 8, 0)
	memoryReading(m, 1<<30, 1<<30, 0)
	diskReading(m, 1<<30, 1<<30, 0)
	key := "charged-reader-key-12345"
	request := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-preparation\x00local\x00"+key), Reason: "durable reader control", TTL: time.Minute,
		Resources: model.Resources{CPU: 1, Memory: 768 << 20, Disk: 1 << 20}}
	scope := &syncScopeReservation{ctx: t.Context(), key: key, side: syncreconcile.Local, host: m.host, owner: m.owner,
		scopeID: "test-only-project", mountID: "test-only-mount", projectID: 42,
		projectRootID: "project", selectedRootID: "selected", homeRootID: "home", ownerEpoch: 1,
		enrollmentDigest: strings.Repeat("a", 64), ownershipDigest: strings.Repeat("b", 64), chargedHard: 1 << 20}
	joined := make(chan struct{})
	owner, err := m.acquireOwnedTransitScope(t.Context(), request, joined, syncPreparationKind, nil, scope)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	owner.recoveryReason = "fixture retains original charge"
	close(joined)
	if _, err := owner.FinishAfterJoin(); err == nil {
		m.Close()
		t.Fatal("fixture original settled")
	}
	return m, home, clock, key, owner.id
}

func TestChargedPreparingReaderRefusesExistingOperationWithoutChangingOriginal(t *testing.T) {
	m, home, clock, key, id := chargedReaderFixture(t)
	if status, err := m.InspectSyncKey(t.Context(), key, syncreconcile.Local); err != nil || status.OperationState != "preparing" || status.Lease.State != "needs_attention" {
		t.Fatalf("valid original unavailable before conflicting operation: %+v %v", status, err)
	}
	var originalRow, originalLease string
	if err := m.db.QueryRow(`SELECT preparation_data FROM sync_charged_scopes WHERE key=? AND side='local'`, key).Scan(&originalRow); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&originalLease); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`INSERT INTO sync_operations(key,side,pair,generation,record,record_sha256,manifest,state) VALUES(?,?,?,1,x'00',?,x'00','reserved')`,
		key, "local", "test-only-conflicting-pair", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readChargedScope(t.Context(), key, syncreconcile.Local); err == nil {
		t.Fatal("shared charged reader accepted operation in preparing phase")
	}
	if got, err := m.InspectSyncKey(t.Context(), key, syncreconcile.Local); err == nil {
		t.Fatalf("inspection accepted operation in preparing phase: %+v", got)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openWithSyncSchema(home, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20}, clock.read, nil, "3"); err == nil {
		reopened.Close()
		t.Fatal("startup accepted operation in preparing phase")
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	var retainedRow, retainedLease string
	if err := source.DB.QueryRow(`SELECT preparation_data FROM sync_charged_scopes WHERE key=? AND side='local'`, key).Scan(&retainedRow); err != nil {
		t.Fatal(err)
	}
	if err := source.DB.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&retainedLease); err != nil {
		t.Fatal(err)
	}
	var retained model.Lease
	if json.Unmarshal([]byte(retainedLease), &retained) != nil || retainedRow != originalRow || retainedLease != originalLease || retained.State != "needs_attention" {
		t.Fatal("refusal changed the original charged lease or row")
	}
}

func TestChargedScopeReaderRefusesMovedOrUnboundedOriginal(t *testing.T) {
	const moved = "charged-reader-moved-12345"
	cases := []struct {
		name      string
		lookupKey string
		edit      func(*testing.T, *Manager, string, string)
	}{
		{"moved charged row key", moved, func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE sync_charged_scopes SET key=? WHERE key=?`, moved, key); err != nil {
				t.Fatal(err)
			}
		}},
		{"physical request key", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET request_key=? WHERE id=?`, "changed-physical-key", id); err != nil {
				t.Fatal(err)
			}
		}},
		{"physical run scalar", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET run=1 WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
		}},
		{"physical run oversized text", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET run=? WHERE id=?`, strings.Repeat("r", 70000), id); err != nil {
				t.Fatal(err)
			}
		}},
		{"physical run BLOB", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET run=zeroblob(1048576) WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
		}},
		{"undercharged project ceiling", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE sync_charged_scopes SET charged_hard_bytes=? WHERE key=?`, (1<<20)-1, key); err != nil {
				t.Fatal(err)
			}
		}},
		{"second charged scope before scan", "", func(t *testing.T, m *Manager, key, id string) {
			insertSecondChargedScope(t, m, key)
		}},
		{"changed deadline", "", func(t *testing.T, m *Manager, key, id string) {
			mutateChargedLease(t, m.db, id, func(l *model.Lease) { expiry := l.Expires.Add(time.Minute); l.Expires = &expiry })
		}},
		{"missing elapsed clock", "", func(t *testing.T, m *Manager, key, id string) {
			mutateChargedLease(t, m.db, id, func(l *model.Lease) { l.Clock = nil })
		}},
		{"adopted PID", "", func(t *testing.T, m *Manager, key, id string) {
			mutateChargedLease(t, m.db, id, func(l *model.Lease) { l.PID = 12345 })
		}},
		{"oversized original text", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE sync_charged_scopes SET preparation_data=? WHERE key=?`, strings.Repeat("x", 70000), key); err != nil {
				t.Fatal(err)
			}
		}},
		{"BLOB original", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE sync_charged_scopes SET preparation_data=zeroblob(1048576) WHERE key=?`, key); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized scope identity", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE sync_charged_scopes SET scope_id=? WHERE key=?`, strings.Repeat("s", 1048576), key); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized current text", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, strings.Repeat("x", 70000), id); err != nil {
				t.Fatal(err)
			}
		}},
		{"BLOB current", "", func(t *testing.T, m *Manager, key, id string) {
			if _, err := m.db.Exec(`UPDATE leases SET data=zeroblob(1048576) WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, home, clock, key, id := chargedReaderFixture(t)
			tc.edit(t, m, key, id)
			lookup := key
			if tc.lookupKey != "" {
				lookup = tc.lookupKey
			}
			if got, err := m.InspectSyncKey(t.Context(), lookup, syncreconcile.Local); err == nil {
				m.Close()
				t.Fatalf("changed original was accepted for %q: %+v", lookup, got)
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := openWithSyncSchema(home, model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20}, clock.read, nil, "3"); err == nil {
				reopened.Close()
				t.Fatal("startup accepted changed charged original")
			}
		})
	}
}

func insertSecondChargedScope(t *testing.T, m *Manager, key string) {
	t.Helper()
	if _, err := m.db.Exec(`INSERT INTO sync_charged_scopes SELECT ?,side,?,preparation_revision,preparation_data,host,owner,provider,scope_id,mount_id,project_id+1,project_root_id,selected_root_id,home_root_id,owner_epoch,enrollment_digest,ownership_digest,charged_hard_bytes,initial_used_bytes,state,row_revision,final_lease FROM sync_charged_scopes WHERE key=?`, "charged-reader-second-12345", "second-preparation-lease", key); err != nil {
		t.Fatal(err)
	}
}

func mutateChargedLease(t *testing.T, db *sql.DB, id string, edit func(*model.Lease)) {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var l model.Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	edit(&l)
	changed, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changed), id); err != nil {
		t.Fatal(err)
	}
}
