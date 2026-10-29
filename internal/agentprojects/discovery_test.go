package agentprojects

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func projectDir(t *testing.T, base, name string) string {
	t.Helper()
	p := filepath.Join(base, name)
	if e := os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	canonical, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestNativeProjectDiscoveryMergesDirectoriesWithoutPrivateContent(t *testing.T) {
	base := t.TempDir()
	codex := projectDir(t, base, "codex")
	claude := projectDir(t, base, "claude")
	shared := projectDir(t, base, "shared")
	selected := projectDir(t, base, "selected")
	running := projectDir(t, base, "marker-directory")
	config := fmt.Sprintf("secret_provider_value = %q\n[projects.%q]\ntrust_level = %q\n", "PRIVATE_ACCOUNT_DATA", shared, "trusted")
	if e := os.WriteFile(filepath.Join(codex, "config.toml"), []byte(config), 0600); e != nil {
		t.Fatal(e)
	}
	writeJSON(t, filepath.Join(codex, ".codex-global-state.json"), map[string]any{"saved-workspace-roots": []string{shared}, "active-workspace-roots": []string{selected}, "thread-titles": map[string]string{"PRIVATE_SESSION_ID": "PRIVATE_CONVERSATION"}})
	writeJSON(t, filepath.Join(claude, ".claude.json"), map[string]any{"projects": map[string]any{shared: map[string]any{"history": []string{"PRIVATE_CONVERSATION"}}}, "oauthAccount": "PRIVATE_ACCOUNT_DATA"})
	writeJSON(t, filepath.Join(claude, "sessions", "fixture.json"), map[string]any{"cwd": running, "sessionId": "PRIVATE_SESSION_ID", "pid": 12345})
	if e := syscall.Mkfifo(filepath.Join(claude, "history.jsonl"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(claude, "projects", "unrelated"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(claude, "projects", "unrelated", "fixture.jsonl"), 0600); e != nil {
		t.Fatal(e)
	}
	catalog := Discover(t.Context(), []Adapter{Codex{Home: codex}, Claude{Home: claude}})
	if len(catalog.Projects) != 3 {
		t.Fatal(catalog)
	}
	for _, adapter := range catalog.Adapters {
		if len(adapter.Warnings) != 0 {
			t.Fatal(adapter.Warnings)
		}
	}
	foundShared, foundSelected, foundMarker := false, false, false
	for _, p := range catalog.Projects {
		switch p.Directory {
		case shared:
			foundShared = len(p.Evidence) == 3
		case selected:
			foundSelected = p.Evidence[0].Kind == "selected-workspace"
		case running:
			foundMarker = p.Evidence[0].Kind == "session-marker"
		}
	}
	if !foundShared || !foundSelected || !foundMarker {
		t.Fatal(catalog)
	}
	b, _ := json.Marshal(catalog)
	if strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("private account/conversation/session data was returned", string(b))
	}
	if !strings.Contains(catalog.Activity, "do not prove running work") {
		t.Fatal(catalog.Activity)
	}
}

func TestDiscoveryMissingUnsafeAndUnsupportedMetadataIsReadOnly(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "absent")
	catalog := Discover(t.Context(), []Adapter{Codex{Home: missing}, Claude{Home: missing}})
	if len(catalog.Projects) != 0 {
		t.Fatal(catalog)
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("discovery created a client home", e)
	}
	root := projectDir(t, base, "client")
	outside := filepath.Join(base, "outside")
	if e := os.WriteFile(outside, []byte(`{"projects":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, ".claude.json")); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(filepath.Join(root, "sessions"), 0600); e != nil {
		t.Fatal(e)
	}
	result := (Claude{Home: root}).Discover(t.Context())
	if len(result.Projects) != 0 || len(result.Warnings) != 2 {
		t.Fatal(result)
	}
	if result = (Codex{Home: "relative"}).Discover(t.Context()); len(result.Projects) != 0 || len(result.Warnings) == 0 {
		t.Fatal(result)
	}
	if e := os.WriteFile(filepath.Join(root, "config.toml"), []byte("invalid = [PRIVATE_ACCOUNT_DATA"), 0600); e != nil {
		t.Fatal(e)
	}
	result = (Codex{Home: root}).Discover(t.Context())
	b, _ := json.Marshal(result)
	if len(result.Warnings) == 0 || strings.Contains(string(b), "PRIVATE_ACCOUNT_DATA") {
		t.Fatal(string(b))
	}
}

func TestDiscoveryCanonicalPathsCancellationAndBudgets(t *testing.T) {
	base := t.TempDir()
	root := projectDir(t, base, "codex")
	project := projectDir(t, base, "project")
	alias := filepath.Join(base, "alias")
	if e := os.Symlink(project, alias); e != nil {
		t.Fatal(e)
	}
	writeJSON(t, filepath.Join(root, ".codex-global-state.json"), map[string]any{"active-workspace-roots": []string{alias, project, "relative", filepath.Join(base, "gone")}})
	if e := os.WriteFile(filepath.Join(root, "config.toml"), []byte(strings.Repeat("x", (1<<20)+1)), 0600); e != nil {
		t.Fatal(e)
	}
	catalog := Discover(t.Context(), []Adapter{Codex{Home: root}})
	if len(catalog.Projects) != 1 || catalog.Projects[0].Directory != project || len(catalog.Adapters[0].Warnings) == 0 {
		t.Fatal(catalog)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	catalog = Discover(ctx, []Adapter{Codex{Home: root}})
	if len(catalog.Projects) != 0 || len(catalog.Adapters) != 0 {
		t.Fatal(catalog)
	}
}

func TestDiscoveryKeepsSelectedAndSessionHintsAtCatalogLimit(t *testing.T) {
	base := t.TempDir()
	codex := projectDir(t, base, "codex")
	claude := projectDir(t, base, "claude")
	var config strings.Builder
	for i := 0; i < maxProjects+1; i++ {
		path := projectDir(t, base, fmt.Sprintf("configured-%03d", i))
		fmt.Fprintf(&config, "[projects.%q]\ntrust_level = \"trusted\"\n", path)
	}
	if e := os.WriteFile(filepath.Join(codex, "config.toml"), []byte(config.String()), 0600); e != nil {
		t.Fatal(e)
	}
	selected := projectDir(t, base, "selected")
	marker := projectDir(t, base, "marker")
	writeJSON(t, filepath.Join(codex, ".codex-global-state.json"), map[string]any{"active-workspace-roots": []string{selected}})
	writeJSON(t, filepath.Join(claude, "sessions", "fixture.json"), map[string]any{"cwd": marker})
	catalog := Discover(t.Context(), []Adapter{Codex{Home: codex}, Claude{Home: claude}})
	foundSelected, foundMarker := false, false
	for _, project := range catalog.Projects {
		foundSelected = foundSelected || project.Directory == selected
		foundMarker = foundMarker || project.Directory == marker
	}
	if len(catalog.Projects) != maxProjects || !foundSelected || !foundMarker {
		t.Fatal("activity hints were displaced by configured-directory history", len(catalog.Projects), foundSelected, foundMarker)
	}
}
