package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/install"
	"getarm.org/internal/rollout"
)

func TestRolloutRemovalCLIReviewAndRetainedHistory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	root := t.TempDir()
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "demo", "--path", root, "--reason", "Define removable project")
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	args := []string{"rollout", "binding", "remove", "--project", "demo", "--path", root, "--reason", "Remove missing boundary"}
	if code, _, _ := rolloutCLI(t, home, args...); code == 0 {
		t.Fatal("unreviewed removal applied")
	}
	rolloutApplyCLI(t, home, args...)
	rolloutApplyCLI(t, home, "rollout", "project", "remove", "--project", "demo", "--reason", "Retire project")
	for _, include := range []bool{false, true} {
		args = []string{"rollout", "status", "--json"}
		if include {
			args = append(args, "--include-removed")
		}
		code, out, stderr := rolloutCLI(t, home, args...)
		var status rolloutStatus
		if code != 0 || json.Unmarshal(out, &status) != nil || len(status.Projects) != map[bool]int{false: 0, true: 1}[include] {
			t.Fatal(code, string(out), stderr)
		}
		if include && (!status.Projects[0].Removed || status.Boundaries[0].State != "removed") {
			t.Fatal(status)
		}
	}
	code, out, stderr := rolloutCLI(t, home, "rollout", "history", "--project", "demo")
	var history []rollout.Event
	if code != 0 || json.Unmarshal(out, &history) != nil || len(history) != 3 || history[1].Action != "remove-binding" || history[2].Action != "remove-project" {
		t.Fatal(code, string(out), stderr)
	}
	for _, path := range []string{filepath.Join(home, "state.db"), filepath.Join(home, "endpoint.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("removal created resource backend state", path, err)
		}
	}
}

func TestRolloutStatusSeparatesReadyInstructionsFromStaleBoundaries(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	root := t.TempDir()
	if _, err := install.Install(home, []install.Target{{Adapter: "codex", Root: t.TempDir(), Name: "AGENTS.md"}}, false); err != nil {
		t.Fatal(err)
	}
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "demo", "--path", root, "--reason", "Define scope")
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "all", "--reason", "Enable selected scope")
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(home, "rollout.json"))
	code, out, stderr := rolloutCLI(t, home, "rollout", "status", "--json")
	var status rolloutStatus
	if code != 0 || json.Unmarshal(out, &status) != nil || !status.Instructions.Ready || len(status.Boundaries) != 1 || status.Boundaries[0].State != "stale" {
		t.Fatal(code, string(out), stderr)
	}
	var human bytes.Buffer
	a := app{home: home, ctx: t.Context(), out: &human, err: &bytes.Buffer{}, outputTTY: true}
	if err := a.rollout([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Instructions: Ready", "BLOCKED", "STALE", root, "missing or inaccessible", "explicitly rebind"} {
		if !strings.Contains(human.String(), want) {
			t.Fatal(want, human.String())
		}
	}
	if strings.Contains(human.String(), "enrollment open") {
		t.Fatal("stale project advertised as open", human.String())
	}
	after, _ := os.ReadFile(filepath.Join(home, "rollout.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("status mutated policy")
	}
}

func TestRolloutOnboardSkipsRemovedProjectIdentity(t *testing.T) {
	m, home := onboardReviewFixture(t, true)
	runOnboardCmd(t, m, onboardKey(m, "y"))
	rolloutApplyCLI(t, home, "rollout", "project", "remove", "--project", "demo", "--reason", "Retire onboarding fixture")
	m = newRolloutOnboard(t.Context(), home, "Select fresh identity", nil)
	runOnboardCmd(t, m, m.resolve(mustProjectRoot(t, home)))
	m.cursor = len(m.choices) - 1
	onboardKey(m, "enter")
	if m.id != "demo-2" {
		t.Fatal("removed identity suggested for reuse", m.id)
	}
}

func mustProjectRoot(t *testing.T, home string) string {
	t.Helper()
	st, err := (rollout.Store{Home: home}).Read()
	if err != nil {
		t.Fatal(err)
	}
	return st.Projects[0].Bindings[0].Root
}
