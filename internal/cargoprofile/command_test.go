package cargoprofile

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClosedCommandsAndPositiveJobs(t *testing.T) {
	for _, args := range [][]string{{"cargo", "build"}, {"/tools/cargo", "check", "--workspace", "-j1", "--features", "a,b"}, {"cargo", "test", "--no-run", "--tests", "--jobs=2"}, {"cargo", "build", "-vv"}} {
		c, e := Parse(args, 2.9)
		if e != nil {
			t.Fatal(args, e)
		}
		if c.Jobs < 1 || c.Jobs > 2 {
			t.Fatal(c)
		}
		if !reflect.DeepEqual(c.Original, args) {
			t.Fatal("intent rewritten")
		}
	}
	for _, args := range [][]string{{"sh", "-c", "cargo build"}, {"cargo", "b"}, {"cargo", "controlled"}, {"cargo", "+nightly", "build"}, {"cargo", "test"}, {"cargo", "test", "--no-run", "--", "--test-threads=1"}, {"cargo", "build", "--no-run"}, {"cargo", "test", "--no-run", "--doc"}, {"cargo", "build", "--jobs=0"}, {"cargo", "build", "-j-1"}, {"cargo", "build", "-j3"}, {"cargo", "build", "-j1", "--jobs=1"}, {"cargo", "build", "--config", "secret-value"}, {"cargo", "build", "--target-dir", "elsewhere"}, {"cargo", "build", "--target", "custom.json"}, {"cargo", "build", "--manifest-path"}, {"cargo", "build", "--jobs", "01"}} {
		if _, e := Parse(args, 2); e == nil {
			t.Fatal("unsupported command accepted", args)
		}
	}
	for _, cpu := range []float64{0, 0.5, math.Inf(1), math.NaN()} {
		if _, e := Parse([]string{"cargo", "build"}, cpu); e == nil {
			t.Fatal(cpu)
		}
	}
	if _, e := Parse([]string{"cargo", "build", "secret-token-value"}, 1); e == nil || strings.Contains(e.Error(), "secret-token-value") {
		t.Fatal("unsafe argument echoed", e)
	}
}
func TestMetadataArgumentsKeepManifestAndFeaturesWithoutCompileControls(t *testing.T) {
	c, e := Parse([]string{"cargo", "test", "--no-run", "--package", "p", "--features", "f", "--manifest-path", "elsewhere/Cargo.toml", "--locked", "--target", "aarch64-unknown-linux-gnu"}, 1)
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"metadata", "--format-version", "1", "--no-deps", "--offline", "--features", "f", "--manifest-path", "elsewhere/Cargo.toml", "--locked"}
	if !reflect.DeepEqual(c.MetadataArguments(), want) {
		t.Fatal(c.MetadataArguments())
	}
}

func TestInvalidUTF8AndCustomTargetSearchRefused(t *testing.T) {
	for _, argv := range [][]string{{"cargo", "build", "--features", string([]byte{0xff})}, {"cargo", "build", "--target", "."}, {"cargo", "build", "--target", "custom"}} {
		if _, e := Parse(argv, 1); e == nil {
			t.Fatal("ambiguous command accepted")
		}
	}
	root := fixtureRoot(t)
	for _, env := range [][]string{{"RUST_TARGET_PATH=private-secret"}, {"RUST_TARGET_PATH=private-secret", "RUST_TARGET_PATH="}} {
		if _, e := Discover(t.Context(), root, root, env); e == nil || strings.Contains(e.Error(), "private-secret") {
			t.Fatal("custom target search accepted/leaked", e)
		}
	}
	write(t, filepath.Join(root, ".cargo", "config.toml"), "[env]\nRUST_TARGET_PATH={value='private-secret',force=true}\n")
	if _, e := Discover(t.Context(), root, root, nil); e == nil || strings.Contains(e.Error(), "private-secret") {
		t.Fatal("config custom target search accepted/leaked", e)
	}
}
