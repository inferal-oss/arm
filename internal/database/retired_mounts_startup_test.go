package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func installedSparseReset(t *testing.T) (*resetStore, *sql.DB) {
	t.Helper()
	s, _ := completedResetFileEngine(t)
	lock := resetInstallLock(t, s)
	if err := InstallResetSeed(t.Context(), s.home.Path, lock); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.home.Path, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return s, db
}

func TestStartupRetiredMountGuardAcceptsOnlyPristineInstalledResetSeed(t *testing.T) {
	s, db := installedSparseReset(t)
	var leases int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='leases'`).Scan(&leases); err != nil || leases != 0 {
		t.Fatal("fixture is not a sparse reset seed", leases, err)
	}
	path := filepath.Join(s.home.Path, "state.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RefuseRetiredMountStateAtStartup(t.Context(), s.home.Path, db); err != nil {
		t.Fatal("exact installed seed refused before schema creation", err)
	}
	// The exceptional startup allowance cannot make an incomplete obligation
	// schema an acceptable independent destructive settled proof.
	if err := RefuseRetiredMountState(t.Context(), db); err == nil {
		t.Fatal("strict settled-proof guard accepted absent obligation schema")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("startup guard changed seed bytes", err)
	}
}

func TestStartupRetiredMountGuardRefusesChangedOrUnboundSparseResetSeed(t *testing.T) {
	for _, change := range []string{"missing-pointer", "malformed-pointer", "uninstalled-pointer", "missing-receipt", "changed-bytes", "replaced-inode", "retired-table", "retired-marker", "forged-applied-marker", "sqlite-companion"} {
		t.Run(change, func(t *testing.T) {
			s, db := installedSparseReset(t)
			path := filepath.Join(s.home.Path, "state.db")
			switch change {
			case "missing-pointer":
				if err := os.Remove(filepath.Join(s.home.Path, ResetSeedName)); err != nil {
					t.Fatal(err)
				}
			case "malformed-pointer":
				if err := os.WriteFile(filepath.Join(s.home.Path, ResetSeedName), []byte(`{"installed":true}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				seed, _, err := readResetJSON[ResetSeed](s.home, ResetSeedName)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(s.home.Path, ResetDirectoryName, "reset-"+seed.Operation, resetReceiptName)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "uninstalled-pointer":
				seed, file, err := readResetJSON[ResetSeed](s.home, ResetSeedName)
				if err != nil {
					t.Fatal(err)
				}
				seed.Installed = false
				if err = atomicResetJSON(s.home, ResetSeedName, seed, &file); err != nil {
					t.Fatal(err)
				}
			case "changed-bytes", "retired-table", "retired-marker", "forged-applied-marker":
				query := map[string]string{
					"changed-bytes":  `CREATE TABLE unrelated_extra(value TEXT)`,
					"retired-table":  `CREATE TABLE share_publications(value TEXT)`,
					"retired-marker": `INSERT INTO metadata VALUES('SHARE_SCHEMA','retired')`,
				}[change]
				if change == "forged-applied-marker" {
					if _, err := db.Exec(`UPDATE metadata SET value='{}' WHERE key=?`, resetAppliedKey); err != nil {
						t.Fatal(err)
					}
				} else if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			case "replaced-inode":
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, path+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				db, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
			case "sqlite-companion":
				// Establish the connection first, then add a named companion.
				if err := db.Ping(); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path+"-shm", []byte("retained unknown companion"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := RefuseRetiredMountStateAtStartup(t.Context(), s.home.Path, db); err == nil {
				t.Fatal("changed/unbound sparse database accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("refusal changed database bytes", err)
			}
		})
	}
}

func TestStartupRetiredMountGuardRefusesUnrelatedPartialDatabase(t *testing.T) {
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO metadata VALUES('host','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`); err != nil {
		t.Fatal(err)
	}
	if err := RefuseRetiredMountStateAtStartup(t.Context(), home, db); err == nil {
		t.Fatal("missing leases alone was treated as absence of obligations")
	}
}
