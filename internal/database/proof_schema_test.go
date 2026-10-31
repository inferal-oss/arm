package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/model"
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

func TestPressureControlOptionalSchemaIsWholeAndExact(t *testing.T) {
	for _, damage := range []string{"", "partial", "changed", "extra", "complete-uppercase", "partial-alias", "oversized-ddl", "oversized-name"} {
		t.Run(damage, func(t *testing.T) {
			db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pressure.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, query := range resetSchema {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if err := VerifyPressureControlSchema(t.Context(), db); err != nil {
				t.Fatal("absent optional group refused", err)
			}
			for i, object := range pressureControlResetSchema {
				if damage == "partial-alias" && i != 0 {
					break
				}
				ddl := object.ddl
				if damage == "complete-uppercase" || damage == "partial-alias" {
					ddl = strings.ToUpper(ddl)
				}
				if damage == "oversized-ddl" && i == 0 {
					ddl = strings.Replace(ddl, " (", strings.Repeat(" ", 4097)+"(", 1)
				}
				if _, err := db.Exec(ddl); err != nil {
					t.Fatal(err)
				}
			}
			switch damage {
			case "partial":
				_, err = db.Exec(`DROP INDEX host_pressure_controls_state`)
			case "changed":
				_, err = db.Exec(`DROP INDEX host_pressure_controls_state`)
				if err == nil {
					_, err = db.Exec(`CREATE INDEX host_pressure_controls_state ON host_pressure_controls(epoch)`)
				}
			case "extra":
				_, err = db.Exec(`CREATE INDEX host_pressure_controls_extra ON host_pressure_controls(epoch)`)
			case "oversized-name":
				_, err = db.Exec(`CREATE INDEX host_pressure_controls_` + strings.Repeat("x", 4097) + ` ON host_pressure_controls(epoch)`)
			}
			if err != nil {
				t.Fatal(err)
			}
			startupErr := VerifyPressureControlSchema(t.Context(), db)
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, proofErr := verifyResetSchema(t.Context(), tx)
			_ = tx.Rollback()
			if damage == "" && (startupErr != nil || proofErr != nil) {
				t.Fatal("exact complete optional group refused", startupErr, proofErr)
			}
			if damage != "" && (startupErr == nil || proofErr == nil) {
				t.Fatal("partial or altered group gained authority", startupErr, proofErr)
			}
		})
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
