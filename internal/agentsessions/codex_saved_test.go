package agentsessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func savedID(n int) string { return fmt.Sprintf("%08x-0000-4000-8000-000000000000", n) }

func savedRequest() SavedSessionRequest { return SavedSessionRequest{Scope: SavedWholeClient} }

func savedFixture(t *testing.T, roots int) storedFixture {
	t.Helper()
	f := newStoredFixture(t, roots)
	if _, err := f.db.Exec("DROP TABLE threads; CREATE TABLE threads(id TEXT,cwd TEXT,title TEXT)"); err != nil {
		t.Fatal(err)
	}
	return f
}

func savedRows(t *testing.T, f storedFixture, rows [][2]string) {
	t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, err = tx.Exec("INSERT INTO threads(id,cwd,title) VALUES(?,?,?)", row[0], row[1], "PRIVATE_PROMPT"); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSavedCatalogRequiresExplicitScope(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, request := range []SavedSessionRequest{{}, {Scope: "unknown"}, {Scope: SavedExactDirectories}, {Scope: SavedWholeClient, Directories: []string{"/work/project"}}, {Scope: SavedExactDirectories, Directories: []string{"relative"}}} {
		out := (CodexStored{Home: home}).DiscoverSavedSessions(t.Context(), request)
		if len(out.Sessions) != 0 || len(out.Warnings) == 0 {
			t.Fatal(out)
		}
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("invalid scope initialized source", err)
	}
}

func TestCodexSavedCatalogNamesUnnamedAndExactScope(t *testing.T) {
	f := savedFixture(t, 2)
	savedRows(t, f, [][2]string{{savedID(1), f.roots[0]}, {savedID(2), f.roots[0]}, {savedID(3), f.roots[1]}})
	f.name(t, savedID(1), "Old name")
	f.name(t, savedID(1), "Explicit saved name")
	out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest())
	if len(out.Sessions) != 3 || len(out.Warnings) != 0 || out.Sessions[0].Ref.ID != savedID(1) || out.Sessions[0].Name != "Explicit saved name" || out.Sessions[1].Name != "" || out.Sessions[2].Directory != f.roots[1] {
		t.Fatal(out)
	}
	for _, child := range out.Sessions {
		if !child.Valid() || child.Ref.Home != f.home || child.Ref.Adapter != "codex" {
			t.Fatal(child)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || bytes.Contains(raw, []byte("PRIVATE_")) || bytes.Contains(raw, []byte("Old name")) {
		t.Fatal("non-name content escaped", err)
	}
	exact := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), SavedSessionRequest{Scope: SavedExactDirectories, Directories: []string{f.roots[1]}})
	if len(exact.Sessions) != 1 || exact.Sessions[0].Ref.ID != savedID(3) {
		t.Fatal(exact)
	}
}

func TestCodexSavedCatalogIndexAvailabilityAndUnsafeRefusal(t *testing.T) {
	for _, mode := range []string{"missing", "malformed-line", "unknown-layout", "symlink", "directory", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			f := savedFixture(t, 1)
			savedRows(t, f, [][2]string{{savedID(1), f.roots[0]}})
			path := filepath.Join(f.home, "session_index.jsonl")
			switch mode {
			case "malformed-line":
				if err := os.WriteFile(path, []byte("{malformed\n"), 0600); err != nil {
					t.Fatal(err)
				}
				f.name(t, savedID(1), "Supported record")
			case "unknown-layout":
				if err := os.WriteFile(path, []byte("{\"other_layout\":[]}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := filepath.Join(t.TempDir(), "index")
				if err := os.WriteFile(other, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, storedIndexBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			}
			out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest())
			if len(out.Warnings) == 0 {
				t.Fatal("coverage warning absent", out)
			}
			if slices.Contains([]string{"symlink", "directory", "oversized"}, mode) {
				if len(out.Sessions) != 0 {
					t.Fatal("unsafe index bypassed through database-only fallback", out)
				}
			} else if len(out.Sessions) != 1 || (mode == "malformed-line" && out.Sessions[0].Name != "Supported record") || (mode != "malformed-line" && out.Sessions[0].Name != "") {
				t.Fatal(out)
			}
		})
	}
}

