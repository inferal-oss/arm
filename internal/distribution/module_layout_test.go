package distribution

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Versioned go install ignores local replacements and omits nested modules from
// its source zip. Keep the audited fork inside the installable ARM module and
// the carrier snapshot, including its internal packages and license/provenance.
func TestInstallableModuleIncludesAuditedFork(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/inferal-oss/arm"
	if !strings.HasPrefix(string(mod), "module "+module+"\n") {
		t.Fatal("unexpected canonical module")
	}
	for _, line := range strings.Split(string(mod), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "replace ") {
			t.Fatal("versioned go install cannot use module replacements")
		}
	}
	fork := filepath.Join(root, "third_party/rsyncgo")
	err = filepath.WalkDir(fork, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "go.mod" || d.Name() == "go.work" {
			t.Errorf("active nested module/workspace would escape distribution: %s", path)
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		source, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range source.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(name, "github.com/gokrazy/rsync") || strings.HasPrefix(name, "github.com/yrashk/arm") {
				t.Errorf("fork escapes canonical module: %s imports %s", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "source")
	if err := copySource(t.Context(), root, target); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LICENSE", "ARM-CHANGES.md", "UPSTREAM-go.mod.txt", "UPSTREAM-go.sum.txt", "rsyncstream/stream.go", "internal/sender/sender.go", "internal/receiver/receiver.go"} {
		rel := filepath.Join("third_party/rsyncgo", name)
		original, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := os.ReadFile(filepath.Join(target, rel))
		if err != nil || string(snapshot) != string(original) {
			t.Fatalf("carrier omitted/changed audited fork input %s: %v", rel, err)
		}
	}
}
