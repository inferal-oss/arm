package agentsessions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeMetadataHelper(t *testing.T) {
	if os.Getenv("ARM_NATIVE_METADATA_HELPER") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &request) != nil {
			os.Exit(11)
		}
		if request.Method == "initialized" {
			continue
		}
		var result any = map[string]any{"userAgent": "fixture"}
		if request.Method == "thread/list" {
			if roots, ok := request.Params["cwd"].([]any); !ok || len(roots) == 0 {
				os.Exit(16)
			}
			if request.Params["useStateDbOnly"] != true || request.Params["archived"] != false {
				os.Exit(12)
			}
			b, e := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "threads-fixture.json"))
			if e != nil {
				os.Exit(13)
			}
			if json.Unmarshal(b, &result) != nil {
				os.Exit(14)
			}
		} else if request.Method != "initialize" {
			os.Exit(15)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"id": request.ID, "result": result})
	}
	os.Exit(0)
}
func testJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCodexMetadataUsesReadOnlyAPIAndIgnoresConversationFields(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	script := filepath.Join(t.TempDir(), "codex")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if e = os.WriteFile(script, []byte("#!/bin/sh\nexec "+quoted+" -test.run '^TestNativeMetadataHelper$'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("ARM_NATIVE_METADATA_HELPER", "1")
	id := uuid.NewString()
	testJSON(t, filepath.Join(home, "threads-fixture.json"), map[string]any{"data": []any{map[string]any{"id": id, "cwd": cwd, "createdAt": 1760000000, "updatedAt": 1760000010, "status": map[string]any{"type": "active"}, "preview": "PRIVATE_CONVERSATION", "name": "PRIVATE_TITLE"}}, "nextCursor": nil})
	out := (Codex{Home: home, Binary: script, Roots: []string{cwd}}).Discover(t.Context())
	if len(out.Sessions) != 1 || len(out.Warnings) != 0 || out.Sessions[0].ID != id || out.Sessions[0].Activity != "active" {
		t.Fatal(out)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("conversation data leaked", string(b))
	}
	if _, e = os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}
func TestClaudeSessionMarkersAreBoundedAndUnverified(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := uuid.NewString()
	path := filepath.Join(home, "sessions", "fixture.json")
	testJSON(t, path, map[string]any{"sessionId": id, "cwd": cwd, "startedAt": "2026-01-01T00:00:00Z", "status": "busy", "pid": 42, "messagingSocketPath": "PRIVATE_SOCKET", "preview": "PRIVATE_CONVERSATION"})
	if e := syscall.Mkfifo(filepath.Join(home, "sessions", "blocked.json"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(home, "history.jsonl"), []byte("PRIVATE_HISTORY"), 0600); e != nil {
		t.Fatal(e)
	}
	out := (Claude{Home: home}).Discover(t.Context())
	if len(out.Sessions) != 1 || out.Sessions[0].ID != id || !out.Sessions[0].Started.IsZero() || out.Sessions[0].InstanceStart.IsZero() || !strings.Contains(out.Sessions[0].Evidence, "unverified") {
		t.Fatal(out)
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("private metadata leaked", string(b))
	}
	old := out.Sessions[0].Generation
	testJSON(t, path, map[string]any{"sessionId": id, "cwd": cwd, "startedAt": "2026-01-01T00:00:00Z", "status": "idle"})
	out = (Claude{Home: home}).Discover(t.Context())
	if out.Sessions[0].Generation != old {
		t.Fatal("heartbeat changed identity")
	}
	testJSON(t, path, map[string]any{"sessionId": id, "cwd": cwd, "startedAt": "2026-01-02T00:00:00Z"})
	out = (Claude{Home: home}).Discover(t.Context())
	if out.Sessions[0].Generation == old {
		t.Fatal("replacement native start retained generation")
	}
}
func TestNativeMetadataMissingUnsafeAndMalformed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, source := range []Source{Claude{Home: missing}, Codex{Home: missing, Binary: "/nonexistent"}} {
		if out := source.Discover(t.Context()); len(out.Sessions) != 0 {
			t.Fatal(out)
		}
	}
	if _, e := os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("discovery created client home")
	}
	home := t.TempDir()
	outside := t.TempDir()
	testJSON(t, filepath.Join(outside, "marker.json"), map[string]any{"sessionId": uuid.NewString(), "cwd": outside})
	if e := os.Symlink(outside, filepath.Join(home, "sessions")); e != nil {
		t.Fatal(e)
	}
	if out := (Claude{Home: home}).Discover(t.Context()); len(out.Sessions) != 0 || len(out.Warnings) == 0 {
		t.Fatal(out)
	}
	for _, bad := range []Session{{Adapter: "claude", ID: "not-a-uuid", CWD: outside, Home: home}, {Adapter: "claude", ID: uuid.NewString(), CWD: "relative", Home: home}, {Adapter: "codex", ID: uuid.NewString(), CWD: outside, Home: home, Started: time.Now().Add(time.Hour)}} {
		if _, e := Normalize(bad); e == nil {
			t.Fatal(fmt.Sprintf("unsupported metadata accepted: %+v", bad))
		}
	}
}
