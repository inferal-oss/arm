package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripPreservesSubsequentUserEdits(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "arm")
	agent := filepath.Join(root, "codex")
	os.Mkdir(agent, 0700)
	path := filepath.Join(agent, "AGENTS.md")
	original := "my existing instructions\n"
	os.WriteFile(path, []byte(original), 0644)
	targets := []Target{{Adapter: "codex", Root: agent, Name: "AGENTS.md"}}
	if _, e := Install(home, targets, true); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("dry-run wrote state")
	}
	if _, e := Install(home, targets, false); e != nil {
		t.Fatal(e)
	}
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), "arm agents --json") {
		t.Fatal("installed instructions do not link to the full agent guide")
	}
	if _, e := Install(home, targets, false); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("install is not idempotent")
	}
	os.WriteFile(path, append(second, []byte("\nnew user instructions\n")...), 0644)
	report, e := Uninstall(home, false)
	if e != nil || !report.Complete {
		t.Fatalf("uninstall failed: %+v %v", report, e)
	}
	b, _ := os.ReadFile(path)
	if string(b) != original+"\nnew user instructions\n" {
		t.Fatalf("user edits were lost: %q", b)
	}
	if _, e = Uninstall(home, false); e != nil {
		t.Fatal("repeat uninstall:", e)
	}
}

func TestEditedManagedBlockIsPreserved(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "arm")
	agent := filepath.Join(root, "claude")
	target := Target{Adapter: "claude", Root: agent, Name: "CLAUDE.md"}
	if _, e := Install(home, []Target{target}, false); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(agent, target.Name)
	b, _ := os.ReadFile(path)
	edited := strings.Replace(string(b), "Start with", "My edits: start with", 1)
	os.WriteFile(path, []byte(edited), 0600)
	report, e := Uninstall(home, false)
	if e != nil || report.Complete {
		t.Fatalf("edited content wasn't preserved: %+v %v", report, e)
	}
	after, _ := os.ReadFile(path)
	if string(after) != edited {
		t.Fatal("modified instructions were destroyed")
	}
}

func TestSymlinkTargetCannotTouchSentinel(t *testing.T) {
	root := t.TempDir()
	agent := filepath.Join(root, "agent")
	os.Mkdir(agent, 0700)
	sentinel := filepath.Join(root, "outside")
	os.WriteFile(sentinel, []byte("must survive"), 0600)
	os.Symlink(sentinel, filepath.Join(agent, "AGENTS.md"))
	if _, e := Install(filepath.Join(root, "arm"), []Target{{Adapter: "codex", Root: agent, Name: "AGENTS.md"}}, false); e == nil {
		t.Fatal("accepted symlink instruction file")
	}
	b, _ := os.ReadFile(sentinel)
	if string(b) != "must survive" {
		t.Fatal("sentinel modified")
	}
}

func TestCreatedFileRemovedButHistoryRetained(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "arm")
	agent := filepath.Join(root, "agent")
	if _, e := Install(home, []Target{{Adapter: "codex", Root: agent, Name: "AGENTS.md"}}, false); e != nil {
		t.Fatal(e)
	}
	if _, e := Uninstall(home, true); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(agent, "AGENTS.md")); e != nil {
		t.Fatal("dry uninstall changed file")
	}
	if _, e := Uninstall(home, false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(agent, "AGENTS.md")); !os.IsNotExist(e) {
		t.Fatal("created instruction file remains")
	}
	if _, e := os.Stat(filepath.Join(home, "install.json")); e != nil {
		t.Fatal("recovery manifest removed")
	}
}

func TestReplacedRootIsNeverCleaned(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "arm")
	root := filepath.Join(base, "agent")
	if _, e := Install(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}, false); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if e := os.Rename(root, root+"-old"); e != nil {
		t.Fatal(e)
	}
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(root, "AGENTS.md"), b, 0600)
	if _, e := Uninstall(home, false); e == nil {
		t.Fatal("cleaned an unrelated replacement directory")
	}
	after, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if string(after) != string(b) {
		t.Fatal("replacement directory was modified")
	}
}

func TestInterruptedInstallResumesWithoutOverwritingEdits(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "arm")
	root := filepath.Join(base, "agent")
	os.Mkdir(root, 0700)
	original := []byte("original instructions\n")
	path := filepath.Join(root, "AGENTS.md")
	os.WriteFile(path, original, 0640)
	targets := []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}
	if _, e := Install(home, targets, false); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, original, 0640)
	if _, e := Install(home, targets, false); e != nil {
		t.Fatal("could not resume recorded installation:", e)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), begin) != 1 {
		t.Fatal("did not resume managed block")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("changed existing instruction permissions")
	}
}
