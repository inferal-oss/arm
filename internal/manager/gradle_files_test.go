package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func gradleTestDirectory(t *testing.T) (*os.File, string) {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f, path
}

func TestGradlePrivateEvidence(t *testing.T) {
	dir, path := gradleTestDirectory(t)
	data := []byte(`{"lease":"known"}`)
	if err := gradleWriteFile(dir, "receipt.json", data); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var result struct {
			Lease string `json:"lease"`
		}
		if err := gradleReadJSON(dir, "receipt.json", &result); err != nil || result.Lease != "known" {
			t.Fatalf("stable receipt: %+v, %v", result, err)
		}
	}
	if err := gradleWriteFile(dir, "receipt.json", []byte("overwrite")); err == nil {
		t.Fatal("replaced existing evidence")
	}
	actual, err := os.ReadFile(filepath.Join(path, "receipt.json"))
	if err != nil || string(actual) != string(data) {
		t.Fatalf("changed evidence: %q, %v", actual, err)
	}
	for _, name := range []string{"../escape", ".", "..", "nested/file"} {
		if err := gradleWriteFile(dir, name, data); err == nil {
			t.Fatalf("accepted write %q", name)
		}
		var result any
		if err := gradleReadJSON(dir, name, &result); err == nil {
			t.Fatalf("accepted read %q", name)
		}
	}
}

func TestGradleEvidenceRefusesUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "public", "oversize", "trailing", "unknown", "empty"} {
		t.Run(kind, func(t *testing.T) {
			dir, path := gradleTestDirectory(t)
			target := filepath.Join(path, "receipt.json")
			var err error
			switch kind {
			case "symlink":
				err = os.WriteFile(filepath.Join(path, "target"), []byte(`{"lease":"x"}`), 0600)
				if err == nil {
					err = os.Symlink("target", target)
				}
			case "hardlink":
				err = os.WriteFile(filepath.Join(path, "target"), []byte(`{"lease":"x"}`), 0600)
				if err == nil {
					err = os.Link(filepath.Join(path, "target"), target)
				}
			case "fifo":
				err = unix.Mkfifo(target, 0600)
			case "directory":
				err = os.Mkdir(target, 0700)
			default:
				data, mode := `{"lease":"x"}`, os.FileMode(0600)
				switch kind {
				case "public":
					mode = 0644
				case "oversize":
					data = strings.Repeat("x", 64<<10+1)
				case "trailing":
					data += `{}`
				case "unknown":
					data = `{"unexpected":true}`
				case "empty":
					data = ""
				}
				err = os.WriteFile(target, []byte(data), mode)
			}
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Lease string `json:"lease"`
			}
			if err := gradleReadJSON(dir, "receipt.json", &result); err == nil {
				t.Fatalf("accepted %s evidence", kind)
			}
		})
	}
}

func TestGradlePrivateDirectoryIdentity(t *testing.T) {
	dir, path := gradleTestDirectory(t)
	child, original, err := gradleChildDirectory(dir, "control", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	child.Close()
	if err := os.Rename(filepath.Join(path, "control"), filepath.Join(path, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "control"), 0700); err != nil {
		t.Fatal(err)
	}
	if child, _, err := gradleChildDirectory(dir, "control", &original, false); err == nil {
		child.Close()
		t.Fatal("adopted replacement directory")
	}
	if err := os.Remove(filepath.Join(path, "control")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("old", filepath.Join(path, "control")); err != nil {
		t.Fatal(err)
	}
	if child, _, err := gradleChildDirectory(dir, "control", &original, false); err == nil {
		child.Close()
		t.Fatal("followed directory symlink")
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := gradleWriteFile(dir, "unsafe", json.RawMessage(`{}`)); err == nil {
		t.Fatal("wrote into public directory")
	}
}
