package manager

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/providerpreparation"
)

// A minimal real SQLite fixture deliberately has no column affinity/checks so
// readers must reject hostile SQL types themselves. It installs no pair schema.
func providerPairDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pair.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, q := range []string{
		`CREATE TABLE leases (id PRIMARY KEY,request_key,data,run)`,
		`CREATE TABLE lease_budget_bindings (lease PRIMARY KEY,data)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func providerPairInsert(t *testing.T, db *sql.DB, r providerPairLeaseRow) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO leases VALUES(?,?,?,NULL)`, r.id, r.requestKey, string(r.lease)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO lease_budget_bindings VALUES(?,?)`, r.id, string(r.budget)); err != nil {
		t.Fatal(err)
	}
}

func providerPairRead(t *testing.T, db *sql.DB, ctx context.Context, id string) (providerPairLeaseRow, error) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return readProviderPairLeaseRow(ctx, tx, id)
}

func TestProviderPairRawTuple(t *testing.T) {
	db := providerPairDB(t)
	original := providerPairLeaseRow{strings.Repeat("a", 32), "operation:worker", []byte(" {\"revision\":1,\"name\":\"\\u0061\"}\n"), []byte(" {\"path\":[]}\n")}
	providerPairInsert(t, db, original)
	actual, err := providerPairRead(t, db, t.Context(), original.id)
	if err != nil || !actual.same(original) {
		t.Fatal(actual, err)
	}
	// Semantic JSON equality cannot replace exact protected-byte equality.
	if _, err = db.Exec(`UPDATE leases SET data=? WHERE id=?`, `{"revision":1,"name":"a"}`, original.id); err != nil {
		t.Fatal(err)
	}
	changed, err := providerPairRead(t, db, t.Context(), original.id)
	if err != nil || changed.same(original) || bytes.Equal(actual.lease, changed.lease) {
		t.Fatal("same-revision replacement was adopted", err)
	}
	if !actual.same(original) {
		t.Fatal("second SQL Scan mutated a previously returned byte snapshot")
	}
}

func TestProviderPairRawTypesAndBounds(t *testing.T) {
	tests := []struct {
		name, query string
		arg         any
	}{
		{"lease blob", `UPDATE leases SET data=?`, []byte(`{}`)},
		{"lease null", `UPDATE leases SET data=?`, nil},
		{"lease integer", `UPDATE leases SET data=?`, int64(7)},
		{"lease empty", `UPDATE leases SET data=?`, ""},
		{"lease sentinel", `UPDATE leases SET data=?`, strings.Repeat("x", providerpreparation.MaxLeaseBytes+1)},
		{"key blob", `UPDATE leases SET request_key=?`, []byte("worker")},
		{"key integer", `UPDATE leases SET request_key=?`, int64(7)},
		{"key null", `UPDATE leases SET request_key=?`, nil},
		{"key sentinel", `UPDATE leases SET request_key=?`, strings.Repeat("x", 129)},
		{"key control", `UPDATE leases SET request_key=?`, "bad\nkey"},
		{"id blob", `UPDATE leases SET id=?`, []byte(strings.Repeat("a", 32))},
		{"id integer", `UPDATE leases SET id=?`, int64(7)},
		{"id null", `UPDATE leases SET id=?`, nil},
		{"run empty", `UPDATE leases SET run=?`, ""},
		{"run json null", `UPDATE leases SET run=?`, "null"},
		{"run blob", `UPDATE leases SET run=?`, []byte{}},
		{"budget blob", `UPDATE lease_budget_bindings SET data=?`, []byte(`{}`)},
		{"budget null", `UPDATE lease_budget_bindings SET data=?`, nil},
		{"budget integer", `UPDATE lease_budget_bindings SET data=?`, int64(7)},
		{"budget empty", `UPDATE lease_budget_bindings SET data=?`, ""},
		{"budget sentinel", `UPDATE lease_budget_bindings SET data=?`, strings.Repeat("x", providerpreparation.MaxBudgetBytes+1)},
		{"budget id blob", `UPDATE lease_budget_bindings SET lease=?`, []byte(strings.Repeat("a", 32))},
		{"budget missing", `DELETE FROM lease_budget_bindings WHERE lease=?`, strings.Repeat("a", 32)},
		{"lease missing", `DELETE FROM leases WHERE id=?`, strings.Repeat("a", 32)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := providerPairDB(t)
			r := providerPairLeaseRow{strings.Repeat("a", 32), "worker", []byte(`{}`), []byte(`{}`)}
			providerPairInsert(t, db, r)
			if _, err := db.Exec(tt.query, tt.arg); err != nil {
				t.Fatal(err)
			}
			got, err := providerPairRead(t, db, t.Context(), r.id)
			if err == nil || got.bounded() {
				t.Fatal("damaged tuple yielded bounded row", got, err)
			}
		})
	}
	t.Run("exact maxima", func(t *testing.T) {
		db := providerPairDB(t)
		r := providerPairLeaseRow{strings.Repeat("a", 32), strings.Repeat("k", 128), bytes.Repeat([]byte("x"), providerpreparation.MaxLeaseBytes), bytes.Repeat([]byte("b"), providerpreparation.MaxBudgetBytes)}
		providerPairInsert(t, db, r)
		got, err := providerPairRead(t, db, t.Context(), r.id)
		if err != nil || !got.same(r) {
			t.Fatal("exact byte bound refused", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		db := providerPairDB(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, err := providerPairRead(t, db, ctx, strings.Repeat("a", 32))
		if err == nil || got.bounded() {
			t.Fatal(got, err)
		}
	})
}
