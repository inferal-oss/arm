package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"getarm.org/internal/model"
)

// Preserve physical types, duplicate rows and raw bytes, rather than comparing
// decoded JSON which could erase the unsupported evidence being tested.
func workerFormatSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT entry FROM (
 SELECT 'schema:'||type||':'||hex(name)||':'||hex(tbl_name)||':'||hex(sql) AS entry FROM sqlite_schema
 UNION ALL SELECT 'metadata:'||typeof(key)||':'||hex(key)||':'||typeof(value)||':'||hex(value) FROM metadata
 UNION ALL SELECT 'lease:'||typeof(data)||':'||hex(data) FROM leases
 ) ORDER BY entry`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func workerFormatMarker(t *testing.T, db *sql.DB, marker string) {
	t.Helper()
	if marker == "absent" {
		return
	}
	if _, err := db.Exec(`INSERT INTO metadata VALUES('workspace_format',?)`, marker); err != nil {
		t.Fatal(err)
	}
	if marker == "3" {
		if _, err := db.Exec(`INSERT INTO metadata VALUES('storage_pools','[]')`); err != nil {
			t.Fatal(err)
		}
	}
}

func assertWorkerFormatRefusal(t *testing.T, db *sql.DB) {
	t.Helper()
	before := workerFormatSnapshot(t, db)
	var changes int
	if err := db.QueryRow(`SELECT total_changes()`).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	var refusal *model.Error
	if err := CheckWorkspaceFormat(t.Context(), db); !errors.As(err, &refusal) || refusal.Code != "workspace_format_unsupported" {
		t.Fatal("reserved worker state escaped format refusal", err)
	}
	var afterChanges int
	if err := db.QueryRow(`SELECT total_changes()`).Scan(&afterChanges); err != nil {
		t.Fatal(err)
	}
	if changes != afterChanges || !reflect.DeepEqual(before, workerFormatSnapshot(t, db)) {
		t.Fatal("refusal changed logical bytes, rows or schema")
	}
}

func TestResidentWorkerFormatRefusesPhysicalNamespaces(t *testing.T) {
	cases := map[string][]string{
		"empty-table":   {`CREATE TABLE resident_worker(id)`},
		"partial-table": {`CREATE TABLE resident_worker_lifetimes(data)`},
		"future-table":  {`CREATE TABLE RESIDENT_WORKER_future(data)`},
		"unicode-table": {`CREATE TABLE "reſident_worKer_future"(data)`},
		"index-name":    {`CREATE INDEX resident_worker_orphan ON leases(data)`},
		"trigger-name":  {`CREATE TRIGGER resident_worker_orphan AFTER INSERT ON leases BEGIN SELECT 1; END`},
		"view-name":     {`CREATE VIEW resident_worker AS SELECT data FROM leases`},
		// A private corrupt fixture isolates tbl_name from the object's name.
		"owner-table":        {`CREATE INDEX ordinary_index ON leases(data)`, `PRAGMA writable_schema=ON`, `UPDATE sqlite_schema SET tbl_name='RESIDENT_WORKER_orphan' WHERE name='ordinary_index'`, `PRAGMA writable_schema=OFF`},
		"metadata-root":      {`INSERT INTO metadata VALUES('resident_worker',NULL)`},
		"metadata-partial":   {`INSERT INTO metadata VALUES('resident_worker_schema','')`},
		"metadata-case":      {`INSERT INTO metadata VALUES('RESIDENT_WORKER_future',1)`},
		"metadata-unicode":   {`INSERT INTO metadata VALUES('reſident_worKer_future',X'00')`},
		"metadata-blob-key":  {`INSERT INTO metadata VALUES(CAST('resident_worker_schema' AS BLOB),NULL)`},
		"metadata-duplicate": {`INSERT INTO metadata VALUES('resident_worker_schema',''),('RESIDENT_WORKER_SCHEMA','')`},
	}
	for name, setup := range cases {
		for _, marker := range []string{"absent", "1", "3"} {
			t.Run(name+"/"+marker, func(t *testing.T) {
				db := workspaceFormatFixture(t, setup...)
				workerFormatMarker(t, db, marker)
				assertWorkerFormatRefusal(t, db)
			})
		}
	}
}

func TestResidentWorkerFormatRefusesLeaseEvidence(t *testing.T) {
	// Verify the Unicode witnesses against the actual ordinary Go decoder.
	var decoded model.Lease
	if err := json.Unmarshal([]byte(`{"reque\u017ft":{"\u212aind":"resident-worker"}}`), &decoded); err != nil || decoded.Request.Kind != "resident-worker" {
		t.Fatal("kind witness does not exercise Go field folding", err)
	}
	var attachment struct {
		Worker json.RawMessage `json:"resident_worker"`
	}
	if err := json.Unmarshal([]byte(`{"re\u017fident_wor\u212aer":false}`), &attachment); err != nil || string(attachment.Worker) != "false" {
		t.Fatal("attachment witness does not exercise Go field folding", err)
	}
	cases := map[string]string{
		"null":                      `{"resident_worker":null}`,
		"false":                     `{"resident_worker":false}`,
		"empty":                     `{"resident_worker":{}}`,
		"array":                     `{"resident_worker":[]}`,
		"case":                      `{"RESIDENT_WORKER":null}`,
		"escaped":                   `{"resident\u005fworker":null}`,
		"unicode":                   `{"re\u017fident_wor\u212aer":null}`,
		"duplicate-attachment":      `{"resident_worker":{},"resident_worker":null}`,
		"worker":                    `{"request":{"kind":"resident-worker"}}`,
		"prepare":                   `{"request":{"kind":"resident-worker-prepare"}}`,
		"escaped-kind":              `{"REQUEST":{"k\u0069nd":"resident-worker-prepare"}}`,
		"unicode-kind":              `{"reque\u017ft":{"\u212aind":"resident-worker"}}`,
		"duplicate-kind-first":      `{"request":{"kind":"resident-worker","kind":"execution"}}`,
		"duplicate-kind-last":       `{"request":{"kind":"execution","kind":"resident-worker-prepare"}}`,
		"duplicate-request-first":   `{"request":{"kind":"resident-worker"},"request":{"kind":"execution"}}`,
		"duplicate-request-last":    `{"request":{"kind":"execution"},"REQUEST":{"kind":"resident-worker-prepare"}}`,
		"duplicate-unicode-request": `{"reque\u017ft":{"\u212aind":"resident-worker"},"request":null}`,
	}
	for name, body := range cases {
		for _, marker := range []string{"absent", "1", "3"} {
			for _, blob := range []bool{false, true} {
				storage := "text"
				var value any = body
				if blob {
					storage, value = "blob", []byte(body)
				}
				t.Run(name+"/"+marker+"/"+storage, func(t *testing.T) {
					db := workspaceFormatFixture(t)
					if _, err := db.Exec(`INSERT INTO leases VALUES(?)`, value); err != nil {
						t.Fatal(err)
					}
					workerFormatMarker(t, db, marker)
					assertWorkerFormatRefusal(t, db)
				})
			}
		}
	}
}

func TestResidentWorkerFormatPreservesOrdinaryAndNamedControls(t *testing.T) {
	for _, marker := range []string{"absent", "1", "3"} {
		t.Run(marker, func(t *testing.T) {
			db := workspaceFormatFixture(t,
				`CREATE TABLE ordinary_resident_worker_notes(data)`,
				`INSERT INTO metadata VALUES('ordinary_resident_worker_note','user data')`,
				`INSERT INTO leases VALUES('{"request":{"kind":"execution","metadata":{"resident_worker":null,"kind":"resident-worker"}}}')`)
			workerFormatMarker(t, db, marker)
			if marker == "3" {
				if _, err := db.Exec(`INSERT INTO leases VALUES('{"storage_binding":{},"request":{"storage_pool":"named"}}')`); err != nil {
					t.Fatal(err)
				}
			}
			before := workerFormatSnapshot(t, db)
			if _, err := db.Exec(`PRAGMA query_only=ON`); err != nil {
				t.Fatal(err)
			}
			if err := CheckWorkspaceFormat(t.Context(), db); err != nil {
				t.Fatal("ordinary/named control refused", err)
			}
			if !reflect.DeepEqual(before, workerFormatSnapshot(t, db)) {
				t.Fatal("control changed")
			}
		})
	}
}
