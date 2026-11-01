package agentsessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCodexAppServerDiscoveryIsExplicit(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "codex")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec "+quoted+" -test.run '^TestNativeMetadataHelper$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARM_NATIVE_METADATA_HELPER", "1")
	id := uuid.NewString()
	testJSON(t, filepath.Join(home, "threads-fixture.json"), map[string]any{"data": []any{map[string]any{"id": id, "cwd": cwd, "createdAt": 1760000000, "name": "Display-only title", "preview": "PRIVATE_CONVERSATION"}}, "nextCursor": nil})
	for _, include := range []bool{false, true} {
		if include {
			t.Setenv("ARM_NATIVE_METADATA_TITLES", "1")
		} else {
			t.Setenv("ARM_NATIVE_METADATA_TITLES", "")
		}
		out := (Codex{Home: home, Binary: script, Roots: []string{cwd}, IncludeAppServer: include}).Discover(t.Context())
		if len(out.Sessions) != 1 || len(out.Warnings) != 0 || out.Sessions[0].ID != id {
			t.Fatal(include, out)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), "Display-only") || strings.Contains(string(encoded), "PRIVATE_") {
			t.Fatal(string(encoded))
		}
	}
	t.Setenv("ARM_NATIVE_METADATA_TITLES", "1")
	titles := (Codex{Home: home, Binary: script}).DiscoverTitles(t.Context(), []string{cwd})
	if len(titles.Titles) != 1 || titles.Titles[0].Name != "Display-only title" || len(titles.Warnings) != 0 {
		t.Fatal(titles)
	}
}
