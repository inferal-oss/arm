package agentsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeSavedFixture(t *testing.T) (string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	return home, cwd
}

func writeClaudeSaved(t *testing.T, home, name, id, cwd string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"sessionId": id, "cwd": cwd, "prompt": "PRIVATE_PROMPT", "title": "PRIVATE_TITLE", "startedAt": "PRIVATE_BIRTH"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "sessions", name), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSavedMarkersScopeAndNoNativeIdentity(t *testing.T) {
	home, cwd := claudeSavedFixture(t)
	writeClaudeSaved(t, home, "first.json", savedID(1), cwd)
	out := (Claude{Home: home}).DiscoverSavedSessions(t.Context(), savedRequest())
	if len(out.Sessions) != 1 || out.Sessions[0].Name != "" || out.Sessions[0].Directory != cwd || out.Sessions[0].Ref.Home != home || out.Sessions[0].Ref.Adapter != "claude" || len(out.Warnings) != 1 {
		t.Fatal(out)
	}
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "generation") || strings.Contains(string(raw), "started") {
		t.Fatal(string(raw), err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if exact := (Claude{Home: home}).DiscoverSavedSessions(t.Context(), SavedSessionRequest{Scope: SavedExactDirectories, Directories: []string{other}}); len(exact.Sessions) != 0 {
		t.Fatal(exact)
	}
	if missing := (Claude{Home: home}).DiscoverSavedSessions(t.Context(), SavedSessionRequest{}); len(missing.Sessions) != 0 || len(missing.Warnings) < 2 {
		t.Fatal(missing)
	}
}

func TestClaudeSavedMarkersConflictsAndUnsupportedRecords(t *testing.T) {
	home, cwd := claudeSavedFixture(t)
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeSaved(t, home, "a.json", savedID(1), cwd)
	writeClaudeSaved(t, home, "b.json", savedID(1), other)
	writeClaudeSaved(t, home, "c.json", savedID(2), cwd)
	writeClaudeSaved(t, home, "d.json", savedID(2), cwd)
	writeClaudeSaved(t, home, "e.json", strings.ToUpper(savedID(10)), cwd)
	writeClaudeSaved(t, home, "f.json", savedID(3), filepath.Join(cwd, "missing"))
	if err = os.WriteFile(filepath.Join(home, "sessions", "g.json"), []byte("{malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	out := (Claude{Home: home}).DiscoverSavedSessions(t.Context(), savedRequest())
	if len(out.Sessions) != 1 || out.Sessions[0].Ref.ID != savedID(2) || len(out.Warnings) < 2 {
		t.Fatal(out)
	}
}

func TestClaudeSavedMarkersEntryAndByteBounds(t *testing.T) {
	for _, mode := range []string{"entries", "record", "aggregate", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := claudeSavedFixture(t)
			switch mode {
			case "entries":
				for i := 0; i < Limit+1; i++ {
					if err := os.WriteFile(filepath.Join(home, "sessions", fmt.Sprintf("%04d.json", i)), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "record":
				if err := os.WriteFile(filepath.Join(home, "sessions", "large.json"), []byte(strings.Repeat(" ", (64<<10)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "aggregate":
				for i := 0; i < 65; i++ {
					data, err := json.Marshal(map[string]string{"sessionId": savedID(i), "cwd": cwd})
					if err != nil {
						t.Fatal(err)
					}
					data = append(data, []byte(strings.Repeat(" ", (64<<10)-len(data)))...)
					if err = os.WriteFile(filepath.Join(home, "sessions", fmt.Sprintf("%04d.json", i)), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink":
				path := filepath.Join(t.TempDir(), "marker")
				if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(home, "sessions", "unsafe.json")); err != nil {
					t.Fatal(err)
				}
			}
			out := (Claude{Home: home}).DiscoverSavedSessions(t.Context(), savedRequest())
			if len(out.Sessions) != 0 || len(out.Warnings) < 2 || mode == "entries" && !out.Truncated {
				t.Fatal(out)
			}
		})
	}
}

func TestClaudeSavedMarkersCloseCancellationAndStabilityDiscard(t *testing.T) {
	for _, mode := range []string{"marker", "marker-root", "marker-directory", "home", "replace-marker", "replace-directory", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			home, cwd := claudeSavedFixture(t)
			writeClaudeSaved(t, home, "a.json", savedID(1), cwd)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			closed := false
			hooks := savedCatalogHooks{closed: func(kind string, err error) error {
				if err != nil {
					t.Fatal(err)
				}
				if kind == mode {
					closed = true
					return errors.New("owned injected close failure")
				}
				return nil
			}, beforeStable: func() {
				switch mode {
				case "replace-marker":
					path := filepath.Join(home, "sessions", "a.json")
					if err := os.Rename(path, path+".retained"); err != nil {
						t.Fatal(err)
					}
					writeClaudeSaved(t, home, "a.json", savedID(1), cwd)
				case "replace-directory":
					path := filepath.Join(home, "sessions")
					if err := os.Rename(path, path+".retained"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					cancel()
				}
			}}
			out := (Claude{Home: home}).discoverSaved(ctx, savedRequest(), hooks)
			if len(out.Sessions) != 0 || len(out.Warnings) < 2 {
				t.Fatal(out)
			}
			if !strings.HasPrefix(mode, "replace-") && mode != "cancel" && !closed {
				t.Fatal("expected actual close observer absent")
			}
		})
	}
}

func TestClaudeSavedMarkersCancellationAfterFirstDirectoryValidation(t *testing.T) {
	home, first := claudeSavedFixture(t)
	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeSaved(t, home, "a.json", savedID(1), first)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var validated, opened, closed []string
	hooks := savedCatalogHooks{
		afterDirectory:     func(path string) { validated = append(validated, path); cancel() },
		beforeMetadataOpen: func(kind string) { opened = append(opened, kind) },
		closed:             func(kind string, err error) error { closed = append(closed, kind); return nil },
	}
	reader := Claude{Home: home}
	request := SavedSessionRequest{Scope: SavedExactDirectories, Directories: []string{first, second}}
	out, err := reader.readSaved(ctx, request, hooks)
	if !errors.Is(err, context.Canceled) || len(validated) != 1 || validated[0] != first || len(opened) != 0 || len(closed) != 0 || len(out.Sessions) != 0 {
		t.Fatal(out, err, validated, opened, closed)
	}
	ctx, cancelAgain := context.WithCancel(t.Context())
	defer cancelAgain()
	hooks.afterDirectory = func(string) { cancelAgain() }
	out = reader.discoverSaved(ctx, request, hooks)
	if len(out.Sessions) != 0 || len(out.Warnings) < 2 || len(opened) != 0 || len(closed) != 0 {
		t.Fatal(out, opened, closed)
	}
}
