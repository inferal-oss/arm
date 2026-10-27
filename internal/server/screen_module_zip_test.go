package server

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// The Go module proxy omits every vendor directory, including non-Go assets.
// A source-checkout embed can therefore succeed while a public go install
// silently loses required browser modules. Keep embedded assets zip-visible.
func TestScreenEmbeddedAssetsSurviveModuleZipLayout(t *testing.T) {
	if err := fs.WalkDir(browserAssets, "web", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, part := range strings.Split(name, "/") {
			if part == "vendor" {
				t.Errorf("embedded browser asset is omitted by Go module ZIP: %s", name)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	imports := regexp.MustCompile(`from\s+["'](\.\./deps/pako/[^"']+)["']`)
	for _, name := range []string{"web/novnc/core/inflator.js", "web/novnc/core/deflator.js"} {
		raw, err := browserAssets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "../vendor/") {
			t.Fatalf("importer still targets zip-omitted vendor path: %s", name)
		}
		matches := imports.FindAllSubmatch(raw, -1)
		if len(matches) == 0 {
			t.Fatalf("required pako imports absent: %s", name)
		}
		for _, match := range matches {
			target := path.Clean(path.Join(path.Dir(name), string(match[1])))
			content, err := browserAssets.ReadFile(target)
			if err != nil || len(content) == 0 {
				t.Fatalf("embedded pako import unavailable: %s -> %s: %v", name, target, err)
			}
		}
	}
	if raw, err := browserAssets.ReadFile("web/novnc/deps/pako/LICENSE"); err != nil || len(raw) == 0 {
		t.Fatal("pako license omitted from embedded distribution", err)
	}
}
