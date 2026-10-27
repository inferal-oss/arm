package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func databaseCLIState(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "arm")
	m, err := manager.Open(home, model.Resources{CPU: 1, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(model.Request{Resources: model.Resources{CPU: 1}, Reason: "offline statistics fixture", TTL: 1e9}, nil); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	return home
}
func TestDatabaseCLIStatsCheckBackupAndHelp(t *testing.T) {
	home := databaseCLIState(t)
	for _, args := range [][]string{{"database", "stats"}, {"database", "check"}, {"database", "check", "--full"}, {"database", "backup", filepath.Join(home, "copy.db")}} {
		var out, errout bytes.Buffer
		code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout)
		if code != 0 {
			t.Fatal(args, code, errout.String())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(args, out.String(), err)
		}
		if result["scope"] == nil {
			t.Fatal("inspection scope omitted", out.String())
		}
	}
	for _, args := range [][]string{{"database", "--help"}, {"database", "stats", "--help"}, {"help", "database", "backup"}} {
		var out, errout bytes.Buffer
		if code := Main(args, strings.NewReader(""), &out, &errout); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(args, code, out.String(), errout.String())
		}
	}
}
func TestDatabaseCLIInvalidArgsAndMissingStateDoNotCreateIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"database", "stats", "extra"}, {"database", "check", "--unknown"}, {"database", "backup"}, {"database", "stats", "--full"}, {"database", "remove"}, {"database", "stats"}} {
		var out, errout bytes.Buffer
		if code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout); code == 0 {
			t.Fatal("invalid request accepted", args, out.String())
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("invalid/missing inspection created state")
		}
	}
}
func TestDatabaseHumanStatsUsesReadableTerminalOutput(t *testing.T) {
	home := databaseCLIState(t)
	var out bytes.Buffer
	a := app{home: home, ctx: context.Background(), in: strings.NewReader(""), out: &out, err: &out, outputTTY: true}
	if code, err := a.database([]string{"stats"}); err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if !strings.Contains(out.String(), "Page") || strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Fatal(out.String())
	}
	var exact database.Stats
	b, _ := json.Marshal(database.Stats{Logical: 9223372036854775807})
	json.Unmarshal(b, &exact)
	if exact.Logical != 9223372036854775807 {
		t.Fatal("stats rounded exact bytes")
	}
}
