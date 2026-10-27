package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func workspaceFormatFixture(t *testing.T, statements ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	// Deliberately no affinities or constraints: the refusal must not depend on
	// a healthy schema having already rejected corruption.
	for _, q := range append([]string{`CREATE TABLE metadata(key,value)`, `CREATE TABLE leases(data)`}, statements...) {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestWorkspaceFormatBaselineReadOnlyCompatibility(t *testing.T) {
	for _, marker := range []string{"absent", "1"} {
		t.Run(marker, func(t *testing.T) {
			db := workspaceFormatFixture(t, `INSERT INTO leases VALUES('{"request":{"kind":"job"},"state":"terminal"}')`)
			if marker == "1" {
				if _, err := db.Exec(`INSERT INTO metadata VALUES('workspace_format','1')`); err != nil {
					t.Fatal(err)
				}
			}
			var before, after int64
			if err := db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			if err := CheckWorkspaceFormat(t.Context(), db); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("read-only baseline wrote rows")
			}
		})
	}
}

func TestWorkspaceFormatBaselineRefusesUnknownAndDamagedMarkers(t *testing.T) {
	cases := map[string]string{
		"next-format":     `INSERT INTO metadata VALUES('workspace_format','2')`,
		"future-format":   `INSERT INTO metadata VALUES('workspace_format','999')`,
		"noncanonical":    `INSERT INTO metadata VALUES('workspace_format','01')`,
		"whitespace":      `INSERT INTO metadata VALUES('workspace_format','1 ')`,
		"null":            `INSERT INTO metadata VALUES('workspace_format',NULL)`,
		"integer":         `INSERT INTO metadata VALUES('workspace_format',1)`,
		"blob":            `INSERT INTO metadata VALUES('workspace_format',X'31')`,
		"blob-key":        `INSERT INTO metadata VALUES(CAST('workspace_format' AS BLOB),'1')`,
		"case-alias":      `INSERT INTO metadata VALUES('WORKSPACE_FORMAT','1')`,
		"duplicate":       `INSERT INTO metadata VALUES('workspace_format','1'),('workspace_format','1')`,
		"duplicate-alias": `INSERT INTO metadata VALUES('workspace_format','1'),('WORKSPACE_FORMAT','1')`,
		"oversized-text":  `INSERT INTO metadata VALUES('workspace_format',printf('%.*c',1048576,'1'))`,
		"oversized-blob":  `INSERT INTO metadata VALUES('workspace_format',zeroblob(1048576))`,
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			db := workspaceFormatFixture(t, setup, `PRAGMA query_only=ON`)
			var refusal *model.Error
			if err := CheckWorkspaceFormat(t.Context(), db); !errors.As(err, &refusal) || refusal.Code != "workspace_format_unsupported" {
				t.Fatalf("missing safe format refusal: %v", err)
			}
		})
	}
}

func TestWorkspaceFormatBaselineRefusesPairsDespiteOrdinaryOrMissingMarker(t *testing.T) {
	var decoded model.Lease
	if err := json.Unmarshal([]byte(`{"reque\u017ft":{"\u212aind":"provider-prepare-worker"}}`), &decoded); err != nil || decoded.Request.Kind != "provider-prepare-worker" {
		t.Fatal("Unicode witness does not exercise actual Go field matching", decoded.Request.Kind, err)
	}
	cases := map[string]string{
		"empty-header":   `CREATE TABLE provider_preparations(id TEXT)`,
		"partial-member": `CREATE TABLE provider_preparation_members(lease TEXT)`,
		"orphan-index":   `CREATE INDEX provider_preparations_operation_key ON leases(data)`,
		"empty-view":     `CREATE VIEW provider_preparations AS SELECT data FROM leases`,
		"marker-only":    `INSERT INTO metadata VALUES('PROVIDER_PREPARATION_SCHEMA',NULL)`,
		"null-ref":       `INSERT INTO leases VALUES('{"provider_preparation":null}')`,
		"false-ref":      `INSERT INTO leases VALUES('{"provider_preparation":false}')`,
		"escaped-ref":    `INSERT INTO leases VALUES('{"provider\u005fpreparation":{}}')`,
		"alias-ref":      `INSERT INTO leases VALUES('{"PROVIDER_PREPARATION":{}}')`,
		"orphan-worker":  `INSERT INTO leases VALUES('{"request":{"kind":"provider-prepare-worker"}}')`,
		"escaped-worker": `INSERT INTO leases VALUES('{"REQUEST":{"k\u0069nd":"provider-prepare-worker"}}')`,
		"unicode-worker": `INSERT INTO leases VALUES('{"reque\u017ft":{"\u212aind":"provider-prepare-worker"}}')`,
	}
	for name, setup := range cases {
		for _, marker := range []string{"absent", "1"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				db := workspaceFormatFixture(t, setup)
				if marker == "1" {
					if _, err := db.Exec(`INSERT INTO metadata VALUES('workspace_format','1')`); err != nil {
						t.Fatal(err)
					}
				}
				if err := CheckWorkspaceFormat(t.Context(), db); err == nil {
					t.Fatal("paired evidence was treated as an ordinary ledger")
				}
			})
		}
	}
	// A nested user payload with the same spelling is not a Lease-node field.
	db := workspaceFormatFixture(t, `INSERT INTO leases VALUES('{"request":{"metadata":{"provider_preparation":"user-data"}}}')`)
	if err := CheckWorkspaceFormat(t.Context(), db); err != nil {
		t.Fatal("nested user data was treated as a lease reference", err)
	}
}

