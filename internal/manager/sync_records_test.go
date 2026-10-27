package manager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
	"github.com/inferal-oss/arm/internal/syncservice"
)

func TestSyncSchemaInstalledDurablyAndReopenProvesIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	open := func() *Manager {
		m, e := Open(home, model.Resources{CPU: 1, Memory: 1 << 20, Disk: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	m := open()
	var level int
	var mode, marker string
	if e := m.db.QueryRow(`PRAGMA synchronous`).Scan(&level); e != nil || level < 2 {
		t.Fatalf("synchronous %d %v", level, e)
	}
	if e := m.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); e != nil || mode != "wal" {
		t.Fatalf("journal %q %v", mode, e)
	}
	if e := m.db.QueryRow(`SELECT value FROM metadata WHERE key='sync_schema'`).Scan(&marker); e != nil || marker != "1" {
		t.Fatalf("schema marker %q %v", marker, e)
	}
	if e := syncservice.VerifyDurableSQLite(context.Background(), m.db); e != nil {
		t.Fatal(e)
	}
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	m = open()
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestSyncSchemaPartialStateRefusesStartupWithoutRepair(t *testing.T) {
	for _, query := range []string{`DROP TABLE sync_effects`, `DELETE FROM metadata WHERE key='sync_schema'`, `DROP INDEX sync_open_generation`} {
		t.Run(query, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, e := Open(home, model.Resources{CPU: 1, Memory: 1 << 20, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.db.Exec(query); e != nil {
				t.Fatal(e)
			}
			if e = m.Close(); e != nil {
				t.Fatal(e)
			}
			if reopened, e := Open(home, model.Resources{CPU: 1, Memory: 1 << 20, Disk: 1 << 20}); e == nil {
				reopened.Close()
				t.Fatal("partial sync schema repaired")
			}
		})
	}
}

func TestRetainedSyncUseBlocksNodeStop(t *testing.T) {
	m, _, external := stopManagerFixture(t)
	if _, err := m.db.Exec(`INSERT INTO metadata(key,value) VALUES('sync_used','1')`); err != nil {
		t.Fatal(err)
	}
	impact, _, err := m.nodeStopCensus(external, nodestop.LeaseReadScope{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, blocker := range impact.Blockers {
		if blocker.ID == "sync:retained" {
			found = true
		}
	}
	if !found {
		t.Fatal("retained sync use absent from Stop blockers")
	}
}
