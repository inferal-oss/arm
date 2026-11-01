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
	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

func pickerKey(m *projectOnboardModel, key string) tea.Cmd {
	code := rune(0)
	ctrl := false
	switch key {
	case "enter":
		code = tea.KeyEnter
	case "esc":
		code = tea.KeyEscape
	case "up":
		code = tea.KeyUp
	case "down":
		code = tea.KeyDown
	case "left":
		code = tea.KeyLeft
	case "right":
		code = tea.KeyRight
	case "tab":
		code = tea.KeyTab
	case "pgup":
		code = tea.KeyPgUp
	case "pgdown":
		code = tea.KeyPgDown
	case "space":
		code = ' '
	case "ctrl+space":
		code = ' '
		ctrl = true
	case "ctrl+c":
		code = 'c'
		ctrl = true
	case "ctrl+n":
		code = 'n'
		ctrl = true
	case "ctrl+r":
		code = 'r'
		ctrl = true
	case "ctrl+o":
		code = 'o'
		ctrl = true
	default:
		code = []rune(key)[0]
	}
	message := tea.KeyPressMsg{Code: code}
	if ctrl {
		message.Mod = tea.ModCtrl
	}
	_, cmd := m.Update(message)
	return cmd
}
func pickerType(t *testing.T, m *projectOnboardModel, value string) {
	t.Helper()
	for _, r := range value {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		if m.stage != pickerProjects {
			t.Fatal("typing left tree", m.stage)
		}
	}
}
func pickerRun(t *testing.T, m *projectOnboardModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected owned operation")
	}
	m.Update(cmd())
}
func pickerFixture(t *testing.T, ready bool) (*projectOnboardModel, string) {
	t.Helper()
	base := t.TempDir()
	root := pickerTestDirectory(t, filepath.Join(base, "project"))
	client := pickerTestDirectory(t, filepath.Join(base, "client"))
	home := filepath.Join(base, "custom state")
	if ready {
		if _, err := install.Install(home, []install.Target{{Adapter: "codex", Root: client.Path, Name: "AGENTS.md"}}, false); err != nil {
			t.Fatal(err)
		}
	}
	group := agentprojects.Group{Key: "project-fixture", Kind: "directory", Name: "Example", Directory: root.Path, Directories: []string{root.Path}, Memberships: []agentprojects.Membership{{Member: root}}}
	for i, name := range []string{"Alpha build", "Jade task", "Keep queue"} {
		group.Sessions = append(group.Sessions, agentsessions.SavedSession{Ref: agentsessions.SavedSessionRef{Adapter: "codex", Home: client.Path, ID: fmt.Sprintf("%08x-0000-4000-8000-000000000000", i+1)}, Directory: root.Path, Name: name})
	}
	m := newProjectOnboard(t.Context(), home, "Interactive checkbox fixture", nil, nil)
	m.probeFactory = func(*gitmeta.Budget) (pickerGitProbe, error) {
		t.Fatal("directory fixture attempted a native probe")
		return nil, gitmeta.ErrUnsupported
	}
	m.Update(pickerLoadedMsg{generation: 0, hierarchy: agentprojects.Hierarchy{Version: 1, Groups: []agentprojects.Group{group}, Coverage: []agentprojects.HierarchyCoverage{{Adapter: "codex", Warnings: []string{"Owned fixture coverage only"}}}}})
	return m, home
}
func pickerReviewFixture(t *testing.T, ready bool) (*projectOnboardModel, string) {
	t.Helper()
	m, home := pickerFixture(t, ready)
	pickerKey(m, "ctrl+space")
	pickerKey(m, "enter")
	if m.stage != pickerSettings {
		t.Fatal(m.stage, m.notice)
	}
	pickerRun(t, m, pickerKey(m, "enter"))
	if m.stage != pickerReview {
		t.Fatal(m.stage, m.notice)
	}
	return m, home
}

func TestProjectPickerLatestChildTypeaheadAndCheckboxFocus(t *testing.T) {
	m, _ := pickerFixture(t, false)
	pickerType(t, m, "Jade task")
	if m.search.Value() != "Jade task" || len(m.rows) != 2 || m.rows[m.cursor].child != 1 {
		t.Fatal(m.search.Value(), m.rows, m.cursor)
	}
	pickerKey(m, "enter")
	if m.stage != pickerSettings || len(m.tree.recipients) != 1 || !m.tree.projects["project-fixture"] {
		t.Fatal(m.stage, m.tree)
	}
	for _, recipient := range m.tree.recipients {
		if recipient.session.Name != "Jade task" {
			t.Fatal(recipient)
		}
	}
	if strings.Contains(m.View().Content, "No onboarding policy") {
		t.Fatal(m.View().Content)
	}
}

