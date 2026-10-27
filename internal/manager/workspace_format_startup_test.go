package manager

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func TestWorkspaceFormatRefusesDirectManagerBeforeSchemaOrRecovery(t *testing.T) {
	for _, change := range []string{"format-2", "partial-pair", "orphan-ref", "blob-worker", "blob-ref", "blob-ordinary"} {
		t.Run(change, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "state.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`, `INSERT INTO metadata VALUES('host','11111111111111111111111111111111')`, `CREATE TABLE leases(id TEXT PRIMARY KEY,request_key TEXT UNIQUE,data TEXT NOT NULL,run TEXT)`, map[string]string{
				"format-2":      `INSERT INTO metadata VALUES('workspace_format','2')`,
				"partial-pair":  `CREATE TABLE provider_preparations(id TEXT)`,
				"orphan-ref":    `INSERT INTO leases VALUES('held','original','{"state":"active","provider_preparation":null}',NULL)`,
				"blob-worker":   `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"provider-prepare-worker"},"state":"active"}' AS BLOB),NULL)`,
				"blob-ref":      `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"storage"},"state":"active","provider_preparation":null}' AS BLOB),NULL)`,
				"blob-ordinary": `INSERT INTO leases VALUES('held','original',CAST('{"request":{"kind":"execution"},"state":"released"}' AS BLOB),NULL)`,
			}[change]} {
				if _, err = db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m, err := Open(home, model.Resources{CPU: 1})
			if m != nil {
				m.Close()
				t.Fatal("unsupported ledger opened")
			}
			if err == nil || !strings.Contains(err.Error(), "workspace_format_unsupported") {
				t.Fatal("missing format refusal", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("unsupported ledger changed before refusal", err)
			}
			entries, err := os.ReadDir(home)
			if err != nil || len(entries) != 1 || entries[0].Name() != "state.db" {
				t.Fatal("startup created schema/sidecar/native namespace", entries, err)
			}
		})
	}
}

func TestWorkspaceFormatOrdinaryManagerReopensWithoutActivatingFormat(t *testing.T) {
	for _, marker := range []string{"absent", "1"} {
		t.Run(marker, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "ordinary")
			for i := 0; i < 2; i++ {
				m, err := Open(home, model.Resources{CPU: 1})
				if err != nil {
					t.Fatal(err)
				}
				if marker == "1" && i == 0 {
					if _, err := m.db.Exec(`INSERT INTO metadata VALUES('workspace_format','1')`); err != nil {
						m.Close()
						t.Fatal(err)
					}
				}
				var count int
				want := 0
				if marker == "1" {
					want = 1
				}
				if err := m.db.QueryRow(`SELECT count(*) FROM metadata WHERE key IN ('workspace_format','provider_preparation_schema')`).Scan(&count); err != nil || count != want {
					m.Close()
					t.Fatal("ordinary open changed format metadata", count, err)
				}
				if err := m.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
