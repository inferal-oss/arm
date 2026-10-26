package distribution

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCarrierTargetValidationPrecedesSourceCompilerAndOutput(t *testing.T) {
	for _, target := range [][2]string{{"linux", ""}, {"", "arm64"}, {"windows", "amd64"}, {"darwin", "riscv64"}, {"LINUX", "arm64"}, {"linux/escape", "arm64"}} {
		t.Run(strings.Join(target[:], "-"), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated")
			options := Options{Root: root, GoBinary: "must-not-run-invalid-carrier", Output: "output", Version: "test", Protocol: 1, CarrierOS: target[0], CarrierArch: target[1]}
			if _, _, err := BuildCarrier(t.Context(), options); err == nil || !strings.Contains(err.Error(), "carrier") {
				t.Fatal("invalid carrier target accepted or validation occurred too late", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("invalid target created source/output", err)
			}
		})
	}
	for _, target := range platforms {
		o := Options{CarrierOS: target[0], CarrierArch: target[1]}
		if _, _, err := Build(t.Context(), o); err == nil || !strings.Contains(err.Error(), "carrier target") {
			t.Fatal("archive silently ignored carrier target", target, err)
		}
	}
}

func TestCarrierTargetDefaultsNativeAndAcceptsSupportedPairs(t *testing.T) {
	goos, arch, err := carrierTarget(Options{})
	if err != nil || goos != runtime.GOOS || arch != runtime.GOARCH {
		t.Fatal("native default changed", goos, arch, err)
	}
	for _, target := range platforms {
		goos, arch, err = carrierTarget(Options{CarrierOS: target[0], CarrierArch: target[1]})
		if err != nil || goos != target[0] || arch != target[1] {
			t.Fatal(target, goos, arch, err)
		}
	}
}

// Build a tiny real module through the same final-compile boundary used by
// BuildCarrier. This checks cross-target compiler output and metadata without
// compiling/compressing all four full ARM payloads in an ordinary unit test.
func TestCarrierSelectedTargetMatchesActualGoExecutableMetadata(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd/arm", "internal/buildinfo", "third_party/rsyncgo", "knowledge"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"go.mod":                        "module github.com/yrashk/arm\n\ngo 1.27.3\nrequire github.com/gokrazy/rsync v0.0.0\nreplace github.com/gokrazy/rsync => ./third_party/rsyncgo\n",
		"go.sum":                        "",
		"internal/buildinfo/tag.go":     "package buildinfo\nvar BundleTag string\n",
		"third_party/rsyncgo/go.mod":    "module github.com/gokrazy/rsync\n\ngo 1.27.3\n",
		"third_party/rsyncgo/engine.go": "package rsync\nconst Fixture = \"local-replace-input\"\n",
		"third_party/rsyncgo/LICENSE":   "fixture license copied with source",
		"cmd/arm/main.go":               "package main\nimport b \"github.com/yrashk/arm/internal/buildinfo\"\nimport r \"github.com/gokrazy/rsync\"\nfunc main(){println(b.BundleTag,r.Fixture)}\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	target := [2]string{"linux", "arm64"}
	if target == [2]string{runtime.GOOS, runtime.GOARCH} {
		target = [2]string{"darwin", "amd64"}
	}
	var log bytes.Buffer
	options := Options{Version: "test-target", Protocol: 1, CarrierOS: target[0], CarrierArch: target[1], Log: &log}
	goos, arch, err := carrierTarget(options)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "arm")
	snapshot := filepath.Join(t.TempDir(), "isolated-source")
	if err = copySource(t.Context(), root, snapshot); err != nil {
		t.Fatal(err)
	}
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if err = buildCarrierExecutable(t.Context(), options, goBinary, snapshot, output, goos, arch); err != nil {
		t.Fatal(err, log.String())
	}
	flags := "-X=" + tagVariable + "=arm-bundle-v1:test-target:1"
	artifact, err := inspect(output, "arm", target[0], target[1], flags)
	if err != nil || artifact.OS != target[0] || artifact.Arch != target[1] || artifact.Size <= 0 {
		t.Fatal("cross-target metadata mismatch", artifact, err)
	}
	if _, err = inspect(output, "arm", runtime.GOOS, runtime.GOARCH, flags); err == nil {
		t.Fatal("cross-target binary was accepted as native")
	}
	if !strings.Contains(log.String(), "Building carrier "+target[0]+"/"+target[1]) {
		t.Fatal("carrier log obscures actual target", log.String())
	}
}
