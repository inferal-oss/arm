package cli_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/cli"
	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestCustomNameCLIDisposableBackendPersistenceAndCorruption(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	var stop context.CancelFunc
	var done chan error
	start := func() {
		ctx, cancel := context.WithCancel(context.Background())
		stop, done = cancel, make(chan error, 1)
		go func() {
			done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1 << 30, Disk: 1 << 30})
		}()
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
				return
			}
			select {
			case err := <-done:
				stop()
				stop = nil
				t.Fatalf("backend exited: %v", err)
			default:
			}
			if time.Now().After(deadline) {
				stop()
				t.Fatal("backend not ready")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	shutdown := func() {
		stop()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		stop = nil
	}
	t.Cleanup(func() {
		if stop != nil {
			stop()
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	})
	invoke := func(result any, args ...string) (int, string, string) {
		t.Helper()
		var out, diagnostic bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &diagnostic)
		if code == 0 && result != nil {
			if err := json.Unmarshal(out.Bytes(), result); err != nil {
				t.Fatal(args, err, out.String())
			}
		}
		return code, out.String(), diagnostic.String()
	}
	call := func(result any, args ...string) {
		t.Helper()
		if code, out, diagnostic := invoke(result, args...); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, diagnostic)
		}
	}
	start()
	var status model.Status
	call(&status, "status")
	var first, second model.Lease
	call(&first, "acquire", "--cpu", "0.1", "--reason", "custom naming integration", "--key", "custom-name-first")
	var snapshot struct {
		customnames.Document
		Diagnostic *model.Error `json:"naming_diagnostic"`
	}
	call(&snapshot, "name", "list")
	if snapshot.Revision != 0 || len(snapshot.Entries) != 0 {
		t.Fatal("absent map was not empty", snapshot)
	}
	call(&snapshot, "name", "set", "host", "localhost", "MyHost", "--revision", "0")
	call(&snapshot, "name", "set", "lease", first.ID, "Build", "--revision", "1")
	var inspected model.Lease
	call(&inspected, "inspect", "Build", "--host", "MyHost")
	if inspected.ID != first.ID || inspected.Host != status.Host {
		t.Fatal("custom selector replaced identity", inspected)
	}
	if code, _, diagnostic := invoke(nil, "name", "set", "lease", first.ID, "NewBuild", "--revision", "1"); code == 0 || !strings.Contains(diagnostic, "name_revision_conflict") {
		t.Fatal("stale CAS succeeded", code, diagnostic)
	}
	call(&snapshot, "name", "set", "lease", "Build", "NewBuild", "--revision", "2")
	call(&snapshot, "name", "clear", "lease", first.ID, "Build", "--host", status.Host, "--revision", "3")
	call(&second, "acquire", "--cpu", "0.1", "--reason", "deliberate name reuse", "--key", "custom-name-second")
	call(&snapshot, "name", "set", "lease", second.ID, "Build", "--revision", "4")
	call(&inspected, "inspect", "Build")
	if inspected.ID != second.ID || first.ID == second.ID {
		t.Fatal("cleared name did not deliberately rebind", inspected)
	}
	call(&inspected, "release", first.ID)
	call(&inspected, "release", second.ID)
	shutdown()
	start()
	call(&snapshot, "name", "list", "--all")
	if snapshot.Revision != 5 || len(snapshot.Entries) != 3 {
		t.Fatal("custom map changed across restart", snapshot)
	}
	call(&snapshot, "name", "clear", "lease", second.ID, "Build", "--host", status.Host, "--revision", strconv.FormatInt(snapshot.Revision, 10))
	shutdown()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE metadata SET value='{"invalid":true}' WHERE key='custom_names_v1'`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	start()
	if code, out, diagnostic := invoke(&inspected, "release", first.ID); code != 0 || inspected.ID != first.ID || !strings.Contains(diagnostic, "name_metadata_invalid") {
		t.Fatal("exact-ID cleanup lost result or warning", code, out, diagnostic)
	}
	if code, _, diagnostic := invoke(nil, "inspect", "NewBuild"); code == 0 || !strings.Contains(diagnostic, "name_metadata_invalid") {
		t.Fatal("custom selector ignored corruption", code, diagnostic)
	}
	call(&snapshot, "name", "list", "--all")
	if snapshot.Diagnostic == nil || snapshot.Diagnostic.Code != "name_metadata_invalid" {
		t.Fatal("name list lost corruption diagnostic", snapshot)
	}
}
