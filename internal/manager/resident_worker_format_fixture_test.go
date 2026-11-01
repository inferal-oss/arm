package manager

import (
	"database/sql"
	sqlite "modernc.org/sqlite"
	"strings"
	"testing"
)

// Owned official-driver SQL grammar fixture only. This creates no original
// Manager/native claim or standing grant, and does not exercise worker startup.
func workerFreshSQLFixture(t *testing.T) *sql.DB {
	t.Helper()
	connector, err := sqlite.NewConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range residentManagerBaseSchema {
		if _, err = pool.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range residentWorkerManagerSchema {
		if _, err = pool.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.ExecContext(t.Context(), `INSERT INTO metadata(key,value) VALUES('workspace_format','5')`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.ExecContext(t.Context(), `INSERT INTO resident_worker_core_state(singleton,generation,revision,phase,active_epoch,activation_slot) VALUES(1,?,0,'preparing',NULL,NULL)`, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	return pool
}
func TestWorkerFreshFormatOfficialDriverInventoryAndRefusals(t *testing.T) {
	for _, name := range []string{"exact", "marker4", "unknown-object", "core-generation", "core-phase", "nonempty-package"} {
		t.Run(name, func(t *testing.T) {
			pool := workerFreshSQLFixture(t)
			var statement string
			switch name {
			case "marker4":
				statement = `UPDATE metadata SET value='4' WHERE key='workspace_format'`
			case "unknown-object":
				statement = `CREATE TABLE resident_worker_unknown(id TEXT)`
			case "core-generation":
				statement = `UPDATE resident_worker_core_state SET generation='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`
			case "core-phase":
				statement = `UPDATE resident_worker_core_state SET phase='active'`
			case "nonempty-package":
				statement = `INSERT INTO resident_worker_packages(slot,package_generation,package_sha256,manifest,source_commitment,phase,revision) VALUES(1,'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',X'7b7d',X'7b7d','staged',0)`
			}
			if statement != "" {
				if _, err := pool.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			call := &residentWorkerFreshFormatCall{ctx: t.Context(), tx: tx}
			err = readWorkerFreshWorkspace(call, strings.Repeat("a", 32))
			if name == "exact" && err != nil || name != "exact" && err == nil {
				t.Fatal("official schema grammar result", name, err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if name == "exact" {
				for _, query := range call.queries {
					if query == nil || !query.queryReturned || query.queryError != nil || query.rows == nil || query.nextCalls != query.nextReturns || !query.closeAttempted || !query.closeReturned || query.closeError != nil {
						t.Fatal("SQL original observation missing")
					}
				}
			}
		})
	}
}
