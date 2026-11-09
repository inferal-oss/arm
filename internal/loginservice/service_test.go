package loginservice

import (
	"bytes"
	"context"
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
		if err := applyService(t.Context(), s); err != nil {
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
	for _, want := range []string{`ExecStart=":/opt/ARM Tools/arm" --home "/path/\"100%%\" \\ ${USER}" serve`, "KillMode=process", "WantedBy=default.target"} {
		if !strings.Contains(string(s.definition), want+"\n") {
			t.Fatalf("missing %q in %s", want, s.definition)
		}
	}
	if err := writeDefinition(s.Path, s.definition); err != nil {
		t.Fatal(err)
	}
	if plan, err := s.Review(); err != nil || plan == nil || !plan.enableLink || plan.definition {
		t.Fatalf("partial setup: plan=%+v err=%v", plan, err)
	}
	if err := applyService(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(s.EnableLink); err != nil || target != s.Path {
		t.Fatalf("enable link target=%q err=%v", target, err)
	}
	if plan, err := s.Review(); plan != nil || err != nil {
		t.Fatalf("remaining setup=%+v err=%v", plan, err)
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
	if err := applyService(t.Context(), s); err == nil {
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
	if err := applyService(t.Context(), s); err == nil {
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
					if err := applyService(t.Context(), s); err != nil {
						t.Error(err)
					}
				})
			}
			group.Wait()
			if plan, err := s.Review(); plan != nil || err != nil {
				t.Fatalf("remaining setup=%+v err=%v", plan, err)
			}
		})
	}
}

func applyService(ctx context.Context, s *Service) error {
	plan, err := s.Review()
	if err != nil || plan == nil {
		return err
	}
	return plan.Apply(ctx)
}

func TestLegacySystemdUpgradePreservesDefinition(t *testing.T) {
	s, err := newService("linux", t.TempDir(), "/bin/arm", "/path/arm")
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(s.definition, []byte("KillMode=process"), []byte("KillMode=mixed"), 1)
	if err := writeDefinition(s.Path, legacy); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.EnableLink), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", s.Name), s.EnableLink); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Review()
	if err != nil || plan == nil || plan.PolicyOverride == "" || plan.definition || plan.enableLink {
		t.Fatalf("expected a drop-in only, got %+v %v", plan, err)
	}
	if _, err := os.Lstat(plan.PolicyOverride); !os.IsNotExist(err) {
		t.Fatalf("review created the drop-in: %v", err)
	}
	if err := plan.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(plan.PolicyOverride); err != nil || string(data) != "[Service]\nKillMode=process\n" {
		t.Fatalf("unexpected shutdown override: %q %v", data, err)
	}
	if data, err := os.ReadFile(s.Path); err != nil || !bytes.Equal(data, legacy) {
		t.Fatalf("legacy definition changed: %q %v", data, err)
	}
	if plan, err := s.Review(); plan != nil || err != nil {
		t.Fatalf("remaining setup=%+v err=%v", plan, err)
	}
}

func TestLoginSetupRefusesUnreviewedLegacyUpgrade(t *testing.T) {
	s, err := newService("linux", t.TempDir(), "/bin/arm", "/path/arm")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.Review()
	if err != nil || plan == nil {
		t.Fatalf("initial review=%+v err=%v", plan, err)
	}
	legacy := bytes.Replace(s.definition, []byte("KillMode=process"), []byte("KillMode=mixed"), 1)
	if err := writeDefinition(s.Path, legacy); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(t.Context()); err == nil {
		t.Fatal("approved a legacy upgrade after reviewing a fresh install")
	}
	if _, err := os.Lstat(s.EnableLink); !os.IsNotExist(err) {
		t.Fatalf("linked an unreviewed definition: %v", err)
	}
}

func TestLoginServiceRefusesTemporaryExecutables(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOTMPDIR", dir)
	binary := filepath.Join(dir, "arm")
	if err := os.WriteFile(binary, []byte("test executable"), 0700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "arm-shim")
	if err := os.Symlink("/bin/sh", shim); err != nil {
		t.Fatal(err)
	}
	for _, executable := range []string{binary, shim} {
		if err := checkExecutable(executable); err == nil || !strings.Contains(err.Error(), "permanent installation") {
			t.Fatalf("temporary executable accepted: %v", err)
		}
	}
	if err := checkExecutable("/bin/sh"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", "/bin")
	if err := checkExecutable("/bin/sh"); err == nil || !strings.Contains(err.Error(), "permanent installation") {
		t.Fatalf("Go cache executable accepted: %v", err)
	}
}

func TestSystemdUnitDirectoryMustBeInManagerSearchPath(t *testing.T) {
	const paths = `{"type":"as","data":["/home/Example User/.config/systemd/user","/usr/lib/systemd/user"]}`
	if err := verifyUnitDirectory([]byte(paths), "/home/Example User/.config/systemd/user"); err != nil {
		t.Fatal(err)
	}
	if err := verifyUnitDirectory([]byte(paths), "/home/Example User/other/systemd/user"); err == nil {
		t.Fatal("accepted a shell-only XDG_CONFIG_HOME")
	}
	if err := verifyUnitDirectory([]byte(`{"type":"s","data":"/usr/lib/systemd/user"}`), "/usr/lib/systemd/user"); err == nil {
		t.Fatal("accepted an invalid UnitPath reply")
	}
}
