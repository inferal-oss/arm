package database

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewMissingPressureSchemaDiagnosticKeepsProofStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-ledger.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range resetSchema {
		if strings.Contains(query, "CREATE TABLE host_pressure_") {
			continue
		}
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		_, proofErr := verifyResetSchema(context.Background(), tx)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if proofErr == nil {
			t.Fatal("missing pressure history was treated as settled schema")
		}
		message := proofErr.Error()
		if !strings.Contains(message, "host_pressure_") || strings.Contains(message, "\x1f") || strings.Contains(message, `\u001f`) {
			t.Errorf("missing schema diagnostic leaked internal token encoding: %q", message)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("schema refusal changed the disposable database", err)
	}
}