func TestCodexSavedCatalogRawWindowsConflictsAndSentinels(t *testing.T) {
	t.Run("unique-named-capacity", func(t *testing.T) {
		f := savedFixture(t, 1)
		names := map[string]savedName{}
		var rows [][2]string
		for i := 0; i < Limit; i++ {
			id := savedID(i)
			names[id] = savedName{name: "Explicit name"}
			rows = append(rows, [2]string{id, f.roots[0]})
		}
		rows = append(rows, [2]string{savedID(Limit), f.roots[0]})
		savedRows(t, f, rows)
		result, err := readSavedCatalogRows(t.Context(), filepath.Join(f.home, "state_5.sqlite"), savedRequest(), names, savedCatalogHooks{})
		if err != nil || result.queries != 1 || result.rawRows != 512 || len(result.matches) != 512 || !result.truncated {
			t.Fatal(result, err)
		}
		if _, exists := result.matches[savedID(Limit)]; exists {
			t.Fatal("unnamed row crossed the retained named capacity")
		}
	})
	t.Run("aggregate-1025", func(t *testing.T) {
		f := savedFixture(t, 1)
		var rows [][2]string
		for i := 0; i < Limit; i++ {
			rows = append(rows, [2]string{savedID(1), filepath.Join(f.roots[0], "missing")})
		}
		for i := 2; i < Limit+3; i++ {
			rows = append(rows, [2]string{savedID(i), f.roots[0]})
		}
		savedRows(t, f, rows)
		result, err := readSavedCatalogRows(t.Context(), filepath.Join(f.home, "state_5.sqlite"), savedRequest(), map[string]savedName{savedID(1): {name: "Unavailable cwd"}}, savedCatalogHooks{})
		if err != nil || result.rawRows != 1025 || result.queries != 2 || len(result.matches) != 512 || !result.truncated || !result.unsupported {
			t.Fatal(result, err)
		}
	})
	t.Run("named-513-refuses-before-remaining", func(t *testing.T) {
		f := savedFixture(t, 1)
		var rows [][2]string
		for i := 0; i < Limit+1; i++ {
			rows = append(rows, [2]string{savedID(1), f.roots[0]})
		}
		savedRows(t, f, rows)
		result, err := readSavedCatalogRows(t.Context(), filepath.Join(f.home, "state_5.sqlite"), savedRequest(), map[string]savedName{savedID(1): {name: "Named"}}, savedCatalogHooks{})
		if err == nil || result.rawRows != 513 || result.queries != 1 {
			t.Fatal(result, err)
		}
		f.name(t, savedID(1), "Named")
		if out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest()); len(out.Sessions) != 0 {
			t.Fatal(out)
		}
	})
	t.Run("terminal-duplicate", func(t *testing.T) {
		f := savedFixture(t, 1)
		var rows [][2]string
		for i := 0; i < Limit+1; i++ {
			rows = append(rows, [2]string{savedID(1), f.roots[0]})
		}
		savedRows(t, f, rows)
		out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest())
		if len(out.Sessions) != 0 || !out.Truncated {
			t.Fatal(out)
		}
	})
	t.Run("canonical-only-and-conflicting-cwd", func(t *testing.T) {
		f := savedFixture(t, 2)
		savedRows(t, f, [][2]string{{savedID(10), f.roots[0]}, {strings.ToUpper(savedID(10)), f.roots[1]}, {savedID(2), f.roots[0]}, {savedID(2), f.roots[1]}, {savedID(3), f.roots[0]}, {savedID(3), f.roots[0]}})
		out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest())
		if len(out.Sessions) != 2 || out.Sessions[0].Ref.ID != savedID(3) || out.Sessions[1].Ref.ID != savedID(10) || len(out.Warnings) == 0 {
			t.Fatal(out)
		}
	})
}

