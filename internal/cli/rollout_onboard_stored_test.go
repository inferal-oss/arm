package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agentprojects"
	"github.com/google/uuid"
)

func TestRolloutOnboardStoredNamesWithoutNativeBinary(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "arm-state")
	native := filepath.Join(base, "selected-native")
	directory := filepath.Join(base, "generic")
	for _, path := range []string{native, directory} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(native, "config.toml"), []byte(fmt.Sprintf("[projects.%q]\n", directory)), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(native, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY,cwd TEXT NOT NULL,title TEXT)"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = db.Exec("INSERT INTO threads VALUES(?,?,?)", id, directory, "PRIVATE_PROMPT"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"id": id, "thread_name": "Recognizable saved task", "updated_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)})
	if err = os.WriteFile(filepath.Join(native, "session_index.jsonl"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	// No usable client is available. A mistaken native launch leaves a marker.
	bin := filepath.Join(base, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, "client-started")
	script := "#!/bin/sh\n: > '" + strings.ReplaceAll(marker, "'", "'\\''") + "'\nexit 73\n"
	if err = os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CODEX_HOME", filepath.Join(base, "unselected-native"))
	code, out, stderr := rolloutCLI(t, home, "rollout", "project", "onboard", "--adapter", "codex", "--codex-home", native, "--json")
	var result struct {
		ReadOnly bool                  `json:"read_only"`
		Catalog  agentprojects.Catalog `json:"catalog"`
	}
	if code != 0 || json.Unmarshal(out, &result) != nil || !result.ReadOnly || len(result.Catalog.Projects) != 1 {
		t.Fatal(code, string(out), stderr)
	}
	item := onboardItem{project: result.Catalog.Projects[0]}
	if !strings.Contains(item.Title(), "Recognizable saved task") || !strings.Contains(item.Description(), directory) || !strings.Contains(item.FilterValue(), "Recognizable") || strings.Contains(string(out), "PRIVATE_") {
		t.Fatal(item, string(out))
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("native client started", err)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("discovery initialized ARM policy/backend", err)
	}
}
