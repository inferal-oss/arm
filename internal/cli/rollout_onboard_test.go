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
	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

func onboardKey(m *rolloutOnboardModel, key string) tea.Cmd {
	code := rune(0)
	switch key {
	case "enter":
		code = tea.KeyEnter
	case "esc":
		code = tea.KeyEscape
	case "down":
		code = tea.KeyDown
	case "up":
		code = tea.KeyUp
	case "ctrl+c":
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		return cmd
	default:
		code = []rune(key)[0]
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}
func runOnboardCmd(t *testing.T, m *rolloutOnboardModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an asynchronous operation")
	}
	m.Update(cmd())
}
func onboardReviewFixture(t *testing.T, ready bool) (*rolloutOnboardModel, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "demo")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if ready {
		if _, e := install.Install(home, []install.Target{{Adapter: "codex", Root: filepath.Join(base, "codex"), Name: "AGENTS.md"}}, false); e != nil {
			t.Fatal(e)
		}
	}
	m := newRolloutOnboard(t.Context(), home, "Interactive project pilot", nil)
	m.Update(onboardCatalogMsg{agentprojects.Catalog{Projects: []agentprojects.Project{{Directory: root}}}})
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	if m.stage != onboardScope {
		t.Fatal(m.stage, m.notice)
	}
	// Directory scope is the last choice, independent of fixture Git parents.
	m.cursor = len(m.choices) - 1
	onboardKey(m, "enter")
	if m.stage != onboardName {
		t.Fatal(m.stage)
	}
	m.input.SetValue("demo")
	onboardKey(m, "enter")
	if m.stage != onboardPolicy {
		t.Fatal(m.stage)
	}
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	if m.stage != onboardReview {
		t.Fatal(m.stage, m.notice)
	}
	return m, home
}

func TestRolloutOnboardDefaultNoAndAtomicConfirmedPolicy(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "decline", true: "apply"}[apply], func(t *testing.T) {
			m, home := onboardReviewFixture(t, true)
			if _, e := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(e) {
				t.Fatal("preview wrote policy", e)
			}
			if !strings.Contains(m.View().Content, "Apply these changes? [y/N]") {
				t.Fatal(m.View().Content)
			}
			if !apply {
				onboardKey(m, "enter")
				if !m.cancelled {
					t.Fatal("default accepted onboarding")
				}
				if _, e := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(e) {
					t.Fatal(e)
				}
				return
			}
			runOnboardCmd(t, m, onboardKey(m, "y"))
			st, e := (rollout.Store{Home: home}).Read()
			if e != nil || m.err != nil || len(st.Projects) != 1 || st.Projects[0].Mode != "new-sessions" || st.Revision != 1 || len(st.History) != 1 || st.History[0].Action != "onboard" {
				t.Fatal(st, e, m.err)
			}
			if len(st.Sessions) != 0 {
				t.Fatal("onboarding enrolled sessions", st.Sessions)
			}
		})
	}
}

func TestRolloutOnboardReadinessAndStaleReviewCannotApply(t *testing.T) {
	m, home := onboardReviewFixture(t, false)
	command := installNextCommand(home, "install")
	if !strings.Contains(m.reviewText(), command) {
		t.Fatal("review omits selected-home installation command")
	}
	onboardKey(m, "y")
	if m.stage != onboardPolicy || m.applyStarted || !strings.Contains(m.notice, "instructions are not ready") {
		t.Fatal(m.stage, m.notice)
	}
	if !strings.Contains(m.notice, command) {
		t.Fatal("readiness refusal omits selected-home installation command", m.notice)
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("unready onboarding initialized state", e)
	}
	m, home = onboardReviewFixture(t, true)
	other := t.TempDir()
	store := rollout.Store{Home: home}
	change := rollout.Change{Action: "project", Project: "other", Binding: &rollout.Binding{Kind: "directory", Root: other}, Reason: "Concurrent operator choice"}
	plan, e := store.Preview(change)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Apply(change, plan.Review); e != nil {
		t.Fatal(e)
	}
	runOnboardCmd(t, m, onboardKey(m, "y"))
	st, e := store.Read()
	if e != nil || m.err == nil || len(st.Projects) != 1 || st.Projects[0].ID != "other" {
		t.Fatal(st, e, m.err)
	}
}

