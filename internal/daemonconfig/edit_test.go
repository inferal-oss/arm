package daemonconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEditsPreserveCommentsValidateAndGuardConcurrentChange(t *testing.T) {
	home := configHome(t)
	original := "# keep header\nschema = 1\n[daemon] # keep table\ncpu = 2.0 # keep inline\nmemory = 'auto'\n\n[[storage_pools]]\nname = 'bulk'\npath = '/mnt/bulk'\n"
	configFile(t, home, original)
	result, e := EditFile(home, defaults(), Edit{Key: "daemon.cpu", Value: "3"})
	if e != nil || !result.Written {
		t.Fatal(result, e)
	}
	if !strings.Contains(result.TOML, "cpu = 3 # keep inline") || !strings.Contains(result.TOML, "# keep header") || !strings.Contains(result.TOML, "path = '/mnt/bulk'") {
		t.Fatal(result.TOML)
	}
	before, _ := os.ReadFile(filepath.Join(home, Filename))
	if _, e = EditFile(home, defaults(), Edit{Key: "daemon.cpu", Value: "0"}); e == nil {
		t.Fatal("invalid value saved")
	}
	if _, e = EditFile(home, defaults(), Edit{Key: "daemon.cpu", Value: "2\nmemory='512GiB'"}); e == nil {
		t.Fatal("CPU scalar injected another config setting")
	}
	if _, e = EditFile(home, defaults(), Edit{Key: "daemon.cpu", Value: "4", ExpectFile: "stale"}); e == nil {
		t.Fatal("stale file guard accepted")
	}
	after, _ := os.ReadFile(filepath.Join(home, Filename))
	if string(after) != string(before) {
		t.Fatal("refused edit changed config")
	}
	r, e := EditFile(home, defaults(), Edit{Pool: "fast", Fields: map[string]string{"path": "/mnt/fast", "tier": "build", "ceiling": "100GiB"}})
	if e != nil || !strings.Contains(r.TOML, `name = "fast"`) {
		t.Fatal(r, e)
	}
	r, e = EditFile(home, defaults(), Edit{Pool: "fast", Fields: map[string]string{"tier": "scratch", "ceiling": "auto"}})
	if e != nil || !strings.Contains(r.TOML, `tier = "scratch"`) {
		t.Fatal(r, e)
	}
	current, e := Resolve(home, defaults(), Overrides{})
	if e != nil || len(current.Settings.StoragePools) != 2 || current.Settings.StoragePools[1].Ceiling != 0 {
		t.Fatal(current, e)
	}
	if _, e = EditFile(home, defaults(), Edit{Pool: "bulk", Remove: true}); e != nil {
		t.Fatal(e)
	}
	current, e = Resolve(home, defaults(), Overrides{})
	if e != nil || len(current.Settings.StoragePools) != 1 || current.Settings.StoragePools[0].Name != "fast" {
		t.Fatal(current, e)
	}
}
func TestConfigEditsCreatePrivateFileAndRefuseSymlinks(t *testing.T) {
	home := filepath.Join(configHome(t), "new")
	r, e := EditFile(home, defaults(), Edit{Key: "daemon.memory", Value: "auto"})
	if e != nil || !r.Written {
		t.Fatal(r, e)
	}
	info, e := os.Stat(filepath.Join(home, Filename))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	if _, e = EditFile(home, defaults(), Edit{Pool: "bad", Fields: map[string]string{"tier": "build"}}); e == nil {
		t.Fatal("missing pool path accepted")
	}
	target := filepath.Join(configHome(t), "outside")
	if e = os.WriteFile(target, []byte("schema=1\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(home, Filename)); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(target, filepath.Join(home, Filename)); e != nil {
		t.Fatal(e)
	}
	if _, e = EditFile(home, defaults(), Edit{Key: "daemon.cpu", Value: "2"}); e == nil {
		t.Fatal("symlink edited")
	}
	content, _ := os.ReadFile(target)
	if string(content) != "schema=1\n" {
		t.Fatal("outside target changed")
	}
}