func TestWorkspaceFormatBaselineHonorsCancelledRead(t *testing.T) {
	db := workspaceFormatFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CheckWorkspaceFormat(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled format read was accepted", err)
	}
	if strings.Contains(unsupportedWorkspaceFormat().Error(), "workspace_format=") {
		t.Fatal("refusal exposed marker contents")
	}
}

func TestWorkspaceFormatBaselineRefusesGoDecodableBlobLeaseBodies(t *testing.T) {
	for name, body := range map[string]string{
		"worker":   `{"request":{"kind":"provider-prepare-worker"}}`,
		"ref":      `{"request":{"kind":"storage"},"provider_preparation":null}`,
		"ordinary": `{"request":{"kind":"execution"},"state":"released"}`,
	} {
		for _, marker := range []string{"absent", "1"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				db := workspaceFormatFixture(t)
				if _, err := db.Exec(`DROP TABLE leases; CREATE TABLE leases(data TEXT)`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO leases VALUES(?)`, []byte(body)); err != nil {
					t.Fatal(err)
				}
				if marker == "1" {
					if _, err := db.Exec(`INSERT INTO metadata VALUES('workspace_format','1')`); err != nil {
						t.Fatal(err)
					}
				}
				// Exercise the actual database/sql []byte + Go JSON path used by
				// Manager, rather than assuming BLOB cannot be decoded as a lease.
				var storageType string
				var raw []byte
				if err := db.QueryRow(`SELECT typeof(data),data FROM leases`).Scan(&storageType, &raw); err != nil || storageType != "blob" {
					t.Fatal("fixture did not retain actual BLOB storage", storageType, err)
				}
				var lease model.Lease
				if err := json.Unmarshal(raw, &lease); err != nil || lease.Request.Kind == "" {
					t.Fatal("fixture is not accepted by the actual Go decoder", err)
				}
				if err := CheckWorkspaceFormat(t.Context(), db); err == nil {
					t.Fatal("Go-decodable BLOB escaped the baseline guard")
				}
				if err := db.QueryRow(`SELECT typeof(data),data FROM leases`).Scan(&storageType, &raw); err != nil || storageType != "blob" || string(raw) != body {
					t.Fatal("refusal converted or changed the BLOB", storageType, err)
				}
			})
		}
	}
}

func TestWorkspaceFormatBaselineRefusesNoncanonicalOrdinaryLeaseStorage(t *testing.T) {
	for name, value := range map[string]any{"null": nil, "integer": int64(1), "real": 1.25, "invalid-text-json": "{"} {
		t.Run(name, func(t *testing.T) {
			db := workspaceFormatFixture(t)
			if _, err := db.Exec(`INSERT INTO leases VALUES(?)`, value); err != nil {
				t.Fatal(err)
			}
			if err := CheckWorkspaceFormat(t.Context(), db); err == nil {
				t.Fatal("unreadable lease storage was treated as an ordinary ledger")
			}
		})
	}
}
