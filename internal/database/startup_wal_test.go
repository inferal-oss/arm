package database

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/storage"
)

func walBase(t *testing.T) string {
	t.Helper()
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
	for _, query := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`, `CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`, `INSERT INTO metadata VALUES('host','11111111111111111111111111111111'),('fixture','before')`, `PRAGMA wal_checkpoint(TRUNCATE)`} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, "state.db-wal")); !os.IsNotExist(err) {
		t.Fatal("fixture did not establish actually absent initial WAL", err)
	}
	return home
}

func independentWALWriter(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func appendIndependentStop(t *testing.T, db *sql.DB, home string) nodestop.State {
	t.Helper()
	dir, err := OpenDirectory(home)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	id, err := storage.DirectoryIdentity(int(dir.FD.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	target := nodestop.Target{Host: strings.Repeat("1", 32), Owner: strconv.Itoa(os.Geteuid()), Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Startup: nodestop.StartupContract}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	a := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	impact := nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{a.Host}}
	plan, err := b.Prepare("independent-stop", a, 1, model.ShutdownRequest{}, "private independent committed WAL", impact, SnapshotTime())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r := nodestop.Record{Schema: nodestop.RecordSchema, Plan: plan, Digest: hash, Shutdown: strings.Repeat("6", 32), DesiredRevision: 2, Phase: "accepted", Created: plan.Observed, Updated: plan.Observed}
	state := nodestop.State{Schema: nodestop.StateSchema, Revision: 2, Running: false, Target: target, Key: plan.Key, Digest: hash}
	if err = state.Validate(r); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, query := range nodestop.Tables {
		if _, err = tx.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_schema VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	rawR, _ := json.Marshal(r)
	rawS, _ := json.Marshal(state)
	if _, err = tx.Exec(`INSERT INTO node_stop_operations VALUES(?,?)`, plan.Key, string(rawR)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_state VALUES(1,?)`, string(rawS)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestStartupReadRefusesConcurrentNewCommittedWALDespiteStaleAbsentSnapshot(t *testing.T) {
	for _, cut := range []string{"validate", "close-only", "after-sqlite-close"} {
		t.Run(cut, func(t *testing.T) {
			home := walBase(t)
			lock := startupHeldLock(t, home)
			read, present, err := OpenStartupRead(t.Context(), home, lock)
			if err != nil || !present {
				t.Fatal(present, err)
			}
			defer read.Close()
			if read.wal.Present {
				t.Fatal("did not test absent initial WAL branch")
			}
			if _, intent, err := nodestop.ReadState(read); err != nil || intent {
				t.Fatal("initial read was not absent", intent, err)
			}
			if err = read.Validate(t.Context()); err != nil {
				t.Fatal("valid cut before independent commit", err)
			}
			writer := independentWALWriter(t, home)
			if cut == "after-sqlite-close" {
				if err = read.closeSQLite(); err != nil {
					t.Fatal(err)
				}
			}
			committed := appendIndependentStop(t, writer, home)
			fresh, intent, err := nodestop.ReadState(writer)
			if err != nil || !intent || fresh != committed {
				t.Fatal("independent commit was not real/complete", fresh, intent, err)
			}
			if cut != "after-sqlite-close" {
				if _, intent, err = nodestop.ReadState(read); err != nil || intent {
					t.Fatal("control did not retain the stale absent snapshot", intent, err)
				}
			}
			switch cut {
			case "validate":
				if err = read.Validate(t.Context()); !errors.Is(err, errStartupWALChanged) {
					t.Fatal("new committed WAL normalized as bookkeeping", err)
				}
			case "after-sqlite-close":
				if err = read.finishClose(); !errors.Is(err, errStartupWALChanged) {
					t.Fatal("final WAL guard missed post-SQLite-close commit", err)
				}
			}
			if err = read.Close(); !errors.Is(err, errStartupWALChanged) {
				t.Fatal("close discarded newly committed/sticky WAL refusal", err)
			}
			fresh, intent, err = nodestop.ReadState(writer)
			if err != nil || !intent || fresh != committed {
				t.Fatal("reader erased/changed the independent committed stop", fresh, intent, err)
			}
		})
	}
}

func TestStartupReadCloseRejectsAppendToPreexistingNonemptyWAL(t *testing.T) {
	home := walBase(t)
	lock := startupHeldLock(t, home)
	writer := independentWALWriter(t, home)
	before := appendIndependentStop(t, writer, home)
	read, present, err := OpenStartupRead(t.Context(), home, lock)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	defer read.Close()
	if !read.wal.Present || read.walEmpty || read.wal.Size <= 32 {
		t.Fatal("did not exercise initial nonempty WAL")
	}
	if state, intent, err := nodestop.ReadState(read); err != nil || !intent || state != before {
		t.Fatal(intent, err)
	}
	if _, err = writer.Exec(`UPDATE metadata SET value='after' WHERE key='fixture'`); err != nil {
		t.Fatal(err)
	}
	// No caller Validate after the commit: public Close must check both cuts.
	if err = read.Close(); !errors.Is(err, errStartupWALChanged) {
		t.Fatal("close ignored an actual existing-WAL append", err)
	}
	if state, intent, err := nodestop.ReadState(writer); err != nil || !intent || state != before {
		t.Fatal("reader changed existing entitlement", intent, err)
	}
}

func TestStartupReadAllowsObservedSQLiteEmptyBookkeepingAndPreservesErrors(t *testing.T) {
	home := walBase(t)
	lock := startupHeldLock(t, home)
	read, present, err := OpenStartupRead(t.Context(), home, lock)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	now, err := read.observeSource("state.db-wal")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual readonly SQLite WAL bookkeeping: present=%t bytes=%d", now.Present, now.Size)
	if err = read.Validate(t.Context()); err != nil {
		t.Fatal("legitimate empty bookkeeping refused", err)
	}
	if err = read.Close(); err != nil {
		t.Fatal("empty SQLite close bookkeeping refused", err)
	}
	read, present, err = OpenStartupRead(t.Context(), home, lock)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	defer read.Close()
	writer := independentWALWriter(t, home)
	appendIndependentStop(t, writer, home)
	first := read.Validate(t.Context())
	if !errors.Is(first, errStartupWALChanged) {
		t.Fatal("actual first WAL failure missing", first)
	}
	if err = read.closeSQLite(); err != nil {
		t.Fatal(err)
	}
	// A subsequent legitimate checkpoint changes the physical arrangement but
	// must not waive an earlier already-observed stale-snapshot failure.
	var busy, frames, checkpointed int
	if err = writer.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed); err != nil || busy != 0 {
		t.Fatal("controlled checkpoint did not complete", busy, frames, checkpointed, err)
	}
	if wal, e := os.Stat(filepath.Join(home, "state.db-wal")); e != nil || wal.Size() != 0 {
		t.Fatal("checkpoint did not establish real empty bookkeeping", e)
	}
	if err = read.Close(); !errors.Is(err, errStartupWALChanged) {
		t.Fatal("later checkpoint erased the first failure", err)
	}
}

