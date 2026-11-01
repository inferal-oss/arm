package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

func savedStatusPicker(t *testing.T) (*projectOnboardModel, string) {
	t.Helper()
	m, home := pickerReviewFixture(t, true)
	pickerRun(t, m, pickerKey(m, "y"))
	if !m.applied || m.stage != pickerStatusOffer {
		t.Fatal("missing post-save offer", m.stage, m.err)
	}
	return m, home
}

func TestOnboardStatusDefaultNoPreservesSavedPolicy(t *testing.T) {
	for _, checked := range []bool{false, true} {
		t.Run(fmt.Sprint(checked), func(t *testing.T) {
			m, home := savedStatusPicker(t)
			if !checked {
				m.selection.Checked = nil
			}
			policy, err := os.ReadFile(filepath.Join(home, "rollout.json"))
			if err != nil {
				t.Fatal(err)
			}
			m.statusSetup.preview = func(context.Context, agentstatus.OnboardingChange) (agentstatus.OnboardingPlan, error) {
				t.Fatal("default No performed setup")
				return agentstatus.OnboardingPlan{}, nil
			}
			pickerKey(m, "enter")
			want := pickerDone
			if checked {
				want = pickerOffer
			}
			if m.stage != want || m.cancelled || !strings.Contains(m.statusSetupSummary(), "skipped") {
				t.Fatal(m.stage, m.statusSetupSummary())
			}
			after, err := os.ReadFile(filepath.Join(home, "rollout.json"))
			if err != nil || !bytes.Equal(policy, after) {
				t.Fatal("status choice changed saved policy", err)
			}
			if _, err = os.Lstat(filepath.Join(home, "status-onboarding-v1")); !os.IsNotExist(err) {
				t.Fatal("skip created setup state", err)
			}
		})
	}
}

