package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/rollout"
	"github.com/google/uuid"
)

func rolloutCLI(t *testing.T, home string, args ...string) (int, []byte, string) {
	t.Helper()
	var out, errout bytes.Buffer
	code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout)
	return code, out.Bytes(), errout.String()
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
