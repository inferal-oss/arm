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

func rolloutCLI(t *testing.T, home string, args ...string) (int, []byte, string) {
	t.Helper()
	var out, errout bytes.Buffer
	code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout)
	return code, out.Bytes(), errout.String()
}

func TestRolloutStatusHumanReadinessAndJSONLifecycle(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "agent")
	var out bytes.Buffer
	a := app{home: home, ctx: t.Context(), out: &out, err: &bytes.Buffer{}, in: strings.NewReader(""), outputTTY: true, colors: true}
	if e := a.rollout([]string{"status"}); e != nil {
		t.Fatal(e)
	}
	if json.Valid(out.Bytes()) || !strings.Contains(out.String(), "Instructions:") || !strings.Contains(out.String(), "Not ready") || !strings.Contains(out.String(), "No ARM-managed instructions are recorded") || !strings.Contains(out.String(), "None configured.") || !strings.Contains(out.String(), " install`") {
		t.Fatal(out.String())
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("terminal status created state", e)
	}
	if _, e := install.Install(home, []install.Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}}, false); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	a.colors = false
	if e := a.rollout([]string{"status"}); e != nil || !strings.Contains(out.String(), "Instructions: Ready") || !strings.Contains(out.String(), "rollout project --help") {
		t.Fatal(e, out.String())
	}
	rolloutApplyCLI(t, home, "rollout", "project", "--project", "demo", "--path", root, "--reason", "Select project")
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "all", "--reason", "Explicit project enrollment")
	before, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = install.Uninstall(home, false); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	if e = a.rollout([]string{"status"}); e != nil || !strings.Contains(out.String(), "Instructions: Not ready") || !strings.Contains(out.String(), "demo · All sessions in project") || !strings.Contains(out.String(), "Saved policy and existing resource obligations are unchanged") {
		t.Fatal(e, out.String())
	}
	after, e := os.ReadFile(filepath.Join(home, "rollout.json"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("readiness or uninstall altered rollout policy", e)
	}
	out.Reset()
	if e = a.rollout([]string{"status", "--json"}); e != nil || !json.Valid(out.Bytes()) || bytes.Contains(out.Bytes(), []byte("\x1b")) {
		t.Fatal(e, out.String())
	}
	var status rolloutStatus
	if e = json.Unmarshal(out.Bytes(), &status); e != nil || status.Instructions.Ready || status.Instructions.State != "not-installed" || len(status.Projects) != 1 || status.Projects[0].Mode != "all" {
		t.Fatal(e, status)
	}
}

func TestRolloutStatusHumanRosterEscapesUntrustedText(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true}
	status := rolloutStatus{Default: "disabled", Instructions: install.InstructionReadiness{State: "ready", Ready: true, Summary: "Instructions match this build."}, Projects: []rollout.Project{{ID: "demo", Mode: "new-sessions", Paused: true, Revision: 3}}, Sessions: []rollout.Participation{{Session: rollout.Session{Project: "demo", Adapter: "codex", Session: "session-fixture", State: "invited", Needs: "Build\x1b[2J\nforged", Identity: "identity-fixture"}, Live: true, Eligible: false, Why: "Further enrollment is paused."}}, NextCommand: "arm rollout status"}
	if e := a.printRollout(status); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"New sessions only", "enrollment paused", "Invited · recent report · not coordinating", `Build\u001b[2J\nforged`, "identity-fixture"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	if strings.Contains(out.String(), "\x1b[2J") {
		t.Fatal("untrusted terminal sequence rendered", out.String())
	}
}
func rolloutApplyCLI(t *testing.T, home string, args ...string) {
	t.Helper()
	code, out, errout := rolloutCLI(t, home, append(args, "--dry-run")...)
	var plan rollout.Plan
	if code != 0 || json.Unmarshal(out, &plan) != nil || plan.Review == "" {
		t.Fatal("preview", code, string(out), errout)
	}
	code, out, errout = rolloutCLI(t, home, append(args, "--expect", plan.Review, "--yes")...)
	if code != 0 {
		t.Fatal("apply", code, string(out), errout)
	}
}
func TestRolloutOfflineHelpStatusCheckAndPreview(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	cwd := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"rollout", "status"}, {"rollout", "history"}, {"rollout", "check", "--cwd", cwd}, {"rollout", "--help"}, {"help", "rollout"}, {"rollout", "project", "--project", "demo", "--path", cwd, "--reason", "Review project", "--dry-run"}} {
		code, out, errout := rolloutCLI(t, home, args...)
		if code != 0 || len(out) == 0 || errout != "" {
			t.Fatal(args, code, string(out), errout)
		}
	}
	for name := range commandHelps {
		if strings.HasPrefix(name, "rollout ") {
			code, _, errout := rolloutCLI(t, home, append(strings.Fields(name), "--help")...)
			if code != 0 {
				t.Fatal(name, errout)
			}
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("offline command initialized state", e)
	}
}
func TestRolloutCLIProjectPolicyObservationInvitationAndAck(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	cwd := t.TempDir()
	base := []string{"rollout", "project", "--project", "demo", "--path", cwd, "--reason", "Define pilot boundary"}
	code, _, _ := rolloutCLI(t, home, base...)
	if code == 0 {
		t.Fatal("policy applied without review")
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("unapproved mutation created state", e)
	}
	rolloutApplyCLI(t, home, base...)
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "observe", "--reason", "Observe pilot sessions")
	session := uuid.NewString()
	args := []string{"--cwd", cwd, "--adapter", "generic", "--session", session}
	code, out, errout := rolloutCLI(t, home, append([]string{"rollout", "identify", "--needs", "Coordinate CPU"}, args...)...)
	var d rollout.Decision
	if code != 0 || json.Unmarshal(out, &d) != nil || d.Session == nil || d.Coordinate || d.Eligible {
		t.Fatal(code, string(out), errout)
	}
	rolloutApplyCLI(t, home, "rollout", "invite", "--project", "demo", "--identities", d.Session.Identity, "--reason", "Enroll a single pilot")
	ack := append([]string{"rollout", "ack", "--identity", d.Session.Identity, "--policy-revision", "2"}, args...)
	code, out, errout = rolloutCLI(t, home, ack...)
	if code != 0 || json.Unmarshal(out, &d) != nil || !d.Coordinate {
		t.Fatal(code, string(out), errout)
	}
	code, out, errout = rolloutCLI(t, home, append([]string{"rollout", "check", "--project", "other"}, args...)...)
	if code == 0 || len(out) != 0 {
		t.Fatal("project selector bypassed path resolution", code, string(out), errout)
	}
	code, out, errout = rolloutCLI(t, home, "rollout", "history", "--project", "demo")
	var history []rollout.Event
	if code != 0 || json.Unmarshal(out, &history) != nil || len(history) != 5 {
		t.Fatal("missing audit", code, string(out), errout)
	}
}

func TestRolloutGuidePrecedesResourceInstructions(t *testing.T) {
	code, out, errout := rolloutCLI(t, filepath.Join(t.TempDir(), "absent"), "agents", "--json")
	var guide agentGuide
	if code != 0 || json.Unmarshal(out, &guide) != nil || guide.Sections[0].ID != "rollout" {
		t.Fatal(code, string(out), errout)
	}
}
