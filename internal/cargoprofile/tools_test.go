package cargoprofile

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestToolVersionsTargetsAndQueryEnvironmentAreClosed(t *testing.T) {
	if v, e := stableRelease([]byte("cargo 1.99.0 (fixture)\nhost: aarch64-apple-darwin\n"), "cargo", 91); e != nil || v != "1.99.0" {
		t.Fatal(v, e)
	}
	for _, v := range []string{"cargo 1.99.0-nightly (fixture)", "cargo 1.90.0", "cargo 9.99.0", "private-secret"} {
		if _, e := stableRelease([]byte(v), "cargo", 91); e == nil || strings.Contains(e.Error(), "private-secret") {
			t.Fatal(e)
		}
	}
	if _, e := activeToolchain([]byte("/unmodeled/toolchain (environment override)")); e == nil {
		t.Fatal("custom path toolchain accepted")
	}
	if _, e := onePath([]byte("/one\n/two")); e == nil {
		t.Fatal("ambiguous component path accepted")
	}
	for _, bad := range [][]byte{[]byte("wasm32-unknown-unknown\nwasm32-unknown-unknown"), append([]byte("wasm32-unknown-unknown"), 0xff)} {
		if _, e := parseBuiltinTargets(bad); e == nil {
			t.Fatal("malformed builtin target evidence accepted")
		}
	}
	list, e := parseBuiltinTargets([]byte("wasm32-unknown-unknown\naarch64-unknown-linux-gnu\n"))
	if e != nil {
		t.Fatal(e)
	}
	targets := []Target{{Selector: "wasm32-unknown-unknown", Origin: "config"}, {Selector: "aarch64-unknown-linux-gnu", Origin: "explicit-command"}}
	got, e := checkBuiltinTargets(targets, list)
	if e != nil || !reflect.DeepEqual(got, targets) {
		t.Fatal(got, e)
	}
	got[0].Selector = "mutated"
	if targets[0].Selector == "mutated" {
		t.Fatal("mutable checked target slice escaped")
	}
	if _, e = checkBuiltinTargets([]Target{{Selector: "looks-like-builtin", Origin: "config"}}, list); e == nil {
		t.Fatal("syntactic custom target accepted without builtin evidence")
	}
	env := queryEnvironment(map[string]string{"PATH": "/bin", "RUSTUP_AUTO_INSTALL": "1", "RUSTUP_TRACE_DIR": "private-secret", "RUSTC_WRAPPER": "wrapper", "MAKEFLAGS": "stale"}, "/private-cache")
	values := map[string]string{}
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		values[k] = v
	}
	if values["RUSTUP_AUTO_INSTALL"] != "0" || values["RUSTC_WRAPPER"] != "" || values["MAKEFLAGS"] != "" || values["RUSTUP_TRACE_DIR"] != "" {
		t.Fatal("unsafe tool query environment")
	}
}
func TestExecutableAndPathEvidenceRejectsWrappersAndReplacement(t *testing.T) {
	root := fixtureRoot(t)
	path := filepath.Join(root, "cargo")
	write(t, path, "#!/bin/sh\nexit 0\n")
	os.Chmod(path, 0700)
	if _, e := snapshotExecutable(context.Background(), path); e == nil {
		t.Fatal("script accepted as native compiler")
	}
	if _, e := resolveInstalledSelector("cargo", root, ".:/bin"); e == nil {
		t.Fatal("relative executable PATH accepted")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	evidence, e := snapshotExecutable(context.Background(), executable)
	if e != nil {
		t.Fatal(e)
	}
	if evidence.SHA256 == "" || evidence.Inode == 0 {
		t.Fatal(evidence)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = snapshotExecutable(ctx, executable); e == nil {
		t.Fatal("cancelled executable hash continued")
	}
}

// Err is an existing cancellation checkpoint inside bounded file streaming, so
// this fixture mutates the copied executable during inspection without a
// production-only test hook or a timing-dependent concurrent chmod.
type executableMutationContext struct {
	context.Context
	mutate func()
}

func (c *executableMutationContext) Err() error {
	if c.mutate != nil {
		f := c.mutate
		c.mutate = nil
		f()
	}
	return c.Context.Err()
}
func TestExecutableEvidenceRejectsModeMutationDuringHash(t *testing.T) {
	root := fixtureRoot(t)
	own, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	in, e := os.Open(own)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	path := filepath.Join(root, "native-tool")
	out, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		t.Fatal(e)
	}
	if ce != nil {
		t.Fatal(ce)
	}
	ctx := &executableMutationContext{Context: context.Background(), mutate: func() {
		if e := os.Chmod(path, 0500); e != nil {
			t.Fatal(e)
		}
	}}
	if _, e = snapshotExecutable(ctx, path); e == nil {
		t.Fatal("mode changed during inspection but original evidence accepted")
	}
	if _, e = snapshotExecutable(context.Background(), path); e != nil {
		t.Fatal("stable copied native tool refused", e)
	}
	for _, bit := range []os.FileMode{os.ModeSetuid, os.ModeSetgid, os.ModeSticky} {
		if e = os.Chmod(path, 0700|bit); e != nil {
			t.Fatal(e)
		}
		if _, e = snapshotExecutable(context.Background(), path); e == nil {
			t.Fatal("special executable bits accepted", bit)
		}
	}
	if e = os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	evidence, e := snapshotExecutable(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	// Replacing a file with byte-identical contents still changes its native
	// identity; a previously prepared tool must not acquire the replacement.
	replacement := filepath.Join(root, "replacement")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(replacement, raw, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(replacement, path); e != nil {
		t.Fatal(e)
	}
	tools := Toolchain{Selected: evidence, Cargo: evidence, Rustc: evidence, Rustdoc: evidence}
	if e = tools.revalidateFiles(context.Background()); e == nil {
		t.Fatal("byte-identical executable replacement accepted")
	}
}
