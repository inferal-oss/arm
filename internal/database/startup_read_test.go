package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

func startupHeldLock(t *testing.T, home string) *os.File {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() })
	return f
}
func startupMainSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func startupCrashWAL(home string) error {
	if !strings.HasPrefix(filepath.Base(home), "arm-stop-wal-") {
		return fmt.Errorf("fixed disposable WAL fixture required")
	}
	dir, err := OpenDirectory(home)
	if err != nil {
		return err
	}
	defer dir.Close()
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	// No SQL Close is called on the successful child path. Its retained parent
	// exec.Cmd receives exit73 after COMMIT, leaving actual crash-style WAL/SHM.
	for _, query := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`, `CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`, `INSERT INTO metadata VALUES('host','11111111111111111111111111111111')`, `PRAGMA wal_checkpoint(TRUNCATE)`} {
		if _, err = db.Exec(query); err != nil {
			return err
		}
	}
	baseline, err := startupMainSHA(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(home, "baseline.sha256"), []byte(baseline), 0600); err != nil {
		return err
	}
	target := nodestop.Target{Host: strings.Repeat("1", 32), Owner: strconv.Itoa(os.Geteuid()), Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Startup: nodestop.StartupContract}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		return err
	}
	a := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	impact := nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{a.Host}}
	plan, err := b.Prepare("wal-stop", a, 1, model.ShutdownRequest{}, "private actual WAL-only intent", impact, SnapshotTime())
	if err != nil {
		return err
	}
	hash, err := plan.Digest()
	if err != nil {
		return err
	}
	r := nodestop.Record{Schema: nodestop.RecordSchema, Plan: plan, Digest: hash, Shutdown: strings.Repeat("6", 32), DesiredRevision: 2, Phase: "accepted", Created: plan.Observed, Updated: plan.Observed}
	s := nodestop.State{Schema: nodestop.StateSchema, Revision: 2, Running: false, Target: target, Key: plan.Key, Digest: hash}
	if err = s.Validate(r); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range nodestop.Tables {
		if _, err = tx.Exec(query); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_schema VALUES(1,1)`); err != nil {
		return err
	}
	rawR, _ := json.Marshal(r)
	rawS, _ := json.Marshal(s)
	if _, err = tx.Exec(`INSERT INTO node_stop_operations VALUES(?,?)`, plan.Key, string(rawR)); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_state VALUES(1,?)`, string(rawS)); err != nil {
		return err
	}
	return tx.Commit()
}

func TestStartupReadWALOnlyCommittedIntentAfterOwnedProcessExit(t *testing.T) {
	if os.Getenv("ARM_TEST_STOP_WAL_CHILD") == "1" {
		if err := startupCrashWAL(os.Getenv("ARM_TEST_STOP_WAL_HOME")); err != nil {
			t.Fatal(err)
		}
		os.Exit(73)
	}
	for _, withoutSHM := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-shm-%t", withoutSHM), func(t *testing.T) {
			parent, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, err := os.MkdirTemp(parent, "arm-stop-wal-")
			if err != nil {
				t.Fatal(err)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestStartupReadWALOnlyCommittedIntentAfterOwnedProcessExit$", "-test.timeout=15s")
			child.Env = append(os.Environ(), "ARM_TEST_STOP_WAL_CHILD=1", "ARM_TEST_STOP_WAL_HOME="+home)
			child.Stdout = io.Discard
			child.Stderr = os.Stderr
			// Actual retained Command/one Wait via Run, no PID/group probing or
			// signalling fallback. The existing outer test owner bounds the package.
			err = child.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 || child.ProcessState == nil {
				t.Fatal("owned WAL producer did not reach exact committed exit", err)
			}
			baseline, err := os.ReadFile(filepath.Join(home, "baseline.sha256"))
			if err != nil {
				t.Fatal(err)
			}
			before, err := startupMainSHA(filepath.Join(home, "state.db"))
			if err != nil || before != string(baseline) {
				t.Fatal("intent was checkpointed into main rather than WAL", before, err)
			}
			wal, err := os.Stat(filepath.Join(home, "state.db-wal"))
			if err != nil || wal.Size() <= 32 {
				t.Fatal("committed WAL residue missing", err)
			}
			if withoutSHM {
				if err = os.Remove(filepath.Join(home, "state.db-shm")); err != nil {
					t.Fatal(err)
				}
			} // exact fixture only, after sole native Wait
			lock := startupHeldLock(t, home)
			read, present, err := OpenStartupRead(t.Context(), home, lock)
			if err != nil || !present {
				t.Fatal("WAL-aware open", present, err)
			}
			if shm, e := os.Stat(filepath.Join(home, "state.db-shm")); e == nil {
				t.Logf("SQLite SHM bookkeeping while open: present bytes=%d mode=%#o", shm.Size(), shm.Mode().Perm())
			} else {
				t.Logf("SQLite SHM bookkeeping while open: %v", e)
			}
			state, exists, err := nodestop.ReadState(read)
			if err != nil || !exists || state.Running || state.Revision != 2 || state.Key != "wal-stop" {
				read.Close()
				t.Fatal("WAL-only stopped intent lost", state, exists, err)
			}
			var schemaBefore int
			if err = read.QueryRow(`PRAGMA schema_version`).Scan(&schemaBefore); err != nil {
				t.Fatal(err)
			}
			rows, writeErr := read.Query(`DELETE FROM node_stop_state`)
			if writeErr == nil {
				rows.Next()
				writeErr = rows.Err()
				rows.Close()
			}
			if writeErr == nil {
				t.Fatal("RO/query-only boundary permitted logical mutation")
			}
			if err = read.Validate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = read.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := startupMainSHA(filepath.Join(home, "state.db"))
			if err != nil || after != before {
				t.Fatal("readonly startup altered main rows/schema", err)
			}
			again, present, err := OpenStartupRead(t.Context(), home, lock)
			if err != nil || !present {
				t.Fatal(present, err)
			}
			defer again.Close()
			current, exists, err := nodestop.ReadState(again)
			if err != nil || !exists || current != state {
				t.Fatal("closed reader changed durable intent", current, err)
			}
			var schemaAfter int
			if err = again.QueryRow(`PRAGMA schema_version`).Scan(&schemaAfter); err != nil || schemaAfter != schemaBefore {
				t.Fatal("schema changed", err)
			}
		})
	}
}

func TestStartupReadLargeNoIntentDoesNotNeedScratchCopy(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	// The baseline format gate requires ARM metadata even when no stopped intent
	// exists. Keep the actual 64 MiB payload and unavailable-scratch assertions.
	for _, query := range []string{`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`, `INSERT INTO metadata VALUES('host','11111111111111111111111111111111')`, `CREATE TABLE payload(data BLOB)`, `INSERT INTO payload VALUES(zeroblob(67108864))`} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(home, "scratch-is-a-file")
	if err = os.WriteFile(blocked, []byte("unavailable scratch, not a simulated ENOSPC measurement"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	lock := startupHeldLock(t, home)
	read, present, err := OpenStartupRead(context.Background(), home, lock)
	if err != nil || !present {
		t.Fatal("large DB required a scratch copy", present, err)
	}
	defer read.Close()
	if _, intent, err := nodestop.ReadState(read); err != nil || intent {
		t.Fatal("legacy no-intent scope changed", intent, err)
	}
}

func TestStartupReadRejectsUnsafeSidecarAndOrphanWithoutMutation(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	lock := startupHeldLock(t, home)
	if err = os.WriteFile(filepath.Join(home, "state.db-wal"), []byte("orphan unknown WAL"), 0600); err != nil {
		t.Fatal(err)
	}
	if read, _, err := OpenStartupRead(t.Context(), home, lock); err == nil {
		read.Close()
		t.Fatal("orphan WAL fabricated empty startup intent")
	}
	if _, err = os.Stat(filepath.Join(home, "state.db")); !os.IsNotExist(err) {
		t.Fatal("orphan refusal created main DB", err)
	}
	if err = os.Remove(filepath.Join(home, "state.db-wal")); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	sentinel := filepath.Join(home, "sentinel")
	if err = os.WriteFile(sentinel, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(sentinel, filepath.Join(home, "state.db-shm")); err != nil {
		t.Fatal(err)
	}
	if read, _, err := OpenStartupRead(t.Context(), home, lock); err == nil {
		read.Close()
		t.Fatal("sidecar alias admitted")
	}
	actual, err := os.ReadFile(sentinel)
	if err != nil || string(actual) != "unrelated" {
		t.Fatal("sentinel changed", err)
	}
}