func TestStartupWALHeaderGrammarUsesActualSQLiteHeader(t *testing.T) {
	home := walBase(t)
	writer := independentWALWriter(t, home)
	appendIndependentStop(t, writer, home)
	wal, err := os.Open(filepath.Join(home, "state.db-wal"))
	if err != nil {
		t.Fatal(err)
	}
	var actual [32]byte
	if _, err = wal.ReadAt(actual[:], 0); err != nil {
		wal.Close()
		t.Fatal(err)
	}
	wal.Close()
	// This header-only PROJECTION is a format control, not a claim that SQLite
	// emitted a32-byte empty file. Runtime bookkeeping sizes are logged above.
	path := filepath.Join(home, "owned-header-projection")
	for _, kind := range []string{"actual-header", "zero-length", "partial", "extra-byte", "version", "magic", "page", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			raw := append([]byte{}, actual[:]...)
			switch kind {
			case "zero-length":
				raw = nil
			case "partial":
				raw = raw[:16]
			case "extra-byte":
				raw = append(raw, 0)
			case "version":
				raw[7] ^= 1
			case "magic":
				raw[0] ^= 1
			case "page":
				for i := 8; i < 12; i++ {
					raw[i] = 0
				}
			case "checksum":
				raw[31] ^= 1
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			id, err := nativeFilePolicy(f, "state.db-wal", false, true)
			if err != nil {
				t.Fatal(err)
			}
			empty, err := emptyStartupWAL(f, id)
			if kind == "actual-header" || kind == "zero-length" {
				if err != nil || !empty {
					t.Fatal("valid frame-free grammar refused", empty, err)
				}
			} else if empty || kind != "extra-byte" && err == nil {
				t.Fatal("malformed/content-bearing header normalized as empty", empty, err)
			}
		})
	}
}

// The final close cut must bind absence too: a cooperating daemon lock does not
// prevent an independent SQLite writer from committing before final validation.
func TestStartupReadFinalCloseBindsInitiallyAbsentMain(t *testing.T) {
	for _, appeared := range []bool{false, true} {
		t.Run(strconv.FormatBool(appeared), func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			lock := startupHeldLock(t, home)
			read, present, err := OpenStartupRead(t.Context(), home, lock)
			if err != nil || present {
				t.Fatal(present, err)
			}
			defer read.Close()
			if err = read.Validate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = read.closeSQLite(); err != nil {
				t.Fatal(err)
			}
			if appeared {
				writer := independentWALWriter(t, home)
				committed := appendIndependentStop(t, writer, home)
				fresh, intent, err := nodestop.ReadState(writer)
				if err != nil || !intent || fresh != committed {
					t.Fatal("actual committed stopped intent missing", fresh, intent, err)
				}
				if err = writer.Close(); err != nil {
					t.Fatal(err)
				}
				for _, suffix := range []string{"-wal", "-shm", "-journal"} {
					if _, err = os.Stat(filepath.Join(home, "state.db"+suffix)); !os.IsNotExist(err) {
						t.Fatal("test requires no surviving sidecar", suffix, err)
					}
				}
			}
			closeErr := read.finishClose()
			if !appeared {
				if closeErr != nil {
					t.Fatal("unchanged absent main refused", closeErr)
				}
				return
			}
			if closeErr == nil || !strings.Contains(closeErr.Error(), "read-only startup main changed on close") {
				t.Fatal("new committed main accepted at final cut", closeErr)
			}
			if err = read.Close(); err == nil || !strings.Contains(err.Error(), "read-only startup main changed on close") {
				t.Fatal("final-cut refusal lost on repeat close", err)
			}
			retained, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if _, intent, err := nodestop.ReadState(retained); err != nil || !intent {
				t.Fatal("refusal erased committed stopped intent", intent, err)
			}
		})
	}
}