func TestRolloutOnboardResizeDuringApplyWaitsForActualResult(t *testing.T) {
	for _, key := range []string{"esc", "q", "ctrl+c"} {
		for _, failed := range []bool{false, true} {
			name := key + map[bool]string{false: "/success", true: "/failure"}[failed]
			t.Run(name, func(t *testing.T) {
				m, home := onboardReviewFixture(t, true)
				apply := onboardKey(m, "y")
				m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
				quit := onboardKey(m, key)
				if quit != nil || m.cancelled || m.stage != onboardApplying || strings.Contains(m.View().Content, "Esc cancels") {
					t.Fatal("pending write was reported as cancelled", m.stage, m.View().Content)
				}
				if failed {
					m.Update(onboardAppliedMsg{err: errors.New("synthetic write failure")})
				} else {
					runOnboardCmd(t, m, apply)
				}
				if m.stage != onboardDone || m.cancelled || m.notice != "" || (m.err != nil) != failed {
					t.Fatal("write result was lost", m.stage, m.err)
				}
				st, err := (rollout.Store{Home: home}).Read()
				if err != nil || (!failed && (len(st.Projects) != 1 || st.Projects[0].Mode != "new-sessions")) || (failed && len(st.Projects) != 0) {
					t.Fatal("policy does not match reported result", st, err)
				}
			})
		}
	}
}

func TestRolloutOnboardInterruptedOutcomeSurvivesSharedHandler(t *testing.T) {
	for _, phase := range []string{"before-confirmation", "pending", "committed-without-result"} {
		for _, human := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/human=%t", phase, human), func(t *testing.T) {
				m, home := onboardReviewFixture(t, true)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.ctx = ctx
				if phase != "before-confirmation" {
					apply := onboardKey(m, "y")
					if phase == "committed-without-result" {
						// Persist the confirmed change but lose the result before the
						// terminal loop can acknowledge it, as with external cancellation.
						if result := apply().(onboardAppliedMsg); result.err != nil {
							t.Fatal(result.err)
						}
					}
				}
				cancel()
				var stdout, stderr bytes.Buffer
				a := app{ctx: ctx, home: home, out: &stdout, err: &stderr, outputTTY: human, errorTTY: human}
				err := m.result(context.Canceled)
				code := a.finishCommand(0, fmt.Errorf("terminal ended: %w", err))
				if code != 130 || stdout.Len() != 0 {
					t.Fatal(code, stdout.String(), stderr.String())
				}
				message := stderr.String()
				expected := "interrupted"
				if phase != "before-confirmation" {
					var detail *model.Error
					if !errors.As(err, &detail) || detail.Code != "rollout_outcome_unknown" {
						t.Fatal("pending apply did not return a typed outcome", err)
					}
					expected = detail.Code
				}
				if !human {
					var wire struct {
						Error *model.Error `json:"error"`
					}
					if json.Unmarshal(stderr.Bytes(), &wire) != nil || wire.Error == nil || wire.Error.Code != expected {
						t.Fatal("shared handler discarded interruption detail", message)
					}
					message = wire.Error.Message
				}
				if phase == "before-confirmation" {
					if !strings.Contains(message, "operation interrupted") || strings.Contains(message, "may have been applied") {
						t.Fatal("ordinary interruption acquired pending-write semantics", message)
					}
				} else {
					for _, want := range []string{"may have been applied", installNextCommand(home, "rollout status"), installNextCommand(home, "rollout history")} {
						if !strings.Contains(message, want) {
							t.Fatal("recovery guidance was lost", want, message)
						}
					}
				}
				st, err := (rollout.Store{Home: home}).Read()
				wantProjects := 0
				if phase == "committed-without-result" {
					wantProjects = 1
				}
				if err != nil || len(st.Projects) != wantProjects || len(st.Sessions) != 0 {
					t.Fatal("interruption reporting changed policy or enrolled sessions", st, err)
				}
			})
		}
	}
}

