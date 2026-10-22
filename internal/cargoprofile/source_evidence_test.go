package cargoprofile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sourceMetadata(t *testing.T, workspace string, roots Roots) []byte {
	t.Helper()
	raw, e := json.Marshal(map[string]any{"workspace_root": workspace, "target_directory": roots.Target, "build_directory": roots.Build, "packages": []any{map[string]any{"id": "member-id", "manifest_path": filepath.Join(workspace, "member", "Cargo.toml")}}, "workspace_members": []string{"member-id"}})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestCargoSourceEvidenceIsBoundedReadOnlyAndDetectsLockBirth(t *testing.T) {
	root := fixtureRoot(t)
	workspace := filepath.Join(root, "project")
	roots := Roots{Target: filepath.Join(root, "target"), Build: filepath.Join(root, "build"), Control: filepath.Join(root, "control")}
	write(t, filepath.Join(workspace, "Cargo.toml"), "[workspace]\nmembers=['member']\n")
	write(t, filepath.Join(workspace, "member", "Cargo.toml"), "[package]\nname='member'\nversion='0.1.0'\n")
	source, e := CaptureSource(context.Background(), sourceMetadata(t, workspace, roots), roots)
	if e != nil {
		t.Fatal(e)
	}
	if len(source.Files) != 3 || len(source.Members) != 1 {
		t.Fatal(source)
	}
	if _, e = os.Stat(filepath.Join(workspace, "Cargo.lock")); !os.IsNotExist(e) {
		t.Fatal("source observer wrote a lock", e)
	}
	write(t, filepath.Join(workspace, "Cargo.lock"), "# newly generated\n")
	if e = source.Revalidate(context.Background()); e == nil {
		t.Fatal("lock birth after metadata accepted")
	}
	source, e = CaptureSource(context.Background(), sourceMetadata(t, workspace, roots), roots)
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(workspace, "member", "Cargo.toml"), "[package]\nname='changed'\nversion='0.1.0'\n")
	if e = source.Revalidate(context.Background()); e == nil {
		t.Fatal("changed manifest accepted")
	}
}
func TestCargoSourceEvidenceRejectsExternalSymlinkAndAmbiguousMembers(t *testing.T) {
	root := fixtureRoot(t)
	workspace := filepath.Join(root, "project")
	roots := Roots{Target: filepath.Join(root, "target"), Build: filepath.Join(root, "build")}
	write(t, filepath.Join(workspace, "Cargo.toml"), "[workspace]\n")
	write(t, filepath.Join(workspace, "member", "Cargo.toml"), "[package]\n")
	raw := sourceMetadata(t, workspace, roots)
	var data map[string]any
	if e := json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	for _, members := range []any{nil, []any{}, []any{"member-id", "member-id"}, []any{"unknown"}} {
		data["workspace_members"] = members
		bad, _ := json.Marshal(data)
		if _, e := CaptureSource(context.Background(), bad, roots); e == nil {
			t.Fatal("ambiguous workspace members accepted", members)
		}
	}
	data["workspace_members"] = []string{"member-id"}
	data["packages"] = []any{map[string]any{"id": "member-id", "manifest_path": filepath.Join(root, "external", "Cargo.toml")}}
	bad, _ := json.Marshal(data)
	if _, e := CaptureSource(context.Background(), bad, roots); e == nil {
		t.Fatal("external member accepted")
	}
	original := filepath.Join(workspace, "member", "Cargo.toml")
	if e := os.Rename(original, original+".retained"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("Cargo.toml.retained", original); e != nil {
		t.Fatal(e)
	}
	if _, e := CaptureSource(context.Background(), raw, roots); e == nil {
		t.Fatal("symlink source manifest accepted")
	}
}
