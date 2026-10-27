package manager

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

// Legacy sync authority cannot be silently upgraded: there is no durable
// locator evidence for its previously staged native workspaces.
func TestWorkspaceReviewLegacySyncSchemaRefusesWithoutRepair(t *testing.T) {
	for _, used := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "used"}[used], func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			resources := model.Resources{CPU: 1, Memory: 1 << 20, Disk: 1 << 20}
			m, err := Open(home, resources)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.db.Exec(`DROP TABLE sync_workspaces; UPDATE metadata SET value='1' WHERE key='sync_schema'`); err != nil {
				t.Fatal(err)
			}
			if used {
				if _, err = m.db.Exec(`INSERT INTO metadata(key,value) VALUES('sync_used','1')`); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			if reopened, err := Open(home, resources); err == nil {
				reopened.Close()
				t.Fatal("legacy sync schema was opened or migrated")
			} else if !strings.Contains(err.Error(), "version 1 is incompatible") {
				t.Fatalf("imprecise legacy refusal: %v", err)
			}
			db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var marker string
			var n int
			if err = db.QueryRow(`SELECT value FROM metadata WHERE key='sync_schema'`).Scan(&marker); err != nil || marker != "1" {
				t.Fatal("legacy marker changed", marker, err)
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='sync_workspaces'`).Scan(&n); err != nil || n != 0 {
				t.Fatal("legacy namespace repaired", n, err)
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM metadata WHERE key='sync_used'`).Scan(&n); err != nil || n != map[bool]int{false: 0, true: 1}[used] {
				t.Fatal("legacy use evidence changed", n, err)
			}
			if err = db.PingContext(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
