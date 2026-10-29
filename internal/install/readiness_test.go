package install

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInstructionReadinessInstallUninstallAndNoRecordedTargets(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "agent")
	before := InspectInstructions(home)
	if before.Ready || before.State != "not-installed" {
		t.Fatal(before)
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("readiness initialized state", e)
	}
	targets := []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}
	if _, e := Install(home, targets, false); e != nil {
		t.Fatal(e)
	}
	after := InspectInstructions(home)
	if !after.Ready || after.State != "ready" || len(after.Targets) != 1 || after.Targets[0].State != "ready" {
		t.Fatal(after)
	}
	if _, e := Uninstall(home, false); e != nil {
		t.Fatal(e)
	}
	after = InspectInstructions(home)
	if after.Ready || after.State != "not-installed" {
		t.Fatal(after)
	}
}

func TestInstructionReadinessReportsMissingModifiedOldAndPendingTargets(t *testing.T) {
	for _, change := range []string{"missing-file", "missing-block", "modified", "outdated", "pending", "root", "user-text", "fifo"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "state")
			root := filepath.Join(base, "agent")
			if _, e := Install(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}, false); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, "AGENTS.md")
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			manifest, e := readManifest(home)
			if e != nil {
				t.Fatal(e)
			}
			want := "unknown"
			switch change {
			case "missing-file":
				e = os.Remove(path)
				want = "missing"
			case "missing-block":
				e = os.WriteFile(path, []byte("User instructions only.\n"), 0600)
				want = "missing"
			case "modified":
				e = os.WriteFile(path, []byte(strings.Replace(string(data), "Installation alone", "Edited policy", 1)), 0600)
				want = "modified"
			case "outdated":
				manifest.Entries[0].Block = strings.Replace(manifest.Entries[0].Block, "Installation alone", "Earlier policy", 1)
				e = os.WriteFile(path, []byte(manifest.Entries[0].Block), 0600)
				if e == nil {
					e = save(home, manifest)
				}
				want = "outdated"
			case "pending":
				manifest.Entries[0].PendingBlock = manifest.Entries[0].Block
				e = save(home, manifest)
				want = "pending"
			case "root":
				e = os.Rename(root, root+"-old")
				if e == nil {
					e = os.Mkdir(root, 0700)
				}
			case "user-text":
				e = os.WriteFile(path, append(data, []byte("\nUser addition.\n")...), 0600)
				want = "ready"
			case "fifo":
				e = os.Remove(path)
				if e == nil {
					e = syscall.Mkfifo(path, 0600)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			state := InspectInstructions(home)
			if len(state.Targets) != 1 || state.Targets[0].State != want || state.Ready != (want == "ready") {
				t.Fatal(state)
			}
		})
	}
}

func TestInstructionReadinessPartialAndInvalidRecordsRemainReadOnly(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	codex := filepath.Join(base, "codex")
	claude := filepath.Join(base, "claude")
	if _, e := Install(home, []Target{{Adapter: "codex", Root: codex, Name: "AGENTS.md"}, {Adapter: "claude", Root: claude, Name: "CLAUDE.md"}}, false); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(claude, "CLAUDE.md")); e != nil {
		t.Fatal(e)
	}
	state := InspectInstructions(home)
	if state.Ready || state.State != "partial" || state.Targets[0].State != "ready" || state.Targets[1].State != "missing" {
		t.Fatal(state)
	}
	path := filepath.Join(home, "install.json")
	for _, data := range []string{`{"version":9}`, `{"entries":[]}`, `{"version":1,"entries":[],"future_policy":true}`, `invalid`} {
		if e := os.WriteFile(path, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		state = InspectInstructions(home)
		if state.Ready || state.State != "unknown" {
			t.Fatal(state)
		}
		after, e := os.ReadFile(path)
		if e != nil || string(after) != data {
			t.Fatal("inspection rewrote invalid manifest", e)
		}
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(base, "outside")
	if e := os.WriteFile(outside, []byte(`{"version":1,"entries":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, path); e != nil {
		t.Fatal(e)
	}
	if state = InspectInstructions(home); state.State != "unknown" {
		t.Fatal("followed manifest symlink", state)
	}
}
