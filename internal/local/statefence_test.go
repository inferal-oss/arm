package local

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateFenceNeverCreatesOrParsesCompletionAsPermission(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "missing")
	if err := CheckStateFence(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("read-only fence check created home")
	}
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	if err := CheckStateFence(base); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Fatal("absence check created state")
	}
	for _, body := range []string{`{"version":1,"phase":"completed"}`, `{broken`, ``} {
		if err := os.WriteFile(filepath.Join(base, ResetFenceName), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var blocked *StateFenceError
		if err := CheckStateFence(base); !errors.As(err, &blocked) {
			t.Fatalf("marker did not fence: %v", err)
		}
		if err := Prepare(base); !errors.As(err, &blocked) {
			t.Fatalf("Prepare bypassed fence: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(base, ResetFenceName))
		if string(data) != body {
			t.Fatal("fence changed receipt")
		}
		if _, err := os.Stat(filepath.Join(base, "state.db")); !os.IsNotExist(err) {
			t.Fatal("fence created database")
		}
	}
}

func TestStateFenceRefusesUnsafeMarkersAndNamespace(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "public", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			os.Chmod(home, 0700)
			path := filepath.Join(home, ResetFenceName)
			switch kind {
			case "symlink":
				os.Symlink(filepath.Join(home, "absent"), path)
			case "hardlink":
				os.WriteFile(filepath.Join(home, "other"), nil, 0600)
				os.Link(filepath.Join(home, "other"), path)
			case "directory":
				os.Mkdir(path, 0700)
			case "public":
				os.WriteFile(path, nil, 0644)
			case "oversize":
				os.WriteFile(path, make([]byte, (64<<10)+1), 0600)
			}
			var blocked *StateFenceError
			if err := CheckStateFence(home); !errors.As(err, &blocked) {
				t.Fatalf("unsafe marker allowed: %v", err)
			}
		})
	}
	home := t.TempDir()
	os.Chmod(home, 0755)
	if err := CheckStateFence(home); err == nil {
		t.Fatal("public home accepted")
	}
}