func TestRolloutOnboardCanSaveDisabledProjectWithoutInstructions(t *testing.T) {
	m, home := onboardReviewFixture(t, false)
	onboardKey(m, "b")
	m.cursor = len(m.choices) - 1
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	if m.mode != "disabled" {
		t.Fatal(m.mode)
	}
	runOnboardCmd(t, m, onboardKey(m, "y"))
	st, e := (rollout.Store{Home: home}).Read()
	if e != nil || m.err != nil || len(st.Projects) != 1 || st.Projects[0].Mode != "disabled" || len(st.Sessions) != 0 {
		t.Fatal(st, e, m.err)
	}
}

func TestRolloutOnboardBackNavigationSmallTerminalAndSafeLabels(t *testing.T) {
	m, home := onboardReviewFixture(t, true)
	onboardKey(m, "b")
	if m.stage != onboardPolicy {
		t.Fatal(m.stage)
	}
	onboardKey(m, "esc")
	if m.stage != onboardName {
		t.Fatal(m.stage)
	}
	m.input.SetValue("Invalid Name")
	onboardKey(m, "enter")
	if m.stage != onboardName || m.notice == "" {
		t.Fatal(m.stage, m.notice)
	}
	m.input.SetValue("demo")
	onboardKey(m, "enter")
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 10})
	onboardKey(m, "y")
	if m.applyStarted || !strings.Contains(m.View().Content, "Enlarge the terminal") {
		t.Fatal(m.View().Content)
	}
	if _, e := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	item := onboardItem{project: agentprojects.Project{Directory: "/work/\x1b[2Jforged", Evidence: []agentprojects.Evidence{{Adapter: "codex"}}}}
	if strings.Contains(item.Title()+item.Description(), "\x1b[2J") {
		t.Fatal("unsafe directory label")
	}
}