func TestRolloutStatusPagesOfflineCustomHomeReviewAndInspect(t *testing.T) {
	m, home := savedStatusPicker(t)
	project := m.statusSetup.change.Projects[0]
	var stdout, stderr bytes.Buffer
	a := app{ctx: t.Context(), home: home, out: &stdout, err: &stderr}
	args := []string{"--projects", project, "--key", "owned-offline-setup", "--adapter", "claude", "--client-project", "explicit-project"}
	if err := a.rollout([]string{"status-pages", "apply", "--projects", project, "--key", "owned-offline-setup"}); err == nil {
		t.Fatal("applied without exact review")
	}
	if err := a.rollout(append([]string{"status-pages", "plan"}, args...)); err != nil {
		t.Fatal(err)
	}
	var plan agentstatus.OnboardingPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Change.Contexts[0].ClientProject != "explicit-project" {
		t.Fatal(plan.Change)
	}
	stdout.Reset()
	if err := a.rollout(append(append([]string{"status-pages", "apply"}, args...), "--expect", plan.Review, "--yes")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.rollout([]string{"status-pages", "inspect", "--key", "owned-offline-setup"}); err != nil {
		t.Fatal(err)
	}
	var record agentstatus.OnboardingRecord
	if json.Unmarshal(stdout.Bytes(), &record) != nil || record.State != "saved" || record.Key != "owned-offline-setup" {
		t.Fatal(stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, "status-v1")); !os.IsNotExist(err) {
		t.Fatal("offline setup started status backend", err)
	}
}

func TestOnboardStatusSeparateReviewUsesActualSavedPolicy(t *testing.T) {
	m, home := savedStatusPicker(t)
	policy, err := (rollout.Store{Home: home}).Read()
	if err != nil {
		t.Fatal(err)
	}
	pickerRun(t, m, pickerKey(m, "y"))
	if m.stage != pickerStatusReview || len(m.statusSetup.plan.Scopes) != len(policy.Projects) {
		t.Fatal(m.stage, m.notice)
	}
	for i, p := range policy.Projects {
		scope := m.statusSetup.plan.Scopes[i]
		if scope.Project != p.ID || scope.PolicyRevision != p.Revision || scope.BindingsDigest == "" {
			t.Fatal("preview invented policy", scope)
		}
	}
	if _, err = os.Lstat(filepath.Join(home, "status-onboarding-v1")); !os.IsNotExist(err) {
		t.Fatal("preview wrote preferences", err)
	}
	pickerKey(m, "b")
	if m.stage != pickerStatusOffer {
		t.Fatal(m.stage)
	}
	pickerRun(t, m, pickerKey(m, "y"))
	pickerRun(t, m, pickerKey(m, "y"))
	if m.activateChecked || m.statusSetup.last() == nil || m.statusSetup.last().record.State != "saved" {
		t.Fatal("status setup changed activation or lost outcome")
	}
	projection := (agentstatus.OnboardingStore{Home: home}).ProjectSetup(t.Context(), statusPolicyReader(home))
	if len(projection.Projects) != 1 || projection.Projects[0].Provenance != "matching" {
		t.Fatal(projection)
	}
	if _, err = os.Stat(filepath.Join(home, "status-v1")); !os.IsNotExist(err) {
		t.Fatal("onboarding initialized provider/status service", err)
	}
}

func TestOnboardStatusReviewPreservesFormattingAndEscapesData(t *testing.T) {
	m, _ := savedStatusPicker(t)
	pickerRun(t, m, pickerKey(m, "y"))
	plan := m.statusSetup.plan
	plan.Change.Key = "review-key\n\x1b[2J\u202e"
	m.stage = pickerStatusPreparing
	m.updateStatusSetup(onboardStatusPreviewMsg{generation: m.generation, plan: plan})
	content := m.reviewContent
	if !strings.Contains(content, "{\n  \"version\":") || !strings.Contains(content, `"review_sha256": "`+plan.Review+`"`) {
		t.Fatal("lost multiline review or exact digest", content)
	}
	if !strings.Contains(content, `"key": "review-key\n\u001b[2J\u202e"`) || strings.ContainsAny(content, "\x1b\u202e") || strings.Contains(content, "review-key\n") {
		t.Fatal("review data can affect terminal layout", content)
	}
}

func TestOnboardStatusCancellationFencesLateCommands(t *testing.T) {
	for _, phase := range []string{"preview", "confirmation"} {
		t.Run(phase, func(t *testing.T) {
			m, _ := savedStatusPicker(t)
			cmd := pickerKey(m, "y")
			if phase == "preview" {
				generation := m.generation
				pickerKey(m, "esc")
				m.Update(onboardStatusPreviewMsg{generation: generation, plan: agentstatus.OnboardingPlan{}})
				if m.stage == pickerStatusReview {
					t.Fatal("late preview reopened confirmation")
				}
			} else {
				pickerRun(t, m, cmd)
				called := false
				m.statusSetup.apply = func(context.Context, agentstatus.OnboardingChange, string) (agentstatus.OnboardingRecord, error) {
					called = true
					return agentstatus.OnboardingRecord{}, nil
				}
				cmd = pickerKey(m, "y")
				m.cancelOperation()
				cmd()
				if called {
					t.Fatal("captured late confirmation published after cancellation")
				}
			}
			m.statusSetup.join()
			if late := m.statusSetup.task(func() tea.Msg { t.Fatal("closed task registry executed"); return nil })(); late != nil {
				t.Fatal(late)
			}
		})
	}
}

func TestOnboardStatusJoinsUndeliveredOriginalOutcome(t *testing.T) {
	for _, state := range []string{"saved", "stale", "unknown"} {
		for _, human := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/human=%t", state, human), func(t *testing.T) {
				m, home := savedStatusPicker(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.ctx = ctx
				pickerRun(t, m, pickerKey(m, "y"))
				started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				key := m.statusSetup.change.Key
				m.statusSetup.apply = func(context.Context, agentstatus.OnboardingChange, string) (agentstatus.OnboardingRecord, error) {
					close(started)
					<-release
					r := agentstatus.OnboardingRecord{Key: key, State: state}
					if state == "unknown" {
						return r, &agentstatus.OnboardingOutcomeUnknown{Key: key, Err: errors.New("owned uncertain publication")}
					}
					return r, nil
				}
				cmd := pickerKey(m, "y")
				go func() { defer close(done); cmd() }() // original result deliberately never delivered to Update
				<-started
				cancel()
				joined := make(chan error, 1)
				go func() { joined <- m.result(nil) }() // nil terminal error must still retain cancellation guidance
				close(release)
				err := <-joined
				<-done
				var detail *model.Error
				want := "status_onboarding_interrupted"
				if state == "unknown" {
					want = "status_onboarding_outcome_unknown"
				}
				if !errors.As(err, &detail) || detail.Code != want || !strings.Contains(detail.Message, key) || !strings.Contains(detail.Message, "--home") || !strings.Contains(detail.Message, home) {
					t.Fatal("original outcome lost", err)
				}
				var stdout, stderr bytes.Buffer
				a := app{ctx: ctx, home: home, out: &stdout, err: &stderr, outputTTY: human, errorTTY: human}
				if code := a.finishCommand(0, fmt.Errorf("terminal: %w", err)); code != 130 || stdout.Len() != 0 {
					t.Fatal(code, stdout.String())
				}
				if !human {
					var wire struct {
						Error *model.Error `json:"error"`
					}
					if json.Unmarshal(stderr.Bytes(), &wire) != nil || wire.Error == nil || wire.Error.Code != want {
						t.Fatal(stderr.String())
					}
				} else if !strings.Contains(stderr.String(), want) {
					t.Fatal(stderr.String())
				}
			})
		}
	}
}
