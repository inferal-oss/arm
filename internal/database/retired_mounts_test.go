package database

import (
	"database/sql"
	"strings"
	"testing"
)

func TestRetiredMountGuardIsReadOnlyAndRefusesPartialNamespaces(t *testing.T) {
	for _, marker := range []string{"", `CREATE TABLE snapshot_copy_parts (data BLOB)`, `CREATE TABLE share_publications (data TEXT)`, `INSERT INTO metadata VALUES ('SHARE_SCHEMA','unknown')`, `INSERT INTO leases VALUES ('{"request":{"kind":"share-snapshot-work"}}')`, `INSERT INTO leases VALUES ('{"Request":{"Kind":"share-consumer"}}')`, `INSERT INTO leases VALUES ('{"Requeſt":{"Kind":"share-consumer"}}')`, `INSERT INTO leases VALUES ('not-json')`} {
		t.Run(marker, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, query := range []string{`CREATE TABLE metadata (key TEXT,value TEXT)`, `CREATE TABLE leases (data TEXT)`} {
				if _, err = db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if marker != "" {
				if _, err = db.Exec(marker); err != nil {
					t.Fatal(err)
				}
			}
			var before int
			if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			err = RefuseRetiredMountState(t.Context(), tx)
			if marker == "" && err != nil || marker != "" && (err == nil || !strings.Contains(err.Error(), "unsupported retired mounting")) {
				t.Fatal("unexpected retired-state decision", err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var after int
			if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema`).Scan(&after); err != nil || before != after {
				t.Fatal("read-only guard changed schema", before, after, err)
			}
		})
	}
}

func TestRetiredMountGuardAcceptsLargeOrdinaryLeaseMetadata(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{`CREATE TABLE metadata (key TEXT,value TEXT)`, `CREATE TABLE leases (data TEXT)`} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// The old recovery reader imposed no 1MiB lease-body maximum. A removed
	// feature must not introduce one for independent ordinary workload metadata.
	raw := `{"request":{"kind":"execution"},"cargo":{"metadata":"` + strings.Repeat("x", 2<<20) + `"}}`
	if _, err = db.Exec(`INSERT INTO leases VALUES (?)`, raw); err != nil {
		t.Fatal(err)
	}
	if err = RefuseRetiredMountState(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var got string
	if err = db.QueryRow(`SELECT data FROM leases`).Scan(&got); err != nil || got != raw {
		t.Fatal("ordinary metadata changed", err)
	}
}