func TestCodexSavedCatalogSchemaExtentAndReadonlyWAL(t *testing.T) {
	for _, mode := range []string{"view", "generated", "extent", "wal"} {
		t.Run(mode, func(t *testing.T) {
			f := savedFixture(t, 1)
			savedRows(t, f, [][2]string{{savedID(1), f.roots[0]}})
			f.name(t, savedID(1), "Name")
			switch mode {
			case "view":
				if _, err := f.db.Exec("ALTER TABLE threads RENAME TO backing; CREATE VIEW threads AS SELECT * FROM backing"); err != nil {
					t.Fatal(err)
				}
			case "generated":
				if _, err := f.db.Exec("DROP TABLE threads; CREATE TABLE threads(id TEXT,cwd TEXT GENERATED ALWAYS AS ('/work/guessed') VIRTUAL)"); err != nil {
					t.Fatal(err)
				}
			case "extent":
				file, err := os.Create(filepath.Join(f.home, "state_5.sqlite-journal"))
				if err != nil {
					t.Fatal(err)
				}
				if err = errors.Join(file.Truncate(storedExtentBytes), file.Close()); err != nil {
					t.Fatal(err)
				}
			case "wal":
				if _, err := f.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.Exec("UPDATE threads SET title='PRIVATE_WAL'"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			out := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest())
			after, err := os.ReadFile(filepath.Join(f.home, "state_5.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("main database changed")
			}
			if mode != "wal" {
				if len(out.Sessions) != 0 {
					t.Fatal(out)
				}
				return
			}
			if len(out.Sessions) != 1 || out.Sessions[0].Name != "Name" {
				t.Fatal(out)
			}
			var title string
			if err = f.db.QueryRow("SELECT title FROM threads").Scan(&title); err != nil || title != "PRIVATE_WAL" {
				t.Fatal(title, err)
			}
			if err = f.db.Close(); err != nil {
				t.Fatal(err)
			}
			if next := (CodexStored{Home: f.home}).DiscoverSavedSessions(t.Context(), savedRequest()); len(next.Sessions) != 1 {
				t.Fatal(next)
			}
		})
	}
}

func TestCodexSavedCatalogCloseCancellationAndStabilityDiscard(t *testing.T) {
	for _, kind := range []string{"rows", "connection", "database", "session_index.jsonl", "state_5.sqlite", "home"} {
		t.Run("close-"+kind, func(t *testing.T) {
			f := savedFixture(t, 1)
			savedRows(t, f, [][2]string{{savedID(1), f.roots[0]}})
			f.name(t, savedID(1), "Name")
			observed := false
			hooks := savedCatalogHooks{closed: func(got string, err error) error {
				if err != nil {
					t.Fatal("actual fixture close", err)
				}
				if got == kind {
					observed = true
					return errors.New("owned injected close failure")
				}
				return nil
			}}
			out := (CodexStored{Home: f.home}).discoverSaved(t.Context(), savedRequest(), hooks)
			if !observed || len(out.Sessions) != 0 || len(out.Warnings) == 0 {
				t.Fatal(out, observed)
			}
		})
	}
	for _, mode := range []string{"canceled", "index-replaced", "missing-index-appears", "main-replaced", "home-replaced"} {
		t.Run(mode, func(t *testing.T) {
			f := savedFixture(t, 1)
			savedRows(t, f, [][2]string{{savedID(1), f.roots[0]}})
			if mode != "missing-index-appears" {
				f.name(t, savedID(1), "Name")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			hooks := savedCatalogHooks{beforeStable: func() {
				switch mode {
				case "canceled":
					cancel()
				case "missing-index-appears":
					f.name(t, savedID(1), "Appeared")
				case "home-replaced":
					if err := os.Rename(f.home, f.home+"-retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(f.home, 0700); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.Rename(f.home+"-retained", filepath.Join(f.home, "retained")); err != nil {
							t.Error(err)
						}
					})
				default:
					name := "session_index.jsonl"
					if mode == "main-replaced" {
						name = "state_5.sqlite"
					}
					path := filepath.Join(f.home, name)
					if err := os.Rename(path, path+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}}
			out := (CodexStored{Home: f.home}).discoverSaved(ctx, savedRequest(), hooks)
			if len(out.Sessions) != 0 || len(out.Warnings) == 0 {
				t.Fatal(out)
			}
		})
	}
}

func TestCodexSavedCatalogNameRecordAndTailBounds(t *testing.T) {
	var data []byte
	for i := 0; i < Limit+1; i++ {
		line, err := json.Marshal(map[string]string{"id": savedID(i), "thread_name": fmt.Sprintf("Name %d", i), "updated_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	out := SavedSessionResult{}
	names, err := parseSavedCatalogNames(t.Context(), data, &out)
	if err != nil || len(names) != 512 || names[savedID(0)].name != "" || names[savedID(512)].name != "Name 512" || len(out.Warnings) == 0 {
		t.Fatal(len(names), out, err)
	}
	for _, record := range []string{strings.Repeat("x", (64<<10)+1), `{"id":"unsupported","thread_name":"Never guessed","updated_at":"2026-01-01T00:00:00Z"}`} {
		out = SavedSessionResult{}
		names, err = parseSavedCatalogNames(t.Context(), []byte(record), &out)
		if err != nil || len(names) != 0 || len(out.Warnings) < 2 {
			t.Fatal(names, out, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = parseSavedCatalogNames(ctx, data, &SavedSessionResult{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
