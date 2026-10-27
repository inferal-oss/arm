package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/cli"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestCLIStorageExportRetainAndOutputSurvivesCleanup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1 << 20, Disk: 4 << 20})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := func(args ...string) (int, []byte, string) {
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		return code, out.Bytes(), stderr.String()
	}
	code, data, stderr := call("storage", "create", "1MiB", "--ttl", "2m", "--reason", "Export test artifacts")
	var source model.Lease
	if code != 0 || stderr != "" || json.Unmarshal(data, &source) != nil {
		t.Fatal(code, string(data), stderr)
	}
	if err := os.WriteFile(filepath.Join(source.ManagedDir, "program"), []byte("#!/bin/sh\nprintf 'kept-output\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.ManagedDir, "runtime.dat"), []byte("selected runtime data"), 0600); err != nil {
		t.Fatal(err)
	}
	code, data, stderr = call("storage", "retain", source.ID, "--ttl", "40s", "--reason", "Only export validation remains")
	if code != 0 || stderr != "" || json.Unmarshal(data, &source) != nil || source.Artifact.Retention == nil {
		t.Fatal(code, string(data), stderr)
	}
	destination := filepath.Join(t.TempDir(), "bundle")
	args := []string{"storage", "export", source.ID, destination, "--file", "program=bin/program", "--file", "runtime.dat=share/runtime.dat", "--reason", "Keep only requested deliverables", "--key", "cli-export-key"}
	code, data, stderr = call(append(args, "--dry-run")...)
	var plan model.StorageExportPlan
	if code != 0 || stderr != "" || json.Unmarshal(data, &plan) != nil || len(plan.Files) != 2 {
		t.Fatal(code, string(data), stderr)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("preview created output", err)
	}
	code, data, stderr = call(append(args, "--wait")...)
	var operation model.StorageExport
	if code != 0 || stderr != "" || json.Unmarshal(data, &operation) != nil || operation.State != "published" {
		t.Fatal(code, string(data), stderr)
	}
	code, data, stderr = call(append(args, "--wait")...)
	var retry model.StorageExport
	if code != 0 || stderr != "" || json.Unmarshal(data, &retry) != nil || retry.ID != operation.ID {
		t.Fatal("exact retry recopied output", code, string(data), stderr)
	}
	conflicting := append([]string(nil), args...)
	for i := range conflicting {
		if conflicting[i] == "Keep only requested deliverables" {
			conflicting[i] = "Changed purpose"
		}
	}
	code, _, stderr = call(conflicting...)
	if code == 0 || !strings.Contains(stderr, "idempotency_conflict") || !strings.Contains(stderr, "cli-export-key") {
		t.Fatal("conflict lost retry identity", code, stderr)
	}
	code, data, stderr = call("storage", "exports", "--lease", source.ID)
	var operations []model.StorageExport
	if code != 0 || stderr != "" || json.Unmarshal(data, &operations) != nil || len(operations) != 1 {
		t.Fatal(code, string(data), stderr)
	}
	for _, action := range []string{"inspect", "reconcile"} {
		if code, data, stderr = call("storage", "exports", action, operation.ID); code != 0 || stderr != "" || !json.Valid(data) {
			t.Fatal(action, code, string(data), stderr)
		}
	}
	if code, data, stderr = call("release", source.ID); code != 0 || stderr != "" {
		t.Fatal(code, string(data), stderr)
	}
	deadline = time.Now().Add(35 * time.Second)
	for {
		code, data, stderr = call("inspect", source.ID)
		if code != 0 || json.Unmarshal(data, &source) != nil {
			t.Fatal(code, string(data), stderr)
		}
		if source.Artifact.NamespaceRemoved {
			break
		}
		if time.Now().After(deadline) || source.State == "needs_attention" {
			t.Fatal("source namespace not reclaimed", source)
		}
		time.Sleep(25 * time.Millisecond)
	}
	output, err := exec.Command(filepath.Join(destination, "bin/program")).CombinedOutput()
	if err != nil || string(output) != "kept-output\n" {
		t.Fatal("exported output stopped working after cleanup", string(output), err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "share/runtime.dat")); err != nil || string(data) != "selected runtime data" {
		t.Fatal("runtime output was not retained", string(data), err)
	}
	if source.State != "terminal" {
		if code, data, stderr = call("storage", "handoff", source.ID); code != 0 || stderr != "" {
			t.Fatal(code, string(data), stderr)
		}
	}
}

func TestCLIStorageExportInvalidArgumentsDoNotCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"storage", "export"},
		{"storage", "export", "LEASE", "dest", "--reason", "test"},
		{"storage", "export", "LEASE", "dest", "--file", "../outside", "--reason", "test"},
		{"storage", "export", "LEASE", "dest", "--file", "source", "--reason", "test", "--wait", "--dry-run"},
		{"storage", "exports", "unknown", "OP"},
		{"storage", "exports", "inspect"},
		{"storage", "exports", "handoff", "OP"},
		{"storage", "retain", "LEASE", "--ttl", "0s", "--reason", "test"},
		{"storage", "retain", "LEASE", "--ttl", "25h", "--reason", "test"},
	} {
		var out, stderr bytes.Buffer
		if code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr); code == 0 || out.Len() != 0 || !json.Valid(stderr.Bytes()) {
			t.Fatal(args, code, out.String(), stderr.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("invalid export arguments created state", err)
	}
}
