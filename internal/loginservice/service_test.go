package loginservice

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLaunchAgentPreservesArguments(t *testing.T) {
	executable := `/opt/ARM & Tools/arm`
	home := `/path/ARM <state> "100%" $USER`
	s, err := newService("darwin", t.TempDir(), executable, home)
	if err != nil {
		t.Fatal(err)
	}
	var plist struct {
		Arguments []string `xml:"dict>array>string"`
	}
	if err := xml.Unmarshal(s.definition, &plist); err != nil {
		t.Fatal(err)
	}
	if want := []string{executable, "--home", home, "serve"}; !reflect.DeepEqual(plist.Arguments, want) {
		t.Fatalf("arguments %q, want %q", plist.Arguments, want)
	}
	if !strings.Contains(string(s.definition), "<key>RunAtLoad</key>\n<true/>") {
		t.Fatal("launch agent must start at login")
	}
	if runtime.GOOS == "darwin" {
		if err := s.Enable(); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-lint", s.Path).CombinedOutput(); err != nil {
			t.Fatalf("native plist validation: %v: %s", err, out)
		}
	}
}

func TestSystemdUnitEscapesPathsAndEnablesOnDefaultTarget(t *testing.T) {
	s, err := newService("linux", t.TempDir(), `/opt/ARM Tools/arm`, `/path/"100%" \ ${USER}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`ExecStart=":/opt/ARM Tools/arm" --home "/path/\"100%%\" \\ ${USER}" serve`, "WantedBy=default.target"} {
		if !strings.Contains(string(s.definition), want+"\n") {
			t.Fatalf("missing %q in %s", want, s.definition)
		}
	}
	if err := writeDefinition(s.Path, s.definition); err != nil {
		t.Fatal(err)
	}
	if enabled, err := s.Enabled(); enabled || err != nil {
		t.Fatalf("partial setup: enabled=%v err=%v", enabled, err)
	}
	if err := s.Enable(); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(s.EnableLink); err != nil || target != filepath.Join("..", s.Name) {
		t.Fatalf("enable link target=%q err=%v", target, err)
	}
	if enabled, err := s.Enabled(); !enabled || err != nil {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
}

func TestLoginServicePreservesConflicts(t *testing.T) {
	s, err := newService("linux", t.TempDir(), "/bin/arm", "/path/arm")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.EnableLink), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", s.EnableLink); err != nil {
		t.Fatal(err)
	}
	if err := s.Enable(); err == nil {
		t.Fatal("accepted a conflicting enable link")
	}
	if _, err := os.Lstat(s.Path); !os.IsNotExist(err) {
		t.Fatalf("wrote a definition despite the link conflict: %v", err)
	}
	if err := os.Remove(s.EnableLink); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path, []byte("operator content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Enable(); err == nil {
		t.Fatal("accepted a conflicting unit")
	}
	if data, err := os.ReadFile(s.Path); err != nil || string(data) != "operator content" {
		t.Fatalf("existing definition changed: %q %v", data, err)
	}
}

func TestConcurrentLoginServiceEnable(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			s, err := newService(platform, filepath.Join(t.TempDir(), "services"), "/bin/arm", "/path/arm")
			if err != nil {
				t.Fatal(err)
			}
			var group sync.WaitGroup
			for range 8 {
				group.Go(func() {
					if err := s.Enable(); err != nil {
						t.Error(err)
					}
				})
			}
			group.Wait()
			if enabled, err := s.Enabled(); !enabled || err != nil {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}
