package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

func TestResetSchemaRetainsSQLiteTokenAndConstraintBoundaries(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{`CREATE TABLE permissive(request_key TEXTUNIQUE)`, `CREATE TABLE constrained(request_key TEXT UNIQUE)`, `INSERT INTO permissive VALUES('same'),('same')`, `INSERT INTO constrained VALUES('same')`} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO constrained VALUES('same')`); err == nil {
		t.Fatal("fixture did not exercise actual UNIQUE constraint")
	}
	if normalizedSchema(`CREATE TABLE leases(request_key TEXTUNIQUE)`) == normalizedSchema(`CREATE TABLE leases(request_key TEXT UNIQUE)`) {
		t.Fatal("type name collided with separate constraint token")
	}
	if normalizedSchema("CREATE TABLE leases ( request_key TEXT\nUNIQUE )") != normalizedSchema("create table leases(request_key text unique)") {
		t.Fatal("insignificant SQL spacing/case did not normalize")
	}
	if normalizedSchema(`CREATE TABLE leases(value TEXT DEFAULT 'a b')`) == normalizedSchema(`CREATE TABLE leases(value TEXT DEFAULT 'ab')`) {
		t.Fatal("quoted literal whitespace was discarded")
	}
	if normalizedSchema(`CREATE TABLE leases(value TEXT DEFAULT 'a''b')`) == normalizedSchema(`CREATE TABLE leases(value TEXT DEFAULT 'ab')`) {
		t.Fatal("escaped quote literal was discarded")
	}
}

func TestResetSchemaMissingObjectsAreDeterministicReadableRefusals(t *testing.T) {
	for _, drops := range [][]string{
		{`DROP TABLE host_pressure_state`},
		{`DROP TABLE host_pressure_state`, `DROP TABLE host_pressure_outbox`, `DROP INDEX pools_name`},
	} {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "schema.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, query := range append(append([]string{}, resetSchema...), drops...) {
			if _, err = db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
		var first string
		for i := 0; i < 20; i++ {
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifyResetSchema(t.Context(), tx)
			tx.Rollback()
			var detail *model.Error
			if !errors.As(err, &detail) || detail.Code != "database_schema_unsupported" {
				t.Fatalf("missing structured schema refusal: %v", err)
			}
			for _, want := range []string{`table "host_pressure_state"`, "incomplete or incompatible", "database backup DESTINATION", "separate empty --home"} {
				if !strings.Contains(detail.Message, want) {
					t.Fatalf("missing %q: %s", want, detail.Message)
				}
			}
			if len(drops) > 1 && (!strings.Contains(detail.Message, `table "host_pressure_outbox"`) || !strings.Contains(detail.Message, `index "pools_name"`)) {
				t.Fatal("missing objects omitted", detail.Message)
			}
			if strings.ContainsAny(detail.Message, "\x1f\x1b") || strings.Contains(strings.ToLower(detail.Message), "create table") {
				t.Fatal("internal SQL presentation leaked", detail.Message)
			}
			if i > 0 && detail.Message != first {
				t.Fatal("missing schema diagnostic is nondeterministic")
			}
			first = detail.Message
		}
	}
}
func TestResetSchemaRequiresImplicitConstraintIndexes(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range resetSchema {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verifyResetSchema(context.Background(), tx); err != nil {
		t.Fatal("valid exact schema refused", err)
	}
	tx.Rollback()
	// writable_schema is used only to make a private corrupt fixture SQLite's
	// ordinary DDL refuses to create. The missing physical-index catalog must
	// not be inferred present merely from the CREATE TABLE text.
	if _, err = db.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM sqlite_schema WHERE name='sqlite_autoindex_leases_2'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA writable_schema=OFF`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = verifyResetSchema(context.Background(), tx); err == nil || !strings.Contains(err.Error(), "constraint index missing") {
		t.Fatal("missing implicit UNIQUE index accepted", err)
	}
}
