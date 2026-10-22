package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func completedResetFileEngine(t *testing.T) (*resetStore, ResetReceipt) {
	t.Helper()
	s, r, file := resetFileEngineFixture(t)
	result, err := s.finishReset(context.Background(), &r, &file, nil)
	if err != nil || !result.Completed {
		t.Fatalf("reset %+v %v", result, err)
	}
	return s, r
}
func resetInstallLock(t *testing.T, s *resetStore) *os.File {
	t.Helper()
	f, _, err := s.home.Open("daemon.lock", unix.O_RDWR)
	if err != nil || f == nil {
		t.Fatal(err)
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockReset(f) })
	return f
}
func TestResetSeedInstallNewAuthorityExactlyOnce(t *testing.T) {
	s, r := completedResetFileEngine(t)
	if err := CheckStartupReset(s.home.Path); err == nil {
		t.Fatal("unlocked manager could consume seed")
	}
	lock := resetInstallLock(t, s)
	if err := InstallResetSeed(context.Background(), s.home.Path, lock); err != nil {
		t.Fatal(err)
	}
	seed, _, err := readResetJSON[ResetSeed](s.home, ResetSeedName)
	if err != nil || !seed.Installed {
		t.Fatalf("seed not consumed %+v %v", seed, err)
	}
	if err = CheckStartupReset(s.home.Path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(s.home.Path, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	host, err := ExistingAuthority(context.Background(), db)
	if err != nil || host != r.Plan.NewHost || host == r.Plan.OldHost {
		t.Fatalf("authority %s %v", host, err)
	}
	if _, err = db.Exec(`CREATE TABLE later_operator_data(value TEXT); INSERT INTO later_operator_data VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = InstallResetSeed(context.Background(), s.home.Path, lock); err != nil {
		t.Fatal("ordinary restart revalidated old database bytes", err)
	}
	db, err = sql.Open("sqlite", filepath.Join(s.home.Path, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var data string
	if err = db.QueryRow(`SELECT value FROM later_operator_data`).Scan(&data); err != nil || data != "preserved" {
		t.Fatal("seed was reapplied", err)
	}
	// The helper did not release/change the caller's lock lifetime.
	rival, _, err := s.home.Open("daemon.lock", unix.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer rival.Close()
	if err = unix.Flock(int(rival.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("installer released caller authority lock")
	}
}
func TestResetSeedInstallRecoversRenameAcknowledgementLoss(t *testing.T) {
	s, r := completedResetFileEngine(t)
	lock := resetInstallLock(t, s)
	if err := renameResetExclusive(int(s.operation.FD.Fd()), resetFreshName, int(s.home.FD.Fd()), "state.db"); err != nil {
		t.Fatal(err)
	}
	if err := InstallResetSeed(context.Background(), s.home.Path, lock); err != nil {
		t.Fatal(err)
	}
	expected := r.Seed
	expected.Name = "state.db"
	if _, err := exactResetFile(context.Background(), s.home, expected, false); err != nil {
		t.Fatal("did not reuse prebuilt exact inode", err)
	}
}
func TestResetSeedInstallRefusesUnlockedOrConflictingAuthority(t *testing.T) {
	for _, kind := range []string{"nil-lock", "wrong-lock", "busy-lock", "replacement", "wal", "consumed-missing"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := completedResetFileEngine(t)
			var lock *os.File
			switch kind {
			case "nil-lock":
			case "wrong-lock":
				lock, _, _ = s.home.Open("other.lock", unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
				defer lock.Close()
			case "busy-lock":
				_ = resetInstallLock(t, s)
				lock, _, _ = s.home.Open("daemon.lock", unix.O_RDWR)
				defer lock.Close()
			default:
				lock = resetInstallLock(t, s)
			}
			switch kind {
			case "replacement":
				os.WriteFile(filepath.Join(s.home.Path, "state.db"), []byte("unrelated established file"), 0600)
			case "wal":
				os.WriteFile(filepath.Join(s.home.Path, "state.db-wal"), []byte("unreviewed WAL"), 0600)
			case "consumed-missing":
				if err := InstallResetSeed(context.Background(), s.home.Path, lock); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(s.home.Path, "state.db")); err != nil {
					t.Fatal(err)
				}
			}
			if err := InstallResetSeed(context.Background(), s.home.Path, lock); err == nil {
				t.Fatal("unsafe installation accepted")
			}
			if kind == "replacement" {
				data, _ := os.ReadFile(filepath.Join(s.home.Path, "state.db"))
				if string(data) != "unrelated established file" {
					t.Fatal("established file repaired")
				}
			}
		})
	}
}
func TestExistingAuthorityNeverRepairsMissingOrMalformedMetadata(t *testing.T) {
	for _, kind := range []string{"missing-table", "missing-host", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if kind != "missing-table" {
				if _, err = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "malformed" {
				if _, err = db.Exec(`INSERT INTO metadata VALUES('host','invalid')`); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.Ping(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = ExistingAuthority(context.Background(), db); err == nil {
				t.Fatal("missing authority accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("authority inspection mutated established database")
			}
		})
	}
}