func TestProjectPickerInitialSpaceSelectsParentAndTypingFocusesSearch(t *testing.T) {
	m, _ := pickerFixture(t, false)
	pickerKey(m, "space")
	if m.search.Value() != "" || len(m.tree.recipients) != 3 || !m.tree.projects["project-fixture"] {
		t.Fatal(m.search.Value(), m.tree)
	}
	pickerType(t, m, "Jade task")
	if !m.searchFocus || m.rows[m.cursor].child != 1 || len(m.tree.recipients) != 3 {
		t.Fatal(m.searchFocus, m.rows, m.tree)
	}
}

func TestProjectPickerParentHiddenChildrenAndNoMatchFooter(t *testing.T) {
	m, _ := pickerFixture(t, false)
	pickerType(t, m, "queue")
	pickerKey(m, "ctrl+space")
	if len(m.tree.recipients) != 1 {
		t.Fatal(m.tree.recipients)
	}
	pickerKey(m, "esc")
	pickerKey(m, "tab")
	pickerKey(m, "space")
	if len(m.tree.recipients) != 3 {
		t.Fatal("mixed parent did not select hidden children", m.tree.recipients)
	}
	pickerKey(m, "left")
	if len(m.rows) != 2 {
		t.Fatal("collapse", m.rows)
	}
	pickerKey(m, "space")
	if m.tree.hasSelection() {
		t.Fatal("fully checked parent did not clear", m.tree)
	}
	pickerKey(m, "space")
	pickerKey(m, "right")
	pickerKey(m, "down")
	pickerKey(m, "down")
	pickerKey(m, "space")
	if len(m.tree.recipients) != 2 || !m.tree.projects["project-fixture"] {
		t.Fatal(m.tree)
	}
	pickerType(t, m, "no such match")
	view := m.View().Content
	if len(m.rows) != 0 || !strings.Contains(view, "Enter: review 1 projects · 2 chats") || !strings.Contains(view, "No matches") {
		t.Fatal(view, m.rows)
	}
	pickerKey(m, "enter")
	if m.stage != pickerSettings || len(m.tree.recipients) != 2 {
		t.Fatal(m.stage, m.tree)
	}
}

func TestProjectPickerPrintableKeysPasteAndEscape(t *testing.T) {
	m, _ := pickerFixture(t, false)
	for _, value := range []string{"q", "j", "k"} {
		pickerType(t, m, value)
		if m.cancelled {
			t.Fatal(value)
		}
		pickerKey(m, "esc")
	}
	m.Update(tea.PasteMsg{Content: "Jade task"})
	if m.search.Value() != "Jade task" || m.rows[m.cursor].child != 1 {
		t.Fatal(m.search.Value(), m.rows)
	}
	pickerKey(m, "left")
	if !m.tree.expanded["project-fixture"] {
		t.Fatal("search cursor folded project")
	}
	pickerKey(m, "esc")
	if m.stage != pickerProjects || m.search.Value() != "" {
		t.Fatal(m.stage, m.search.Value())
	}
	m.Update(tea.PasteMsg{Content: strings.Repeat("界", 100)})
	if len([]rune(m.search.Value())) != 64 {
		t.Fatal(len([]rune(m.search.Value())))
	}
	pickerKey(m, "esc")
	pickerKey(m, "esc")
	if !m.cancelled || m.stage != pickerDone {
		t.Fatal(m.stage, m.cancelled)
	}
}

func TestProjectPickerDefaultNoAndAtomicPolicySave(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprint(apply), func(t *testing.T) {
			m, home := pickerReviewFixture(t, true)
			if _, err := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
				t.Fatal("preview saved policy", err)
			}
			if !strings.Contains(m.View().Content, "Save policy? [y/N]") {
				t.Fatal(m.View().Content)
			}
			if !apply {
				pickerKey(m, "enter")
				if !m.cancelled || m.stage != pickerDone {
					t.Fatal(m.stage)
				}
				if _, err := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
					t.Fatal(err)
				}
				return
			}
			pickerRun(t, m, pickerKey(m, "y"))
			if m.stage != pickerOffer || !m.applied {
				t.Fatal(m.stage, m.notice, m.err)
			}
			state, err := (rollout.Store{Home: home}).Read()
			if err != nil || len(state.Projects) != 1 || len(state.Sessions) != 0 || state.Projects[0].Mode != "new-sessions" {
				t.Fatal(state, err)
			}
			pickerKey(m, "enter")
			if m.stage != pickerDone || m.cancelled {
				t.Fatal(m.stage, m.cancelled)
			}
		})
	}
}

