package mesh

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	armBuild "getarm.org/internal/buildinfo"
	"getarm.org/internal/local"
)

// Exercise the actual Go buildinfo boundary without running either fixture.
// These tiny standard-library-only builds use the caller's bounded test gate.
func namespaceArtifact(t *testing.T, module string) (string, []byte) {
	t.Helper()
	root := t.TempDir()
	command := filepath.Join(root, "cmd", "arm")
	if err := os.MkdirAll(command, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.27.3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(command, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "arm")
	build := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=readonly", "-p=1", "-trimpath", "-o", path, "./cmd/arm")
	build.Dir = root
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOTOOLCHAIN", "GOWORK", "GOENV", "GOPROXY", "GOSUMDB", "GOMAXPROCS":
			continue
		}
		build.Env = append(build.Env, entry)
	}
	build.Env = append(build.Env, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0", "GOFLAGS=-p=1", "GOTOOLCHAIN=local", "GOWORK=off", "GOENV=off", "GOPROXY=off", "GOSUMDB=sum.golang.org", "GOMAXPROCS=1")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func TestVanityNamespacePreservesExistingTrustedCatalogPolicy(t *testing.T) {
	legacyPath, legacyBytes := namespaceArtifact(t, "github.com/inferal-oss/arm")
	vanityPath, _ := namespaceArtifact(t, "getarm.org")
	if _, err := InspectArtifact(legacyPath, "fresh legacy import must be refused"); err == nil {
		t.Fatal("fresh GitHub-path artifact passed vanity module inspection")
	}
	if _, err := InspectArtifact(vanityPath, "fresh vanity artifact"); err != nil {
		t.Fatal("vanity artifact refused", err)
	}

	digest := sha256.Sum256(legacyBytes)
	legacy := Artifact{Schema: 1, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(legacyBytes)), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: armBuild.Version, Protocol: LifecycleProtocol, Imported: time.Now().UTC(), Actor: local.CurrentIdentity(), Reason: "already trusted pre-vanity catalog fixture"}

	// Seed a historical trusted catalog directly. Going through today's import
	// inspector here would test a new trust decision instead of existing trust.
	home := filepath.Join(t.TempDir(), "existing-home")
	if err := local.Prepare(home); err != nil {
		t.Fatal(err)
	}
	directory, err := artifactDirectory(home, true)
	if err != nil {
		t.Fatal(err)
	}
	directory.Close()
	if err := os.WriteFile(ArtifactPath(home, legacy), legacyBytes, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(home, "mesh-artifacts", "artifact-"+legacy.SHA256+".json")
	if err := save(manifest, legacy); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	file, err := OpenArtifact(home, legacy)
	if err != nil {
		t.Fatal("existing trusted artifact lost digest/stat access", err)
	}
	actual, err := io.ReadAll(file)
	file.Close()
	if err != nil || !bytes.Equal(actual, legacyBytes) {
		t.Fatal("existing trusted bytes changed", err)
	}
	if _, err := ImportCompanion(home, legacy, bytes.NewReader(legacyBytes)); err != nil {
		t.Fatal("existing exact trusted descriptor was not reused", err)
	}
	after, err := os.ReadFile(manifest)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing trusted descriptor was rewritten", err)
	}

	fresh := filepath.Join(t.TempDir(), "fresh-home")
	if _, err := ImportCompanion(fresh, legacy, bytes.NewReader(legacyBytes)); err == nil {
		t.Fatal("new legacy stream import passed vanity inspection")
	}
	if _, err := os.Lstat(ArtifactPath(fresh, legacy)); !os.IsNotExist(err) {
		t.Fatal("refused legacy stream published catalog bytes", err)
	}
}
