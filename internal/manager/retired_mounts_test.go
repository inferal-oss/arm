package manager

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func TestRetiredMountRemovalPreservesOrdinaryLeaseHistoryOnReopen(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	// This checks durable history, not physical headroom admission.
	memoryReading(m, 1024, 1024, 0)
	lease, err := m.Acquire(model.Request{Reason: "ordinary preserved promise", Key: "ordinary", TTL: time.Minute, Resources: model.Resources{Memory: 1}}, nil)
	if err != nil {
		_ = m.Close()
		t.Fatal(err)
	}
	var original string
	var events int
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE entity=?`, lease.ID).Scan(&events); err != nil || events == 0 {
		t.Fatal("missing original journal", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	}()
	var current string
	var currentEvents, retired int
	if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&current); err != nil || current != original {
		t.Fatal("ordinary promise changed", err)
	}
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE entity=?`, lease.ID).Scan(&currentEvents); err != nil || currentEvents != events {
		t.Fatal("ordinary history changed", err)
	}
	if err = m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name GLOB 'share_*' OR name GLOB 'snapshot_*'`).Scan(&retired); err != nil || retired != 0 {
		t.Fatal("ordinary open initialized retired namespaces", err)
	}
}

func TestRetiredMountStartupRefusesBeforeRecoveryAndPreservesDuties(t *testing.T) {
	for _, name := range []string{"partial-share-table", "partial-ordinary-schema", "snapshot-marker", "aliased-table", "queued-internal-lease", "active-internal-lease", "terminal-internal-lease", "orphan-planned-storage"} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			capacity := model.Resources{CPU: 1, Memory: 1024, Disk: 2048}
			m, err := Open(home, capacity)
			if err != nil {
				t.Fatal(err)
			}
			// Keep the real memory promise independent of current host pressure.
			memoryReading(m, 1024, 1024, 0)
			lease, err := m.Acquire(model.Request{Reason: "retired fixture obligation", Key: "retained", TTL: time.Minute, Resources: model.Resources{Memory: 1}}, nil)
			if err != nil {
				_ = m.Close()
				t.Fatal(err)
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var query string
			switch name {
			case "partial-share-table", "partial-ordinary-schema":
				query = `CREATE TABLE share_consumers (data TEXT)`
			case "snapshot-marker":
				query = `INSERT INTO metadata VALUES ('snapshot_copy_schema','2')`
			case "aliased-table":
				query = `CREATE TABLE ShArE_future (data TEXT)`
			default:
				lease.Request.Kind = "share-consumer"
				lease.State = strings.TrimSuffix(name, "-internal-lease")
				if name == "orphan-planned-storage" {
					lease.State, lease.Request.Kind = "queued", "storage"
					lease.Artifact = &model.Artifact{Phase: "creating", Policy: "snapshot-reservation-no-work/v1; no allocation attempted"}
				}
				body, e := json.Marshal(lease)
				if e != nil {
					t.Fatal(e)
				}
				if _, err = db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(body), lease.ID); err != nil {
					t.Fatal(err)
				}
			}
			if query != "" {
				if _, err = db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if name == "partial-ordinary-schema" {
				if _, err = db.Exec(`DROP TABLE agents`); err != nil {
					t.Fatal(err)
				}
			}
			const catalog = `SELECT group_concat(entry,'|') FROM (SELECT type||':'||name||':'||coalesce(sql,'') AS entry FROM sqlite_schema ORDER BY name)`
			var schemaBefore string
			if err = db.QueryRow(catalog).Scan(&schemaBefore); err != nil {
				t.Fatal(err)
			}
			var before string
			if err = db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(home, "retained-native-file")
			if err = os.WriteFile(retained, []byte("must not be reclaimed"), 0600); err != nil {
				t.Fatal(err)
			}
			identity, err := os.Stat(retained)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(home, capacity)
			if err == nil {
				_ = reopened.Close()
				t.Fatal("retired obligation allowed generic recovery")
			}
			if !strings.Contains(err.Error(), "unsupported retired mounting") {
				t.Fatal(err)
			}
			var schemaAfter string
			if err = db.QueryRow(catalog).Scan(&schemaAfter); err != nil || schemaAfter != schemaBefore {
				t.Fatal("refused startup changed the original schema", err)
			}
			var after string
			if err = db.QueryRow(`SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&after); err != nil || after != before {
				t.Fatalf("original lease changed: %v", err)
			}
			got, err := os.ReadFile(retained)
			current, statErr := os.Stat(retained)
			if err != nil || statErr != nil || string(got) != "must not be reclaimed" || !os.SameFile(identity, current) {
				t.Fatal("retained native file changed", err, statErr)
			}
			if _, err = os.Stat(filepath.Join(home, "storage", lease.ID)); !os.IsNotExist(err) {
				t.Fatal("startup provisioned the retired destination", err)
			}
		})
	}
}