func TestProjectPickerExactCopyOnlyActivationOffer(t *testing.T) {
	m, home := pickerFixture(t, true)
	pickerKey(m, "ctrl+space")
	unchecked := m.tree.groups[0].Sessions[1]
	m.tree.toggle(onboardTreeRow{group: 0, child: 1})
	pickerKey(m, "enter")
	pickerRun(t, m, pickerKey(m, "enter"))
	m.discover = func(context.Context) (agentprojects.Catalog, agentprojects.Hierarchy, error) {
		t.Fatal("policy/offer started native rediscovery")
		return agentprojects.Catalog{}, agentprojects.Hierarchy{}, nil
	}
	pickerRun(t, m, pickerKey(m, "y"))
	if m.stage != pickerOffer || len(m.selection.Checked) != 2 {
		t.Fatal(m.stage, m.selection)
	}
	pickerKey(m, "y")
	if m.stage != pickerActivation {
		t.Fatal(m.stage)
	}
	for index := range m.selection.Checked {
		guide := m.activationGuide(index)
		if strings.Contains(guide, unchecked.Ref.ID) || !strings.Contains(guide, "(UNVERIFIED)") || !strings.Contains(guide, "--session YOUR_ORIGINAL_SESSION_UUID") || !strings.Contains(guide, "Original conversation start is unknown") || !strings.Contains(guide, "--home") {
			t.Fatal(guide)
		}
		if strings.Contains(guide, "--session "+m.selection.Checked[index].Session.Ref.ID) {
			t.Fatal("saved reference became an ARM identity", guide)
		}
	}
	state, err := (rollout.Store{Home: home}).Read()
	if err != nil || len(state.Sessions) != 0 {
		t.Fatal(state, err)
	}
	if !strings.Contains(m.policyReview(), "[ ] Jade task") || !strings.Contains(m.policyReview(), "All project bindings after") {
		t.Fatal(m.policyReview())
	}
}

