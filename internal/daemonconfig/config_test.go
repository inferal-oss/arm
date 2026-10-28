package daemonconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func configHome(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}
func configFile(t *testing.T, home, raw string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(home, Filename), []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
}
func defaults() Settings { return Settings{Resources: model.Resources{CPU: 7}, Listen: "127.0.0.1:0"} }

func TestResolvedConfigPartialFileAndExplicitPrecedence(t *testing.T) {
	home := configHome(t)
	configFile(t, home, "schema = 1\n[daemon]\ncpu = 2.0\nmemory = '9007199254740993B'\n")
	r, e := Resolve(home, defaults(), Overrides{})
	if e != nil {
		t.Fatal(e)
	}
	if r.Settings.Resources.CPU != 2 || r.Settings.Resources.Memory != 9007199254740993 || r.Settings.Resources.Disk != 0 || r.Sources["cpu"] != "file" || r.Sources["disk"] != "default" || !r.FilePresent || len(r.FileSHA256) != 64 || len(r.SettingsSHA256) != 64 {
		t.Fatal(r)
	}
	cpu := 3.0
	auto := "auto"
	before := r.FileSHA256
	r, e = Resolve(home, defaults(), Overrides{CPU: &cpu, Memory: &auto})
	if e != nil || r.Settings.Resources.CPU != 3 || r.Settings.Resources.Memory != 0 || r.Sources["cpu"] != "explicit-startup-flag" || r.FileSHA256 != before {
		t.Fatal(r, e)
	}
	entries, e := os.ReadDir(home)
	if e != nil || len(entries) != 1 {
		t.Fatal("resolver created state", entries, e)
	}
}

func TestResolvedConfigInvalidWholeFileCannotBeMasked(t *testing.T) {
	for name, raw := range map[string]string{
		"empty": "", "version": "schema = 2", "missing-schema": "[daemon]\ncpu = 2", "unknown": "schema = 1\n[provider]\nenabled = true", "partial-invalid": "schema = 1\n[daemon]\ncpu = 0", "nan": "schema = 1\n[daemon]\ncpu = nan", "unknown-daemon": "schema = 1\n[daemon]\nheadroom = 2", "duplicate": "schema = 1\nschema = 1", "public": "schema = 1\n[daemon]\nlisten = '0.0.0.0:0'", "size": "schema = 1\n[daemon]\nmemory = '0'", "type": "schema = 1\n[daemon]\nmemory = 100", "oversize": strings.Repeat("#", MaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			home := configHome(t)
			configFile(t, home, raw)
			cpu := 4.0
			if _, e := Resolve(home, defaults(), Overrides{CPU: &cpu}); e == nil {
				t.Fatal("accepted invalid file")
			}
			entries, _ := os.ReadDir(home)
			if len(entries) != 1 {
				t.Fatal("created state")
			}
		})
	}
}

func TestResolvedConfigAbsentAndUnsafeInputsAreReadOnly(t *testing.T) {
	home := filepath.Join(configHome(t), "missing", "ancestor", "home")
	r, e := Resolve(home, defaults(), Overrides{})
	if e != nil || !reflect.DeepEqual(r.Settings, defaults()) || r.FilePresent {
		t.Fatal(r, e)
	}
	if _, e = os.Lstat(home); !os.IsNotExist(e) {
		t.Fatal("created home", e)
	}
	for _, kind := range []string{"symlink", "hardlink", "public-mode", "directory"} {
		t.Run(kind, func(t *testing.T) {
			home := configHome(t)
			p := filepath.Join(home, Filename)
			target := filepath.Join(home, "target")
			configFile(t, home, "schema=1")
			switch kind {
			case "symlink":
				os.Rename(p, target)
				os.Symlink(target, p)
			case "hardlink":
				os.Link(p, target)
			case "public-mode":
				os.Chmod(p, 0644)
			case "directory":
				os.Remove(p)
				os.Mkdir(p, 0700)
			}
			if _, e := Resolve(home, defaults(), Overrides{}); e == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}

func TestConfigCeilingsAreExactAndBounded(t *testing.T) {
	for raw, want := range map[string]int64{"auto": 0, "1.5KiB": 1536, "9223372036854775807B": 1<<63 - 1, "0.001KB": 1} {
		got, e := Ceiling(raw)
		if e != nil || got != want {
			t.Fatal(raw, got, e)
		}
	}
	for _, raw := range []string{"0", "-1", "0.1B", "9223372036854775808B", "nan", "1/2", "0x10", "1e3", "auto ", "", "."} {
		if _, e := Ceiling(raw); e == nil {
			t.Fatal("invalid ceiling", raw)
		}
	}
}
