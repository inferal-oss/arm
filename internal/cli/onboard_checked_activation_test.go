package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/activation"
	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
	"github.com/google/uuid"
)

func checkedKey(m *checkedActivationModel, key string) tea.Cmd {
	code := rune(0)
	switch key {
	case "enter":
		code = tea.KeyEnter
	case "esc":
		code = tea.KeyEscape
	case "right":
		code = tea.KeyRight
	default:
		code = []rune(key)[0]
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
}
func checkedDrain(t *testing.T, m *checkedActivationModel, cmd tea.Cmd) {
	t.Helper()
	for steps := 0; cmd != nil; steps++ {
		if steps > 10 {
			t.Fatal("unbounded model commands")
		}
		_, cmd = m.Update(cmd())
	}
}
func checkedFixture(t *testing.T) (*checkedActivationModel, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home, native, root, first := activationFixture(t)
	native, _ = filepath.EvalSymlinks(native)
	root, _ = filepath.EvalSymlinks(root)
	group := agentprojects.Group{Key: "fixture"}
	var threads []any
	for index, id := range []string{first, uuid.NewString(), uuid.NewString()} {
		saved := agentsessions.SavedSession{Ref: agentsessions.SavedSessionRef{Adapter: "codex", Home: native, ID: id}, Directory: root, Name: fmt.Sprintf("Fixture task %d", index)}
		group.Sessions = append(group.Sessions, saved)
		threads = append(threads, map[string]any{"id": id, "cwd": root, "createdAt": 1760000000, "status": map[string]any{"type": "active"}})
	}
	activationJSON(t, filepath.Join(native, "threads.json"), map[string]any{"data": threads, "nextCursor": nil})
	selection := pickerSelection{Groups: []agentprojects.Group{group}, Settings: []pickerSetting{{Group: "fixture", Project: "demo", Mode: "all"}}}
	for _, index := range []int{0, 2} {
		selection.Checked = append(selection.Checked, pickerChecked{Group: "fixture", Session: group.Sessions[index]})
	}
	m, err := newCheckedActivation(&app{ctx: t.Context(), home: home}, selection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.close)
	return m, home, native
}
func TestCheckedActivationExactSubsetQueuesAfterReview(t *testing.T) {
	m, home, native := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	if m.stage != checkedReview || m.ready() != 2 {
		t.Fatal(m.stage, m.results())
	}
	if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
		t.Fatal("verification delivered", err)
	}
	unchecked := m.selection.Groups[0].Sessions[1]
	for index := range m.entries {
		m.cursor = index
		if content := m.reviewEntry(); strings.Contains(content, unchecked.Ref.ID) || !strings.Contains(content, m.entries[index].key) || !strings.Contains(content, "Exact message:") {
			t.Fatal(content)
		}
	}
	checkedDrain(t, m, checkedKey(m, "y"))
	if m.stage != checkedResults {
		t.Fatal(m.stage)
	}
	data, err := os.ReadFile(filepath.Join(native, "queued.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || strings.Contains(string(data), unchecked.Ref.ID) {
		t.Fatal(string(data))
	}
	for index, line := range lines {
		var sent map[string]string
		if json.Unmarshal([]byte(line), &sent) != nil || sent["session"] != m.entries[index].checked.Session.Ref.ID || sent["message"] != m.entries[index].plan.Message {
			t.Fatal(line)
		}
		r, err := (activation.Store{Home: home}).Lookup(m.entries[index].key)
		if err != nil || r.State != "queued" {
			t.Fatal(r, err)
		}
	}
	if checkedKey(m, "y") != nil {
		t.Fatal("results allowed resend")
	}
	if !strings.Contains(m.results(), "not that the chat has enrolled") {
		t.Fatal(m.results())
	}
}
func TestCheckedActivationReviewDefaultsToNo(t *testing.T) {
	for _, key := range []string{"enter", "n", "esc"} {
		t.Run(key, func(t *testing.T) {
			m, _, native := checkedFixture(t)
			checkedDrain(t, m, m.Init())
			if cmd := checkedKey(m, key); cmd == nil {
				t.Fatal("review did not close")
			}
			if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
				t.Fatal("decline delivered", err)
			}
		})
	}
}
func TestCheckedActivationSelectionContextIsDefensive(t *testing.T) {
	m, _, _ := checkedFixture(t)
	original := m.entries[0].checked.Session
	input := clonePickerSelection(m.selection)
	owned, err := newCheckedActivation(m.a, input)
	if err != nil {
		t.Fatal(err)
	}
	defer owned.close()
	input.Checked[0].Session.Name = "changed"
	input.Groups[0].Sessions[0].Name = "changed"
	input.Settings[0].Project = "changed"
	if owned.entries[0].checked.Session != original || owned.selection.Groups[0].Sessions[0] != original || owned.entries[0].project != "demo" {
		t.Fatal(owned.entries)
	}
	for _, variant := range []string{"duplicate", "directory", "home", "group", "missing"} {
		t.Run(variant, func(t *testing.T) {
			selection := clonePickerSelection(m.selection)
			switch variant {
			case "duplicate":
				selection.Checked = append(selection.Checked, selection.Checked[0])
			case "directory":
				selection.Checked[0].Session.Directory = t.TempDir()
			case "home":
				selection.Checked[0].Session.Ref.Home = t.TempDir()
			case "group":
				selection.Checked[0].Group = "other"
			case "missing":
				selection.Checked[0].Session.Ref.ID = uuid.NewString()
			}
			if unexpected, err := newCheckedActivation(m.a, selection); err == nil {
				unexpected.close()
				t.Fatal("accepted changed checked context")
			}
		})
	}
}
func TestCheckedActivationChangedNativeContextRefuses(t *testing.T) {
	for _, variant := range []string{"directory", "home"} {
		t.Run(variant, func(t *testing.T) {
			m, _, native := checkedFixture(t)
			saved := m.entries[0].checked
			if variant == "directory" {
				activationJSON(t, filepath.Join(native, "threads.json"), map[string]any{"data": []any{map[string]any{"id": saved.Session.Ref.ID, "cwd": t.TempDir(), "createdAt": 1760000000}}})
			} else {
				saved.Session.Ref.Home = t.TempDir()
			}
			if _, err := m.a.checkedActivationPlan(t.Context(), saved, "demo"); err == nil {
				t.Fatal("accepted changed native context")
			}
			if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
func TestCheckedActivationClaudeRemainsBlocked(t *testing.T) {
	m, _, native := checkedFixture(t)
	entry := m.entries[0].checked
	entry.Session.Ref.Adapter = "claude"
	_, err := m.a.checkedActivationPlan(t.Context(), entry, "demo")
	if err == nil || !strings.Contains(err.Error(), "Claude") {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
func TestCheckedActivationUnknownStopsBatchAndRetainsKey(t *testing.T) {
	m, _, _ := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	calls := 0
	m.send = func(ctx context.Context, p activation.Plan, key string, checked pickerChecked) (activation.Record, error) {
		calls++
		return activation.Record{Key: key, State: "unknown"}, fmt.Errorf("fixture lost acknowledgment")
	}
	key := m.entries[0].key
	checkedDrain(t, m, checkedKey(m, "y"))
	if calls != 1 || m.stage != checkedResults || m.entries[0].key != key || m.entries[1].attempted {
		t.Fatal(calls, m.entries)
	}
	if err := m.result(nil); err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "--home") {
		t.Fatal(err)
	}
}
func TestCheckedActivationCloseJoinsOriginalAndKeepsReceipt(t *testing.T) {
	m, _, _ := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	started, done := make(chan struct{}), make(chan struct{})
	m.send = func(ctx context.Context, p activation.Plan, key string, checked pickerChecked) (activation.Record, error) {
		close(started)
		<-ctx.Done()
		return activation.Record{Key: key, State: "queued"}, nil
	}
	cmd := checkedKey(m, "y")
	go func() { defer close(done); cmd() }()
	<-started
	m.close()
	<-done
	if m.entries[0].record.State != "queued" || !m.entries[0].attempted || m.entries[1].attempted {
		t.Fatal(m.results())
	}
	if err := m.result(context.Canceled); err != context.Canceled {
		t.Fatal("lost known receipt", err)
	}
	if late := m.task(func() tea.Msg { t.Error("closed task started"); return nil })(); late != nil {
		t.Fatal(late)
	}
}
func TestCheckedActivationStopAfterCurrentReceipt(t *testing.T) {
	m, _, _ := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	calls := 0
	m.send = func(ctx context.Context, p activation.Plan, key string, checked pickerChecked) (activation.Record, error) {
		calls++
		return activation.Record{Key: key, State: "queued"}, nil
	}
	cmd := checkedKey(m, "y")
	checkedKey(m, "esc")
	checkedDrain(t, m, cmd)
	if calls != 1 || m.stage != checkedResults || m.entries[1].attempted {
		t.Fatal(calls, m.results())
	}
}

func TestCheckedActivationPolicyBlocksBeforeNativeProbe(t *testing.T) {
	for _, mode := range []string{"new-sessions", "observe", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			m, home, _ := checkedFixture(t)
			rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", mode, "--reason", "Fixture blocked eligibility")
			// No native binary is available: the authoritative policy reason must
			// win before capability probing or proxy discovery.
			t.Setenv("PATH", t.TempDir())
			_, err := m.a.checkedActivationPlan(t.Context(), m.entries[0].checked, "demo")
			if err == nil || !strings.Contains(err.Error(), "not eligible:") {
				t.Fatal(err)
			}
		})
	}
}
func TestCheckedActivationPolicyChangesRefuseFinalSend(t *testing.T) {
	m, home, native := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	rolloutApplyCLI(t, home, "rollout", "policy", "--project", "demo", "--mode", "observe", "--reason", "Fixture policy changed after review")
	checkedDrain(t, m, checkedKey(m, "y"))
	if m.stage != checkedResults {
		t.Fatal(m.stage)
	}
	for _, entry := range m.entries {
		if entry.err == nil || !strings.Contains(entry.err.Error(), "not eligible:") || entry.record.Key != "" {
			t.Fatal(entry)
		}
	}
	if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
		t.Fatal("changed policy delivered", err)
	}
}
func TestCheckedActivationNativeChangeGivesPartialResults(t *testing.T) {
	m, _, native := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	second := m.entries[1].checked.Session
	activationJSON(t, filepath.Join(native, "threads.json"), map[string]any{"data": []any{map[string]any{"id": second.Ref.ID, "cwd": second.Directory, "createdAt": 1760000000}}})
	checkedDrain(t, m, checkedKey(m, "y"))
	if m.entries[0].err == nil || m.entries[0].record.Key != "" || m.entries[1].record.State != "queued" {
		t.Fatal(m.results())
	}
	data, err := os.ReadFile(filepath.Join(native, "queued.jsonl"))
	if err != nil || strings.Contains(string(data), m.entries[0].checked.Session.Ref.ID) || !strings.Contains(string(data), second.Ref.ID) {
		t.Fatal(string(data), err)
	}
}
func TestCheckedActivationCanceledBeforeAnyNativeWork(t *testing.T) {
	m, _, _ := checkedFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	t.Setenv("PATH", t.TempDir())
	if _, err := m.a.checkedActivationPlan(ctx, m.entries[0].checked, "demo"); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestCheckedActivationAggregateVerificationCancellation(t *testing.T) {
	m, _, _ := checkedFixture(t)
	m.verifyCancel()
	m.plan = func(context.Context, pickerChecked, string) (activation.Plan, error) {
		t.Fatal("expired batch started verification")
		return activation.Plan{}, nil
	}
	checkedDrain(t, m, m.Init())
	if m.stage != checkedReview || m.ready() != 0 {
		t.Fatal(m.stage, m.ready())
	}
	for _, entry := range m.entries {
		if entry.err == nil || !strings.Contains(entry.err.Error(), "verification stopped") {
			t.Fatal(entry)
		}
	}
}
func TestCheckedActivationDeclineIgnoresQueuedLateConfirmation(t *testing.T) {
	for _, key := range []string{"n", "enter", "esc"} {
		t.Run(key, func(t *testing.T) {
			m, _, native := checkedFixture(t)
			checkedDrain(t, m, m.Init())
			if checkedKey(m, key) == nil || !m.finished {
				t.Fatal("decline did not finish synchronously")
			}
			if checkedKey(m, "y") != nil || m.entries[0].attempted {
				t.Fatal("late confirmation started delivery")
			}
			if cmd := m.Init(); cmd != nil {
				t.Fatal("finished model prepared more work")
			}
			if _, err := os.Stat(filepath.Join(native, "queued.jsonl")); !os.IsNotExist(err) {
				t.Fatal(err)
			}
		})
	}
}
func TestCheckedActivationQuitIgnoresLateMetadataAndTasks(t *testing.T) {
	m, _, _ := checkedFixture(t)
	cmd := m.Init()
	checkedKey(m, "q")
	if !m.finished || cmd() != nil {
		t.Fatal("post-quit task started")
	}
	m.Update(checkedActivationPrepared{index: 0, plan: activation.Plan{Delivery: "queue"}})
	if m.entries[0].verified || checkedKey(m, "y") != nil {
		t.Fatal("late metadata revived the model")
	}
}
func TestCheckedActivationProgressShowsOriginalLookup(t *testing.T) {
	m, _, _ := checkedFixture(t)
	checkedDrain(t, m, m.Init())
	checkedKey(m, "y") // Schedule only; this fixture never executes the sender.
	key := m.entries[0].key
	for _, width := range []int{80, 30} {
		m.width = width
		view := m.View().Content
		compact := strings.Join(strings.Fields(view), "")
		if !strings.Contains(compact, key) || !strings.Contains(compact, "--home") || !strings.Contains(compact, "rolloutactivation") {
			t.Fatal(view)
		}
	}
}
