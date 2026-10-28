package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func runFixture(t *testing.T, data []byte) (*dashboardModel, *testTransport, model.Run) {
	m, s := fixtureDashboard(t)
	run := model.Run{ID: "a", Name: "readable-run", Command: []string{"test", "--all"}, Agent: "worker", Reason: "Verification", State: "terminal", LogBytes: int64(len(data))}
	s.reply = func(op string, p, result any) error {
		switch op {
		case "runs":
			*result.(*[]model.Run) = []model.Run{run}
		case "run_output":
			q := p.(map[string]any)
			offset := q["offset"].(int64)
			end := min(len(data), int(offset)+3)
			*result.(*model.RunLog) = model.RunLog{Run: run, Offset: offset, NextOffset: int64(end), Data: data[offset:end], Done: true}
		}
		return nil
	}
	return m, s, run
}
func runRead(t *testing.T, m *dashboardModel, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing run read")
	}
	msg, ok := cmd().(runsReadMsg)
	if !ok {
		t.Fatal("unexpected message")
	}
	return m.acceptRunsRead(msg)
}
func TestRunsNavigationFollowPauseAndTerminalDrain(t *testing.T) {
	m, s, _ := runFixture(t, []byte("A🙂B"))
	runRead(t, m, press(m, "8"))
	if m.view != 7 || m.itemCount() != 1 || !strings.Contains(m.runsText(), "Verification") {
		t.Fatal("missing Runs tab inventory")
	}
	cmd := runRead(t, m, press(m, "enter"))
	if !m.runsUI.following || m.runsUI.done || m.runsUI.offset != 3 {
		t.Fatal("first terminal page did not preserve continuation")
	}
	if got := safeRunOutput(m.runsUI.data, false); got != "A" {
		t.Fatal("split UTF-8 shown early", got)
	}
	runRead(t, m, cmd)
	if !m.runsUI.done || string(m.runsUI.data) != "A🙂B" {
		t.Fatal("terminal output not fully drained")
	}
	press(m, "f")
	if m.runsUI.following {
		t.Fatal("pause did not apply")
	}
	before := len(s.operations)
	runRead(t, m, m.readRuns(false))
	if len(s.operations) != before+1 || s.operations[len(s.operations)-1] != "runs" {
		t.Fatal("paused output was reread")
	}
	runRead(t, m, m.runKey("home"))
	if m.runsUI.offset != 3 || m.runsUI.following {
		t.Fatal("read from start must be paused")
	}
	runRead(t, m, m.runKey("n"))
	if string(m.runsUI.data) != "A🙂B" {
		t.Fatal("manual pagination failed")
	}
	press(m, "1")
	if m.view != 0 || m.runsUI.selected != nil {
		t.Fatal("detail swallowed navigation")
	}
}
func TestRunsLateSelectionAndSessionReceiptsAreIgnored(t *testing.T) {
	m, _, run := runFixture(t, []byte("x"))
	m.view = 7
	m.runsUI = &runsDashboard{records: []model.Run{run}}
	cmd := m.openRun()
	msg := cmd().(runsReadMsg)
	m.retireRuns()
	replacement := run
	replacement.ID = "other"
	m.runsUI.selected = &replacement
	m.acceptRunsRead(msg)
	if len(m.runsUI.data) != 0 || m.runsUI.selected.ID != "other" {
		t.Fatal("obsolete output crossed selection")
	}
	msg.generation = m.runsUI.generation
	m.session = newTestTransport()
	m.acceptRunsRead(msg)
	if len(m.runsUI.data) != 0 {
		t.Fatal("old session output accepted")
	}
}
func TestRunsSafeOutputWindowAndReporterLoss(t *testing.T) {
	for _, fixture := range []struct {
		data []byte
		done bool
		want string
	}{
		{[]byte("A\xf0\x9f"), false, "A"},
		{[]byte("A\xf0\x9f"), true, "A�"},
		{[]byte("A\xffB"), false, "A�B"},
		{[]byte("ok\n\x1b[2J\t\u202e"), true, "ok\n\\u001b[2J    \\u202e"},
	} {
		if got := safeRunOutput(fixture.data, fixture.done); got != fixture.want {
			t.Fatalf("unsafe/incorrect output %q want %q", got, fixture.want)
		}
	}
	m, s, run := runFixture(t, nil)
	m.view = 7
	run.State = "interrupted"
	run.Outcome = "reporter_lost"
	run.Truncated = true
	run.LogBytes = runDisplayLimit + 5
	m.runsUI = &runsDashboard{selected: &run, following: true, data: bytes.Repeat([]byte("x"), runDisplayLimit), offset: runDisplayLimit}
	page := model.RunLog{Run: run, Offset: runDisplayLimit, NextOffset: runDisplayLimit + 5, Data: []byte("final"), Done: true}
	m.acceptRunsRead(runsReadMsg{ui: m.runsUI, session: s, generation: m.runsUI.generation, page: &page})
	if len(m.runsUI.data) != runDisplayLimit || !m.runsUI.done || !m.runsUI.displayCut {
		t.Fatal("unbounded display or missing drain")
	}
	text := m.runsText()
	for _, want := range []string{"process completion is unknown", "latest 256 KiB", "retention limit", "no resource lease"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing limitation", want)
		}
	}
}
func TestRunsPausedReadRetiredOnHide(t *testing.T) {
	m, _, _ := runFixture(t, nil)
	m.view = 7
	cmd := m.readRuns(false)
	if cmd == nil || !m.runsUI.busy {
		t.Fatal("read not started")
	}
	m.view = 0
	m.retireRunsIfHidden()
	if m.runsUI.busy {
		t.Fatal("hidden read slot not retired")
	}
	msg := cmd().(runsReadMsg)
	m.acceptRunsRead(msg)
	if len(m.runsUI.records) != 0 {
		t.Fatal("hidden receipt accepted")
	}
}

func TestRunsNativeFramesFitNarrowAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{45, 14}, {70, 24}, {140, 42}} {
		m, _, run := runFixture(t, nil)
		m.width, m.height = size[0], size[1]
		m.view = 7
		run.Command = []string{"build", "--all"}
		m.runsUI = &runsDashboard{records: []model.Run{run}, selected: &run, following: true, data: []byte(strings.Repeat("compiling\n", 100) + "FINAL OUTPUT\n")}
		m.syncViewport()
		m.viewport.GotoBottom()
		frame := ansi.Strip(m.View().Content)
		lines := strings.Split(frame, "\n")
		if len(lines) != m.height {
			t.Fatalf("%v frame height %d", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatalf("%v frame width %d", size, ansi.StringWidth(line))
			}
		}
		if !strings.Contains(frame, "FINAL OUTPUT") || !strings.Contains(frame, "Runs") {
			t.Fatalf("%v following output or navigation absent", size)
		}
	}
}
