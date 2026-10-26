package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/nodestop"
)

func TestStopOptionalNamespaceCannotBeErasedByOldResetProof(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "stop-reset.db"))
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
		t.Fatal("legacy exact schema positive", err)
	}
	tx.Rollback()
	if _, err = db.Exec(nodestop.Tables[0]); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = verifyResetSchema(context.Background(), tx); err == nil || !strings.Contains(err.Error(), "node_stop_schema") {
		t.Fatal("new stop namespace was ignored by destructive proof", err)
	}
}