func TestProjectPickerReadinessDisabledAndUnchanged(t *testing.T) {
	m, home := pickerReviewFixture(t, false)
	if cmd := pickerKey(m, "y"); cmd != nil || m.stage != pickerSettings {
		t.Fatal("enabled without managed instructions", m.stage)
	}
	m.settings[0].Mode = "disabled"
	pickerRun(t, m, pickerKey(m, "enter"))
	pickerRun(t, m, pickerKey(m, "y"))
	state, err := (rollout.Store{Home: home}).Read()
	if err != nil || state.Projects[0].Mode != "disabled" || install.InspectInstructions(home).Ready {
		t.Fatal(state, err)
	}
	ready, home := pickerReviewFixture(t, true)
	pickerRun(t, ready, pickerKey(ready, "y"))
	before, err := os.ReadFile(filepath.Join(home, "rollout.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range install.InspectInstructions(home).Targets {
		if target.State == "ready" {
			if err = os.Remove(target.Path); err != nil {
				t.Fatal(err)
			}
		}
	}
	ready.applied = false
	ready.stage = pickerProjects
	ready.beginSettings()
	pickerRun(t, ready, pickerKey(ready, "enter"))
	if !ready.plan.Unchanged {
		t.Fatal(ready.plan)
	}
	pickerRun(t, ready, pickerKey(ready, "y"))
	after, err := os.ReadFile(filepath.Join(home, "rollout.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("noop rewrote policy", err)
	}
}

func TestProjectPickerPendingApplyResizeAndUnknownRecovery(t *testing.T) {
	m, home := pickerReviewFixture(t, true)
	original := pickerKey(m, "y")
	if original == nil || m.stage != pickerApplying {
		t.Fatal(m.stage)
	}
	m.Update(tea.WindowSizeMsg{Width: 10, Height: 5})
	pickerKey(m, "esc")
	pickerKey(m, "ctrl+c")
	if m.stage != pickerApplying || m.cancelled || !strings.Contains(m.View().Content, "Waiting for the result") {
		t.Fatal(m.stage, m.View().Content)
	}
	unknown := m.result(errors.New("owned interruption"))
	if unknown == nil || !strings.Contains(unknown.Error(), "--home") || !strings.Contains(unknown.Error(), "rollout history") {
		t.Fatal(unknown)
	}
	pickerRun(t, m, original)
	if !m.applied || m.stage != pickerOffer {
		t.Fatal(m.stage, m.notice, m.err)
	}
	state, err := (rollout.Store{Home: home}).Read()
	if err != nil || len(state.Projects) != 1 {
		t.Fatal(state, err)
	}
}

func TestProjectPickerCancellationIgnoresLateWork(t *testing.T) {
	m, home := pickerFixture(t, false)
	pickerKey(m, "ctrl+space")
	pickerKey(m, "enter")
	pending := pickerKey(m, "enter")
	pickerKey(m, "esc")
	pickerRun(t, m, pending)
	if m.stage != pickerDone || !m.cancelled {
		t.Fatal(m.stage)
	}
	if _, err := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	loading := newProjectOnboard(t.Context(), home, "Canceled discovery", nil, nil)
	loading.discover = func(ctx context.Context) (agentprojects.Catalog, agentprojects.Hierarchy, error) {
		return agentprojects.Catalog{}, agentprojects.Hierarchy{Version: 1}, ctx.Err()
	}
	late := loading.Init()
	pickerKey(loading, "esc")
	pickerRun(t, loading, late)
	if loading.stage != pickerDone || loading.tree != nil {
		t.Fatal(loading.stage)
	}
}

func TestProjectPickerManualProjectOnlyAndDiagnostics(t *testing.T) {
	m, home := pickerFixture(t, false)
	pickerKey(m, "ctrl+o")
	if m.stage != pickerDiagnostics || !strings.Contains(m.reviewContent, "Owned fixture coverage only") {
		t.Fatal(m.stage, m.reviewContent)
	}
	pickerKey(m, "esc")
	root := m.tree.groups[0].Directory
	pickerKey(m, "ctrl+n")
	m.editor.SetValue(root)
	m.probeFactory = func(*gitmeta.Budget) (pickerGitProbe, error) { return nil, gitmeta.ErrUnsupported }
	pickerRun(t, m, pickerKey(m, "enter"))
	if m.stage != pickerSettings || len(m.tree.recipients) != 0 {
		t.Fatal(m.stage, m.tree)
	}
	m.settings[0].Mode = "disabled"
	pickerRun(t, m, pickerKey(m, "enter"))
	pickerRun(t, m, pickerKey(m, "y"))
	if m.stage != pickerDone || !m.applied {
		t.Fatal(m.stage, m.err, m.notice)
	}
	state, err := (rollout.Store{Home: home}).Read()
	if err != nil || len(state.Projects) != 1 || state.Projects[0].Bindings[0].Root != root {
		t.Fatal(state, err)
	}
}

func TestProjectPickerSettingsSurviveFilterBackAndInvalidatePreview(t *testing.T) {
	m, _ := pickerFixture(t, false)
	pickerType(t, m, "Jade")
	pickerKey(m, "enter")
	pickerKey(m, "i")
	m.editor.SetValue("chosen-project")
	pickerKey(m, "enter")
	pickerKey(m, "p")
	setting := m.settings[0]
	generation := m.generation
	pickerKey(m, "esc")
	if m.rows[m.cursor].child != 1 {
		t.Fatal("context parent replaced actual search match", m.rows, m.cursor)
	}
	pickerKey(m, "enter")
	if m.settings[0] != setting || m.generation <= generation || m.plan.Review != "" {
		t.Fatal(m.settings, m.generation, generation, m.plan)
	}
}

func TestProjectPickerTypedPossibleSaveFailureDoesNotOfferActivation(t *testing.T) {
	m, _ := pickerReviewFixture(t, true)
	pickerKey(m, "y")
	m.Update(pickerAppliedMsg{err: &rollout.OnboardOutcomeUnknown{Review: m.plan.Review, Err: errors.New("owned possible-save failure")}})
	if m.stage != pickerDone || m.err == nil || m.cancelled || m.applied || !strings.Contains(m.err.Error(), "--home") || !strings.Contains(m.err.Error(), "rollout status") {
		t.Fatal(m.stage, m.err, m.cancelled, m.applied)
	}
	if strings.Contains(m.View().Content, "No onboarding policy was applied") {
		t.Fatal("possible save reported as rollback", m.View().Content)
	}
}

func TestProjectPickerDeliveredUnknownSurvivesTerminalCancellation(t *testing.T) {
	for _, phase := range []string{"before-confirmation", "possible-save-result", "definitive-refusal", "known-success"} {
		for _, human := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/human=%t", phase, human), func(t *testing.T) {
				m, home := pickerReviewFixture(t, true)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.ctx = ctx
				if phase != "before-confirmation" {
					original := pickerKey(m, "y")
					switch phase {
					case "possible-save-result":
						m.Update(pickerAppliedMsg{err: &rollout.OnboardOutcomeUnknown{Review: m.plan.Review, Err: errors.New("owned possible-save result")}})
					case "definitive-refusal":
						m.Update(pickerAppliedMsg{err: errors.New("owned definitive refusal")})
					case "known-success":
						pickerRun(t, m, original)
					}
				}
				cancel()
				var stdout, stderr bytes.Buffer
				a := app{ctx: ctx, home: home, out: &stdout, err: &stderr, outputTTY: human, errorTTY: human}
				result := m.result(context.Canceled)
				code := a.finishCommand(0, fmt.Errorf("terminal ended: %w", result))
				if code != 130 || stdout.Len() != 0 {
					t.Fatal(code, stdout.String(), stderr.String())
				}
				expected := "interrupted"
				if phase == "possible-save-result" {
					expected = "rollout_outcome_unknown"
					var detail *model.Error
					if !errors.As(result, &detail) || detail.Code != expected {
						t.Fatal("delivered uncertainty lost", result)
					}
				}
				message := stderr.String()
				if !human {
					var wire struct {
						Error *model.Error `json:"error"`
					}
					if err := json.Unmarshal(stderr.Bytes(), &wire); err != nil || wire.Error == nil || wire.Error.Code != expected {
						t.Fatal(err, message)
					}
					message = wire.Error.Message
				} else if !strings.Contains(message, expected) {
					t.Fatal(message)
				}
				if phase == "possible-save-result" {
					for _, want := range []string{installNextCommand(home, "rollout status"), installNextCommand(home, "rollout history")} {
						if !strings.Contains(message, want) {
							t.Fatal("custom-home recovery lost", want, message)
						}
					}
				} else if strings.Contains(message, "may have been applied") {
					t.Fatal("definitive result fabricated uncertainty", phase, message)
				}
				state, err := (rollout.Store{Home: home}).Read()
				wantProjects := 0
				if phase == "known-success" {
					wantProjects = 1
				}
				if err != nil || len(state.Projects) != wantProjects || len(state.Sessions) != 0 {
					t.Fatal("reporting retried policy or enrolled a chat", state, err)
				}
			})
		}
	}
}

