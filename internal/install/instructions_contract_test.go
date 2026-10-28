package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/rollout"
)

func TestCurrentInstructionsRoundTripBothAdaptersWithBoundState(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state with ' quote")
	targets := []Target{{Adapter: "codex", Root: filepath.Join(base, "codex"), Name: "AGENTS.md"}, {Adapter: "claude", Root: filepath.Join(base, "claude"), Name: "CLAUDE.md"}}
	original := "User instructions stay authoritative.\n"
	for _, target := range targets {
		if err := os.Mkdir(target.Root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target.Root, target.Name), []byte(original), 0640); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := Install(home, targets, true)
	if err != nil || !preview.Complete || len(preview.Actions) != len(targets) {
		t.Fatal(preview, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("preview created state", err)
	}
	if _, err = Install(home, targets, false); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		path := filepath.Join(target.Root, target.Name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range []string{
			"arm rollout check", "arm rollout identify", "arm rollout ack", "fully disabled",
			"--client-project CLIENT_PROJECT",
			"Installation alone does not authorize adoption", "existing session's original start",
			"arm agents --json", "arm agents card --list --json", "arm agents card rust-build --json",
			"arm run --quiet", "arm proxy --quiet --agent CONTACT", "arm runs --json",
			"arm log -f RUN_ID", "arm log RUN_ID --offset BYTE_OFFSET", "does not bypass",
			"still waits and preserves", "Lost reporting leaves execution uncertain",
			"current OS headroom", "not kernel hard limits", "keep the authority/key",
			"does not release the source", "shorter deadline, never an extension",
			"remote mesh pool bundles are refused", "reverse permissions default to none",
			"it is not a sandbox", "arm mesh message-lookup HOST_ID --key KEY",
			"arm agent publish plan", "arm agent publish apply", "arm agent publish withdraw",
			"opaque endpoint ID", "native workspace delivery is refused", "routed_origin",
			"--expect REVIEW_SHA256", "--generation GENERATION_ID",
			"Direct-peer messages do not transit other peers", "arm workspaces list --json", "--after CURSOR",
			"Reading or watching does not acknowledge", "never as local operator policy",
			shellQuote(executable) + " --home " + shellQuote(home),
		} {
			if !strings.Contains(string(data), fragment) {
				t.Errorf("%s lacks %q", target.Adapter, fragment)
			}
		}
		if strings.Contains(string(data), "target/release/") || strings.Contains(string(data), "will extend to multiple hosts later") {
			t.Fatal("instructions embed Rust-specific export policy or obsolete remote promise")
		}
		if err := os.WriteFile(path, append(data, []byte("\nLater user edit.\n")...), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Install(home, targets, false); err != nil {
		t.Fatal("repeat install with surrounding user edits", err)
	}
	report, err := Uninstall(home, false)
	if err != nil || !report.Complete {
		t.Fatal(report, err)
	}
	for _, target := range targets {
		path := filepath.Join(target.Root, target.Name)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original+"\nLater user edit.\n" {
			t.Fatal("uninstall changed user instructions", target.Adapter, string(data), err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0640 {
			t.Fatal("instruction mode changed", target.Adapter, info, err)
		}
	}
}

func TestInstallAndUninstallPreserveExplicitRolloutPolicy(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "instructions")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	store := rollout.Store{Home: home}
	for _, change := range []rollout.Change{
		{Action: "project", Project: "demo", Binding: &rollout.Binding{Kind: "directory", Root: root}, Reason: "Select project"},
		{Action: "policy", Project: "demo", Mode: "all", Reason: "Explicitly include this project"},
	} {
		plan, e := store.Preview(change)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.Apply(change, plan.Review); e != nil {
			t.Fatal(e)
		}
	}
	before, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	targets := []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}
	for _, dry := range []bool{true, false, false} {
		report, e := Install(home, targets, dry)
		if e != nil || !strings.Contains(report.Adoption, "disabled") || !strings.Contains(report.Adoption, "new-sessions") {
			t.Fatal(report, e)
		}
		after, e := os.ReadFile(filepath.Join(home, "rollout.json"))
		if e != nil || string(after) != string(before) {
			t.Fatal("installation changed explicit rollout policy", e)
		}
	}
	if _, e = Uninstall(home, false); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil || string(after) != string(before) {
		t.Fatal("uninstall discarded rollout policy", e)
	}
}
