package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/cli"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

func TestCLIExecutionExpiryAndUninstallGuard(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1 << 30, Disk: 1 << 30})
	}()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(home, "endpoint.json")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := func(args ...string) (int, string, string) {
		var out, err bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), bytes.NewReader(nil), &out, &err)
		return code, out.String(), err.String()
	}
	code, out, err := call("run", "--reason", "CLI exit fixture", "--", "/bin/sh", "-c", "printf 'hello arm'; exit 7")
	if code != 7 || !json.Valid([]byte(out)) || err != "hello arm" {
		t.Fatalf("output or exit status lost: %d %q %q", code, out, err)
	}
	for _, boundary := range [][]string{{"--", "/bin/sh"}, {"/bin/sh"}} {
		args := append([]string{"run", "--reason", "child option boundary"}, boundary...)
		args = append(args, "-c", `printf '%s' "$@"`, "fixture", "--reason", "child", "--help", "--cpu", "8")
		code, out, err = call(args...)
		if code != 0 || !json.Valid([]byte(out)) || err != "--reasonchild--help--cpu8" {
			t.Fatalf("child flags interpreted by ARM: %d %q %q", code, out, err)
		}
	}
	for _, args := range [][]string{
		{"storage", "create", "10MiB", "-reason", "q"},
		{"storage", "create", "--reason", "q", "10MiB"},
		{"storage", "create", "--disk", "10MiB", "--reason", "q"},
	} {
		code, out, err = call(args...)
		var storage model.Lease
		if code != 0 || json.Unmarshal([]byte(out), &storage) != nil || storage.Request.Reason != "q" || storage.Request.Resources.Disk != 10<<20 || storage.Request.Resources.CPU != 0 {
			t.Fatalf("storage argument parsing: %d %s %s", code, out, err)
		}
		if code, _, err = call("storage", "handoff", "--json", storage.ID); code != 0 {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	if e := os.WriteFile(filepath.Join(work, "size-fixture"), []byte("arm"), 0600); e != nil {
		t.Fatal(e)
	}
	code, out, err = call("storage", "inspect", work, "--json")
	var measured model.StorageMeasurement
	if code != 0 || json.Unmarshal([]byte(out), &measured) != nil || measured.Logical.Value == nil || *measured.Logical.Value != 3 || measured.Reclaimable.Value != nil {
		t.Fatalf("storage inspection contract: %d %s %s", code, out, err)
	}
	os.WriteFile(filepath.Join(work, "fixture.sh"), []byte("#!/bin/sh\nprintf relative-cwd"), 0700)
	code, out, err = call("run", "--reason", "relative executable fixture", "--cwd", work, "--", "./fixture.sh")
	if code != 0 || !json.Valid([]byte(out)) || err != "relative-cwd" {
		t.Fatalf("cwd executable resolution: %d %q %q", code, out, err)
	}
	code, out, err = call("run", "--reason", "CLI expiry fixture", "--ttl", "50ms", "--json", "--", "/bin/sleep", "10")
	var expired model.Lease
	if code != 124 || json.Unmarshal([]byte(out), &expired) != nil || expired.Outcome != "expired" {
		t.Fatalf("expiry contract: %d %q %q", code, out, err)
	}
	root := t.TempDir()
	cx := filepath.Join(root, "codex")
	cc := filepath.Join(root, "claude")
	if code, _, err = call("install", "--yes", "--codex-home", cx, "--claude-home", cc); code != 0 {
		t.Fatal(err)
	}
	code, out, err = call("acquire", "--cpu", "1", "--reason", "protect uninstall", "--ttl", "1m")
	if code != 0 {
		t.Fatal(err)
	}
	var lease model.Lease
	if e := json.Unmarshal([]byte(out), &lease); e != nil {
		t.Fatal(e)
	}
	if code, _, _ = call("uninstall", "--yes"); code == 0 {
		t.Fatal("uninstall ignored outstanding lease")
	}
	if _, e := os.Stat(filepath.Join(cx, "AGENTS.md")); e != nil {
		t.Fatal("blocked uninstall changed instructions")
	}
	if code, _, err = call("release", lease.ID); code != 0 {
		t.Fatal(err)
	}
	code, out, err = call("uninstall", "--yes")
	if code != 0 {
		t.Fatal(err)
	}
	var report map[string]any
	if e := json.Unmarshal([]byte(out), &report); e != nil {
		t.Fatalf("uninstall output is not one JSON response: %s", out)
	}
	if report["complete"] != true {
		t.Fatalf("uninstall incomplete: %s", out)
	}
}