func TestProjectPickerRefreshUpdatesSelectedHomeReadiness(t *testing.T) {
	m, home := pickerFixture(t, false)
	group := cloneOnboardGroup(m.tree.groups[0])
	m.discover = func(ctx context.Context) (agentprojects.Catalog, agentprojects.Hierarchy, error) {
		return agentprojects.Catalog{}, agentprojects.Hierarchy{Version: 1, Groups: []agentprojects.Group{group}}, ctx.Err()
	}
	if m.readiness.Ready || !strings.Contains(m.View().Content, "Instructions not ready") {
		t.Fatal(m.readiness, m.View().Content)
	}
	if _, err := install.Install(home, []install.Target{{Adapter: "codex", Root: group.Sessions[0].Ref.Home, Name: "AGENTS.md"}}, false); err != nil {
		t.Fatal(err)
	}
	pickerRun(t, m, pickerKey(m, "ctrl+r"))
	if !m.readiness.Ready || strings.Contains(m.View().Content, "Instructions not ready") {
		t.Fatal("recovery banner stale", m.readiness, m.View().Content)
	}
	for _, target := range install.InspectInstructions(home).Targets {
		if target.State == "ready" {
			if err := os.Remove(target.Path); err != nil {
				t.Fatal(err)
			}
		}
	}
	pickerRun(t, m, pickerKey(m, "ctrl+r"))
	if m.readiness.Ready || !strings.Contains(m.View().Content, "Instructions not ready") {
		t.Fatal("removal banner stale", m.readiness, m.View().Content)
	}
}

func TestProjectPickerPageNavigationBlursSearchInput(t *testing.T) {
	m, _ := pickerFixture(t, false)
	for _, key := range []string{"pgdown", "pgup"} {
		pickerType(t, m, "a")
		if !m.searchFocus || !m.search.Focused() {
			t.Fatal("search not focused")
		}
		pickerKey(m, key)
		if m.searchFocus || m.search.Focused() {
			t.Fatal("list routing retained search focus", key)
		}
		pickerKey(m, "esc")
	}
}