func TestRolloutOnboardDiscoveryJSONAndHelpDoNotInitializePolicy(t *testing.T) {
	// Never connect to a real native client during fixture discovery.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	base := t.TempDir()
	home := filepath.Join(base, "state")
	codex := filepath.Join(base, "codex")
	claude := filepath.Join(base, "claude")
	project := filepath.Join(base, "demo")
	for _, path := range []string{codex, claude, project} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	b, _ := json.Marshal(map[string]any{"active-workspace-roots": []string{project}})
	if e := os.WriteFile(filepath.Join(codex, ".codex-global-state.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	code, out, stderr := rolloutCLI(t, home, "rollout", "project", "onboard", "--codex-home", codex, "--claude-home", claude, "--json")
	var result struct {
		ReadOnly bool                  `json:"read_only"`
		Catalog  agentprojects.Catalog `json:"catalog"`
	}
	if code != 0 || json.Unmarshal(out, &result) != nil || !result.ReadOnly || len(result.Catalog.Projects) != 1 {
		t.Fatal(code, string(out), stderr)
	}
	for _, args := range [][]string{{"rollout", "project", "onboard", "--help"}, {"help", "rollout", "project", "onboard"}} {
		if code, out, stderr = rolloutCLI(t, home, args...); code != 0 || !strings.Contains(string(out), "--claude-config") {
			t.Fatal(code, string(out), stderr)
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("discovery created policy state", e)
	}
}

func TestRolloutOnboardSuggestsUniqueNameAndIgnoresLateCancelledDiscovery(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "demo")
	other := filepath.Join(base, "other")
	for _, path := range []string{root, other} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	store := rollout.Store{Home: home}
	change := rollout.Change{Action: "project", Project: "demo", Binding: &rollout.Binding{Kind: "directory", Root: other}, Reason: "Existing unrelated project"}
	plan, e := store.Preview(change)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Apply(change, plan.Review); e != nil {
		t.Fatal(e)
	}
	m := newRolloutOnboard(t.Context(), home, "New project", nil)
	catalog := onboardCatalogMsg{agentprojects.Catalog{Projects: []agentprojects.Project{{Directory: root}}}}
	m.Update(catalog)
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	m.cursor = len(m.choices) - 1
	onboardKey(m, "enter")
	if m.id != "demo-2" {
		t.Fatal("unrelated project was suggested for implicit scope expansion", m.id)
	}
	m = newRolloutOnboard(t.Context(), home, "Cancelled discovery", nil)
	onboardKey(m, "esc")
	m.Update(catalog)
	if !m.cancelled || m.stage != onboardDone {
		t.Fatal("late discovery resurrected a cancelled editor", m.stage)
	}
}

func TestRolloutOnboardSessionNamesSearchAndDirectoryFallback(t *testing.T) {
	item := onboardItem{project: agentprojects.Project{Directory: "/work/abcd/project", Names: []string{"Zulu", "Alpha", "Alpha", "Delta", "Hidden search name"}, Evidence: []agentprojects.Evidence{{Adapter: "codex"}}}}
	if !strings.HasPrefix(item.Title(), "Session names: Alpha · Delta · Hidden search name (+1 more)") || !strings.Contains(item.Description(), "/work/abcd/project") || !strings.Contains(item.FilterValue(), "Zulu") {
		t.Fatal(item.Title(), item.Description(), item.FilterValue())
	}
	item.project.Names = []string{"  Fix\nstatus\x1b[2J\u202e" + strings.Repeat("界", 300)}
	if strings.ContainsAny(item.Title()+item.FilterValue(), "\n\x1b\u202e") || !strings.Contains(item.Title(), "…") {
		t.Fatal(item.Title())
	}
	item.project.Names = nil
	if !strings.HasPrefix(item.Title(), "Directory: project") || !strings.Contains(item.Description(), item.project.Directory) {
		t.Fatal(item)
	}
	manual := onboardItem{manual: true}
	if !strings.Contains(manual.Title(), "another directory") {
		t.Fatal(manual)
	}
}

func TestRolloutOnboardSessionNamesDoNotChangeReviewedScopeOrApply(t *testing.T) {
	m, home := onboardReviewFixture(t, true)
	before, _ := json.Marshal(m.change())
	review := m.plan.Review
	// Return to the directory picker with names attached to the same directory.
	root := m.directory.Root
	m.stage = onboardLoading
	m.Update(onboardCatalogMsg{agentprojects.Catalog{Projects: []agentprojects.Project{{Directory: root, Names: []string{"Unrelated task name", "Second task"}}}}})
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	m.cursor = len(m.choices) - 1
	onboardKey(m, "enter")
	if m.id != "demo" || m.binding.Root != root {
		t.Fatal(m.id, m.binding)
	}
	onboardKey(m, "enter")
	runOnboardCmd(t, m, onboardKey(m, "enter"))
	after, _ := json.Marshal(m.change())
	if !bytes.Equal(before, after) || review != m.plan.Review {
		t.Fatal("display names changed exact policy review", string(before), string(after))
	}
	runOnboardCmd(t, m, onboardKey(m, "y"))
	st, e := (rollout.Store{Home: home}).Read()
	if e != nil || len(st.Projects) != 1 || st.Projects[0].Bindings[0].Root != root || len(st.Sessions) != 0 {
		t.Fatal(st, e)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "Unrelated task") || strings.Contains(string(raw), "Second task") {
		t.Fatal("display names persisted into policy", string(raw))
	}
}
