package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			"arm agents --json", "arm agents card --list --json", "arm agents card rust-build --json",
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
