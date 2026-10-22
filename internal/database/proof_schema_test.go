package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
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
