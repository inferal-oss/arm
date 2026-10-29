package manager

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

func TestResidentWorkerStartupRefusesBeforeSchemaOrRecovery(t *testing.T) {
	cases := map[string]string{
		"empty-namespace":    `CREATE TABLE resident_worker(id)`,
		"partial-namespace":  `CREATE TABLE resident_worker_lifetimes(data)`,
		"index-namespace":    `CREATE INDEX resident_worker_pending ON leases(data)`,
		"metadata-namespace": `INSERT INTO metadata VALUES('RESIDENT_WORKER_SCHEMA','')`,
		"blob-metadata-key":  `INSERT INTO metadata VALUES(CAST('resident_worker_schema' AS BLOB),'')`,
		"attachment":         `INSERT INTO leases VALUES('held','original','{"state":"active","resident_worker":null}',NULL)`,
		"unicode-attachment": `INSERT INTO leases VALUES('held','original','{"state":"active","re\u017fident_wor\u212aer":false}',NULL)`,
		"worker-kind":        `INSERT INTO leases VALUES('held','original','{"state":"active","request":{"kind":"resident-worker"}}',NULL)`,
		"prepare-kind":       `INSERT INTO leases VALUES('held','original','{"state":"active","request":{"kind":"resident-worker-prepare"}}',NULL)`,
		"duplicate-request":  `INSERT INTO leases VALUES('held','original','{"request":{"kind":"resident-worker"},"request":{"kind":"execution"},"state":"active"}',NULL)`,
		"blob-worker":        `INSERT INTO leases VALUES('held','original',CAST('{"state":"active","request":{"kind":"resident-worker"}}' AS BLOB),NULL)`,
	}
	for name, setup := range cases {
		for _, marker := range []string{"absent", "1", "3", "4"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				home := t.TempDir()
				if err := os.Chmod(home, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(home, "state.db")
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{`CREATE TABLE metadata(key,value)`, `INSERT INTO metadata VALUES('host','11111111111111111111111111111111')`, `CREATE TABLE leases(id,request_key,data,run)`, setup} {
					if _, err := db.Exec(q); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				if marker != "absent" {
					if _, err := db.Exec(`INSERT INTO metadata VALUES('workspace_format',?)`, marker); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				if marker == "3" {
					if _, err := db.Exec(`INSERT INTO metadata VALUES('storage_pools','[]')`); err != nil {
						db.Close()
						t.Fatal(err)
					}
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				m, err := Open(home, model.Resources{CPU: 1})
				if m != nil {
					m.Close()
					t.Fatal("reserved worker state opened")
				}
				var refusal *model.Error
				if !errors.As(err, &refusal) || refusal.Code != "workspace_format_unsupported" {
					t.Fatal("missing startup format refusal", err)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("startup refusal changed database bytes", err)
				}
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 1 || entries[0].Name() != "state.db" {
					t.Fatal("refusal created a schema/sidecar/native namespace", err)
				}
			})
		}
	}
}

func TestResidentWorkerAdmissionRefusesBeforeReconciliationOrEffects(t *testing.T) {
	for _, kind := range []string{"resident-worker", "resident-worker-prepare"} {
		for _, command := range []bool{false, true} {
			name := kind + "/lease"
			if command {
				name = kind + "/run"
			}
			t.Run(name, func(t *testing.T) {
				db, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				db.SetMaxOpenConns(1)
				for _, q := range []string{`CREATE TABLE leases(id,request_key,data,run)`, `CREATE TABLE events(data)`, `INSERT INTO leases VALUES('existing','existing','{"state":"active"}',NULL)`} {
					if _, err := db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				clockReads := 0
				m := &Manager{db: db, jobs: map[string]*job{}, leaseClockRead: func() (leaseclock.Sample, error) {
					clockReads++
					return leaseclock.Sample{}, errors.New("reconciliation must not run")
				}}
				var before int
				if err := db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				var run *RunRequest
				if command {
					run = &RunRequest{Command: []string{"must-never-start"}}
				}
				q := model.Request{Kind: kind, Key: "reserved", Reason: "reserved kind refusal", TTL: time.Minute, Queue: true, Resources: model.Resources{CPU: 1, Memory: 1}}
				lease, err := m.Acquire(q, run)
				var refusal *model.Error
				if !errors.As(err, &refusal) || refusal.Code != "invalid_request" || !strings.Contains(refusal.Message, "resident worker admission is unsupported") || lease.ID != "" {
					t.Fatal("missing early dedicated refusal", lease, err)
				}
				var changes, leases, events int
				var body string
				if err := db.QueryRow(`SELECT total_changes(),(SELECT count(*) FROM leases),(SELECT count(*) FROM events),(SELECT data FROM leases WHERE id='existing')`).Scan(&changes, &leases, &events, &body); err != nil {
					t.Fatal(err)
				}
				if clockReads != 0 || changes != before || leases != 1 || events != 0 || body != `{"state":"active"}` || len(m.jobs) != 0 || m.leaseClockError != nil {
					t.Fatal("reserved admission reconciled or changed existing work")
				}
			})
		}
	}
}
