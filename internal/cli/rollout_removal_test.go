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
	"github.com/google/uuid"
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

func TestRolloutOnboardActiveReplacementWinsOverRemovedFence(t *testing.T) {
	m, home := onboardReviewFixture(t, true)
	root := m.binding.Root
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "old", "--path", root, "--reason", "Define original scope")
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "old", "--mode", "all", "--reason", "Original participation policy")
	store := rollout.Store{Home: home}
	in := rollout.Identification{Query: rollout.Query{CWD: root, Adapter: "codex", Session: uuid.NewString()}, Protocol: rollout.Protocol, Needs: "Synthetic coordination need"}
	d, err := store.Identify(in)
	if err != nil {
		t.Fatal(err)
	}
	if d, err = store.Acknowledge(in.Query, d.Session.Identity, d.Project.Revision); err != nil || !d.Coordinate {
		t.Fatal(d, err)
	}
	rolloutApplyCLI(t, home, "rollout", "binding", "remove", "--project", "old", "--path", root, "--reason", "Retire original boundary")
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "replacement", "--path", root, "--reason", "Explicit replacement scope")
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "replacement", "--mode", "observe", "--reason", "Replacement manual pilot")
	rolloutApplyCLI(t, home, "rollout", "pause", "--project", "replacement", "--reason", "Keep enrollment paused")
	before, err := os.ReadFile(filepath.Join(home, "rollout.json"))
	if err != nil {
		t.Fatal(err)
	}
	m = newRolloutOnboard(t.Context(), home, "Keep reviewed replacement", nil)
	runOnboardCmd(t, m, m.resolve(root))
	m.cursor = len(m.choices) - 1
	onboardKey(m, "enter")
	if m.id != "replacement" {
		t.Fatal("removed fence selected over active project", m.id)
	}
	onboardKey(m, "enter")
	if m.choices[0].value != "observe" {
		t.Fatal("replacement policy was not offered first", m.choices)
	}
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	if !m.plan.Unchanged || !m.plan.After.Paused || m.plan.After.Revision != 3 || m.plan.After.ID != "replacement" {
		t.Fatal(m.plan)
	}
	runOnboardCmd(t, m, onboardKey(m, "y"))
	if m.err != nil {
		t.Fatal(m.err)
	}
	after, err := os.ReadFile(filepath.Join(home, "rollout.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unchanged replacement rewrote ledger", err)
	}
	st, err := store.Read()
	if err != nil || len(st.Sessions) != 1 || st.Sessions[0].Project != "old" || st.Sessions[0].State != "revoked" {
		t.Fatal("revoked history changed", st, err)
	}
}
