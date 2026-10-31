package agentsessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

type storedFixture struct {
	home  string
	roots []string
	db    *sql.DB
}

func newStoredFixture(t *testing.T, n int) storedFixture {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	roots := make([]string, n)
	for i := range roots {
		root := t.TempDir()
		roots[i], err = filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,cwd TEXT NOT NULL,title TEXT)"); err != nil {
		t.Fatal(err)
	}
	return storedFixture{home, roots, db}
}
func (f storedFixture) add(t *testing.T, id, root, name string) {
	t.Helper()
	if _, err := f.db.Exec("INSERT INTO threads(id,cwd,title) VALUES(?,?,?)", id, root, "PRIVATE_PROMPT"); err != nil {
		t.Fatal(err)
	}
	f.name(t, id, name)
}
func (f storedFixture) name(t *testing.T, id, name string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(f.home, "session_index.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(file).Encode(map[string]any{"id": id, "thread_name": name, "updated_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), "preview": "PRIVATE_PREVIEW"})
	if err = errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestCodexStoredNamesExactSharedLatestAndLaterDirectory(t *testing.T) {
	f := newStoredFixture(t, 17)
	first, second, later := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.add(t, first, f.roots[0], "Old task name")
	f.add(t, second, f.roots[0], "Shared task")
	f.name(t, first, "Renamed task")
	f.add(t, later, f.roots[16], "Later worktree task")
	// An unrequested cwd may have a name but must never decorate these roots.
	outside := t.TempDir()
	f.add(t, uuid.NewString(), outside, "Unrequested task")
	out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	if len(out.Warnings) != 0 || len(out.Titles) != 3 {
		t.Fatal(out)
	}
	names := []string{}
	for _, title := range out.Titles {
		if title.Directory != f.roots[0] && title.Directory != f.roots[16] {
			t.Fatal(title)
		}
		names = append(names, title.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Later worktree task", "Renamed task", "Shared task"}) {
		t.Fatal(names)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "Old task name") {
		t.Fatal("non-name or superseded content leaked", string(raw))
	}
}

func TestCodexStoredNamesFilterUnnamedHistoryAndQuotedPaths(t *testing.T) {
	f := newStoredFixture(t, 1)
	quoted := filepath.Join(f.roots[0], "user's directory ' OR 1=1 --")
	if err := os.Mkdir(quoted, 0700); err != nil {
		t.Fatal(err)
	}
	f.roots[0] = quoted
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < Limit+25; i++ {
		if _, err = tx.Exec("INSERT INTO threads(id,cwd,title) VALUES(?,?,?)", uuid.NewString(), quoted, "PRIVATE_HISTORY"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.add(t, uuid.NewString(), quoted, "Named match after history")
	out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	if len(out.Warnings) != 0 || len(out.Titles) != 1 || out.Titles[0].Name != "Named match after history" || out.Titles[0].Directory != quoted {
		t.Fatal(out)
	}
}

func TestCodexStoredNamesReadCommittedWALWithoutLogicalWrites(t *testing.T) {
	f := newStoredFixture(t, 1)
	if _, err := f.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	f.add(t, uuid.NewString(), f.roots[0], "Committed WAL name")
	before, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	after, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Warnings) != 0 || len(out.Titles) != 1 || string(before) != string(after) {
		t.Fatal(out, "read changed main database bytes")
	}
	var rows int
	var title string
	if err = f.db.QueryRow("SELECT count(*),min(title) FROM threads").Scan(&rows, &title); err != nil || rows != 1 || title != "PRIVATE_PROMPT" {
		t.Fatal(rows, title, err)
	}
	// WAL/SHM synchronization bookkeeping is deliberately not asserted unchanged.
	for _, name := range []string{"state_5.sqlite-wal", "state_5.sqlite-shm"} {
		info, err := os.Lstat(filepath.Join(f.home, name))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal(name, err)
		}
	}
	// Ordinary last-writer close checkpoints the fixture. Offline discovery
	// must not need an active client or pre-existing synchronization files.
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state_5.sqlite-wal", "state_5.sqlite-shm"} {
		if _, err = os.Lstat(filepath.Join(f.home, name)); !os.IsNotExist(err) {
			t.Fatal("ordinary fixture writer close did not remove sidecar", name, err)
		}
	}
	offlineBefore, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	offline := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	offlineAfter, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
	if err != nil || len(offline.Warnings) != 0 || len(offline.Titles) != 1 || offline.Titles[0].Name != "Committed WAL name" || string(offlineBefore) != string(offlineAfter) {
		t.Fatal("offline WAL-mode lookup failed or changed main bytes", offline, err)
	}
	// Verify logical/schema data using a separate explicitly read-only handle.
	check, err := sql.Open("sqlite", "file:"+filepath.Join(f.home, "state_5.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	if err = check.QueryRow("SELECT count(*),min(title) FROM threads").Scan(&rows, &title); err != nil || rows != 1 || title != "PRIVATE_PROMPT" {
		t.Fatal("offline read changed logical fixture rows", rows, title, err)
	}
	var kind string
	if err = check.QueryRow("SELECT type FROM sqlite_schema WHERE name='threads'").Scan(&kind); err != nil || kind != "table" {
		t.Fatal("offline read changed fixture schema", kind, err)
	}
}

func TestCodexStoredNamesRejectUnsupportedSchemas(t *testing.T) {
	for _, tc := range []struct{ name, ddl string }{
		{"view", "CREATE VIEW threads AS SELECT 'synthetic' AS id,'/fixture' AS cwd"},
		{"virtual", "CREATE VIRTUAL TABLE threads USING fts5(id,cwd)"},
		{"generated", "CREATE TABLE threads(id TEXT,cwd TEXT GENERATED ALWAYS AS ('/fixture') VIRTUAL)"},
		{"stored-generated", "CREATE TABLE threads(id TEXT,cwd TEXT GENERATED ALWAYS AS ('/fixture') STORED)"},
		{"nontext", "CREATE TABLE threads(id TEXT,cwd BLOB)"},
		{"missing-column", "CREATE TABLE threads(id TEXT)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoredFixture(t, 1)
			f.name(t, uuid.NewString(), "Schema must refuse")
			if _, err := f.db.Exec("DROP TABLE threads"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(tc.ddl); err != nil {
				t.Fatal(err)
			}
			out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
			if len(out.Titles) != 0 || len(out.Warnings) == 0 {
				t.Fatal(out)
			}
		})
	}
}

func TestCodexStoredNamesMissingMalformedAndUnsafeMetadata(t *testing.T) {
	for _, kind := range []string{"missing-index", "missing-db", "malformed-index", "symlink-index", "fifo-index", "symlink-db", "oversized-index", "oversized-db"} {
		t.Run(kind, func(t *testing.T) {
			f := newStoredFixture(t, 1)
			f.add(t, uuid.NewString(), f.roots[0], "Safe task")
			index := filepath.Join(f.home, "session_index.jsonl")
			db := filepath.Join(f.home, "state_5.sqlite")
			switch kind {
			case "missing-index":
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
			case "missing-db":
				f.db.Close()
				if err := os.Remove(db); err != nil {
					t.Fatal(err)
				}
			case "malformed-index":
				if err := os.WriteFile(index, []byte("PRIVATE_MALFORMED\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-index":
				if err := os.Rename(index, index+".outside"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(index+".outside", index); err != nil {
					t.Fatal(err)
				}
			case "fifo-index":
				if err := os.Remove(index); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(index, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-db":
				f.db.Close()
				if err := os.Rename(db, db+".outside"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(db+".outside", db); err != nil {
					t.Fatal(err)
				}
			case "oversized-index":
				if err := os.Truncate(index, storedIndexBytes+1); err != nil {
					t.Fatal(err)
				}
			case "oversized-db":
				f.db.Close()
				if err := os.Truncate(db, storedExtentBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
			raw, _ := json.Marshal(out)
			if len(out.Titles) != 0 || len(out.Warnings) == 0 || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal(out)
			}
		})
	}
}

func TestCodexStoredNamesContextCancellationAndReaderCleanup(t *testing.T) {
	f := newStoredFixture(t, 1)
	f.add(t, uuid.NewString(), f.roots[0], "After cancellation")
	if _, err := f.db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := (CodexStored{Home: f.home}).DiscoverTitles(ctx, f.roots)
	if len(out.Titles) != 0 || len(out.Warnings) == 0 || time.Since(start) > time.Second {
		t.Fatal(out, "cancellation not prompt")
	}
	if _, err := f.db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	out = (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	if len(out.Warnings) != 0 || len(out.Titles) != 1 {
		t.Fatal("cancelled reader poisoned later lookup", out)
	}
	if _, err := f.db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal("reader retained a lock", err)
	}
	if _, err := f.db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
}

func TestCodexStoredNamesCatalogAndIndexBounds(t *testing.T) {
	f := newStoredFixture(t, Limit)
	for i, root := range f.roots {
		f.add(t, uuid.NewString(), root, fmt.Sprintf("Task %03d", i))
	}
	out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	if len(out.Warnings) != 0 || len(out.Titles) != Limit {
		t.Fatal(len(out.Titles), out.Warnings)
	}
	f.name(t, uuid.NewString(), "Last unassociated index entry")
	out = (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	if len(out.Titles) != Limit-1 || len(out.Warnings) != 1 {
		t.Fatal(len(out.Titles), out.Warnings)
	}
	tooMany := append(slices.Clone(f.roots), f.roots[0])
	if out = (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), tooMany); len(out.Titles) != 0 || len(out.Warnings) == 0 {
		t.Fatal(out)
	}
}

func TestCodexStoredNamesDetectChangedSnapshot(t *testing.T) {
	f := newStoredFixture(t, 1)
	f.add(t, uuid.NewString(), f.roots[0], "Snapshot")
	root, err := os.OpenRoot(f.home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	snapshot, err := openStored(root, "session_index.jsonl", false)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.file.Close()
	if !snapshot.stable(root) {
		t.Fatal("fresh snapshot rejected")
	}
	f.name(t, uuid.NewString(), "Changed metadata")
	if snapshot.stable(root) {
		t.Fatal("changed source accepted as stable")
	}
	wal := filepath.Join(f.home, "state_5.sqlite-wal")
	absent, err := openStored(root, "state_5.sqlite-wal", true)
	if err != nil || !absent.stable(root) {
		t.Fatal("absent WAL rejected", err)
	}
	if err = os.WriteFile(wal, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !absent.stable(root) {
		t.Fatal("new empty WAL rejected")
	}
	existing, err := openStored(root, "state_5.sqlite-wal", true)
	if err != nil {
		t.Fatal(err)
	}
	defer existing.file.Close()
	if err = os.WriteFile(wal, []byte("nonempty WAL"), 0600); err != nil {
		t.Fatal(err)
	}
	if absent.stable(root) || existing.stable(root) {
		t.Fatal("new nonempty or changed existing WAL accepted")
	}
	if err = os.Remove(wal); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(f.home, "state_5.sqlite"), wal); err != nil {
		t.Fatal(err)
	}
	if absent.stable(root) {
		t.Fatal("new WAL symlink accepted")
	}
}

func TestCodexStoredNamesNeverUsesPromptFallback(t *testing.T) {
	f := newStoredFixture(t, 1)
	id := uuid.NewString()
	if _, err := f.db.Exec("INSERT INTO threads(id,cwd,title) VALUES(?,?,?)", id, f.roots[0], "PRIVATE_DATABASE_TITLE"); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"id": id, "title": "PRIVATE_TITLE_FIELD", "preview": "PRIVATE_PREVIEW_FIELD", "updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err := os.WriteFile(filepath.Join(f.home, "session_index.jsonl"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.home, "sessions", "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(f.home, "sessions", "fixture", "transcript.jsonl"), 0600); err != nil {
		t.Fatal(err)
	}
	out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
	raw, _ := json.Marshal(out)
	if len(out.Titles) != 0 || strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal(out)
	}
}

func TestCodexStoredNamesAmbiguityAndSelectedRowLimit(t *testing.T) {
	for _, count := range []int{2, Limit + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f := newStoredFixture(t, 2)
			if _, err := f.db.Exec("DROP TABLE threads"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec("CREATE TABLE threads(id TEXT,cwd TEXT,title TEXT)"); err != nil {
				t.Fatal(err)
			}
			id := uuid.NewString()
			f.name(t, id, "Ambiguous metadata")
			tx, err := f.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < count; i++ {
				if _, err = tx.Exec("INSERT INTO threads VALUES(?,?,?)", id, f.roots[i%2], "PRIVATE_PROMPT"); err != nil {
					t.Fatal(err)
				}
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			out := (CodexStored{Home: f.home}).DiscoverTitles(t.Context(), f.roots)
			if len(out.Titles) != 0 || count > Limit && len(out.Warnings) == 0 {
				t.Fatal("ambiguous or excessive selected rows accepted", out)
			}
			if _, err = f.db.Exec("BEGIN EXCLUSIVE"); err != nil {
				t.Fatal("failed reader retained its transaction", err)
			}
			if _, err = f.db.Exec("ROLLBACK"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
