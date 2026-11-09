package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func guardianFixture() *model.GuardianStatus {
	cpu, mem := 3.5, uint64(6<<30)
	return &model.GuardianStatus{
		Mode: model.GuardianAct, Role: model.GuardianManned, At: time.Now(),
		Ladder:    []model.GuardianStepRule{{Step: "notify", Level: "high", After: 15 * time.Second}, {Step: "deprioritize", Level: "high", After: 30 * time.Second}},
		Pressure:  model.MonitorPressure{CPU: model.PressureReading{Level: model.PressureHigh, Reason: "load 2.4 per CPU"}, Memory: model.PressureReading{Level: model.PressureNormal}, IO: model.PressureReading{Level: model.PressureNormal}},
		Groups:    []model.GuardianGroup{{ID: "g1", Name: "Runaway.app", Processes: 4, CPUCores: &cpu, MemoryBytes: &mem}},
		Incidents: []model.GuardianIncident{{ID: "i1", Resource: "cpu", Level: model.PressureHigh, Peak: model.PressureHigh, Since: time.Now(), Step: "deprioritize"}},
		Actions: []model.GuardianAction{
			{ID: "act-2", At: time.Now(), Step: "deprioritize", GroupName: "Runaway.app", Outcome: model.GuardianApplied, Reversible: true, Reason: "CPU high"},
			{ID: "act-1", At: time.Now(), Step: "notify", GroupName: "Runaway.app", Outcome: model.GuardianApplied, Reason: "CPU high"},
		},
		Budget: model.GuardianBudget{Used: 1, Limit: 12},
	}
}

func TestGuardianPageSitsBesideMonitorWithoutMovingNumberKeys(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width, m.height = 130, 40
	nav := strings.Split(ansi.Strip(m.View().Content), "\n")[1]
	if !strings.Contains(nav, "1 Monitor") || !strings.Contains(nav, "g Guardian") || !strings.Contains(nav, "2 Resources") || strings.Index(nav, "g Guardian") > strings.Index(nav, "2 Resources") {
		t.Fatalf("navigation %q", nav)
	}
	press(m, "2")
	if m.view != 1 {
		t.Fatal("2 no longer opens Resources", m.view)
	}
	cmd := press(m, "g")
	if m.view != viewGuardian || cmd == nil {
		t.Fatal("g did not open Guardian with a read", m.view)
	}
	g := guardianFixture()
	s.reply = func(op string, _ any, result any) error {
		if op == "guardian_status" {
			*result.(*model.GuardianStatus) = *g
		}
		return nil
	}
	drain(t, m, cmd)
	text := ansi.Strip(m.View().Content)
	for _, want := range []string{"UNDER PRESSURE", "CPU high · 1 open incident", "act · runs every step", "manned · operator present", "1/12 this hour", "HIGH · load 2.4 per CPU", "notify ≥high 15s › deprioritize ≥high 30s", "Runaway.app", "INCIDENTS", "deprioritize", "[x] undo", "[g Guardian]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("guardian page lacks %q:\n%s", want, text)
		}
	}
	// Mouse navigation reaches Guardian through its shortcut.
	press(m, "1")
	for _, r := range m.mouseRegions() {
		if r.key == "nav8" {
			drain(t, m, m.acceptMouse(dashboardMouseMsg{frame: m.mouseFrame(), region: r}, m.mouseFrame()))
		}
	}
	if m.view != viewGuardian {
		t.Fatal("Guardian tab click", m.view)
	}
}

func TestGuardianUndoAsksBeforeSending(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width, m.height = 130, 40
	m.view = viewGuardian
	m.data.guardian = guardianFixture()
	undone := 0
	s.reply = func(op string, params any, result any) error {
		if op == "guardian_undo" {
			undone++
			if params.(model.GuardianUndoRequest).Action != "act-2" {
				t.Fatal("undo target", params)
			}
		}
		if op == "guardian_status" {
			*result.(*model.GuardianStatus) = *guardianFixture()
		}
		return nil
	}
	press(m, "x")
	if m.guardianConfirm == nil || !strings.Contains(m.notice, "Undo deprioritize on Runaway.app?") {
		t.Fatal("x did not ask", m.notice)
	}
	press(m, "n")
	if m.guardianConfirm != nil || undone != 0 {
		t.Fatal("n sent or kept the undo")
	}
	press(m, "x")
	drain(t, m, press(m, "y"))
	if undone != 1 || !strings.Contains(m.notice, "Reverted deprioritize on Runaway.app") {
		t.Fatal("y did not undo", undone, m.notice)
	}
	// A non-reversible action has nothing to undo.
	press(m, "j")
	press(m, "x")
	if m.guardianConfirm != nil || !strings.Contains(m.notice, "nothing left to undo") {
		t.Fatal("notify undo", m.notice)
	}
	s.reply = func(op string, _ any, _ any) error {
		if op == "guardian_undo" {
			return errors.New("action already reverted")
		}
		return nil
	}
	press(m, "k")
	press(m, "x")
	drain(t, m, press(m, "y"))
	if !strings.Contains(m.notice, "Undo failed: action already reverted") {
		t.Fatal("failure notice", m.notice)
	}
}

func TestGuardianPageReportsOffUnknownAndUnavailable(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width, m.height = 100, 40
	m.view = viewGuardian
	s.reply = func(op string, _ any, _ any) error { return errors.New("unknown operation guardian_status") }
	drain(t, m, m.readGuardian(true))
	if text := ansi.Strip(m.View().Content); !strings.Contains(text, "Guardian status unavailable: unknown operation") {
		t.Fatal(text)
	}
	m.data.guardian = &model.GuardianStatus{Mode: model.GuardianOff, Role: model.GuardianManned}
	m.guardianErr = ""
	text := ansi.Strip(m.View().Content)
	if !strings.Contains(text, "UNKNOWN") || !strings.Contains(text, "No observations yet") || !strings.Contains(text, "MODE    off") || !strings.Contains(text, "observes nothing and acts on nothing") || !strings.Contains(text, "arm config set guardian.enabled true") || strings.Contains(text, "ALL CLEAR") || strings.Contains(text, "LADDER") {
		t.Fatal(text)
	}
	// The page polls only while visible and not more often than its period.
	m.guardianRead = m.timeNow
	if m.readGuardian(false) != nil {
		t.Fatal("polled before its period")
	}
	m.view = 0
	m.timeNow = m.timeNow.Add(time.Minute)
	if m.readGuardian(false) != nil {
		t.Fatal("polled while hidden")
	}
}

func TestMonitorLeavesGuardianToItsPage(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width, m.height = 130, 40
	m.data.guardian = guardianFixture()
	m.page.Latest = &model.MonitorSnapshot{Pressure: model.MonitorPressure{CPU: model.PressureReading{Level: model.PressureHigh}}}
	text := ansi.Strip(m.monitorText())
	for _, gone := range []string{"PRESSURE", "Guardian", "guardian"} {
		if strings.Contains(text, gone) {
			t.Fatalf("Monitor still shows %q:\n%s", gone, text)
		}
	}
	// Snapshots never read the guardian, and keep the page's reading.
	data, err := readSnapshot(m.ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range s.operations {
		if op == "guardian_status" {
			t.Fatal("snapshot read the guardian")
		}
	}
	kept := m.data.guardian
	m.applySnapshot(data)
	if m.data.guardian != kept {
		t.Fatal("snapshot dropped the guardian reading")
	}
}

func TestGuardianNavigationHiddenForRemoteNode(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width, m.height = 130, 40
	m.nodeUI = &nodeDashboard{}
	for _, v := range m.navViews() {
		if v == viewGuardian {
			t.Fatal("remote node lists Guardian")
		}
	}
}

// drain runs a command chain to completion, feeding results back into the model.
func drain(t *testing.T, m *dashboardModel, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 50 {
			t.Fatal("command chain did not settle")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := next().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case nil:
		default:
			_, follow := m.Update(msg)
			queue = append(queue, follow)
		}
	}
}

func TestGuardianReviewRegressions(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width, m.height = 130, 30
	m.data.guardian = guardianFixture()
	undone := 0
	s.reply = func(op string, _ any, result any) error {
		switch op {
		case "guardian_undo":
			undone++
		case "guardian_status":
			g := guardianFixture()
			if undone > 0 {
				now := time.Now()
				g.Actions[0].RevertedAt = &now
			}
			*result.(*model.GuardianStatus) = *g
		}
		return nil
	}

	// Clicking the Guardian tab from Mesh opens Guardian instead of Mesh's g.
	m.view = 6
	m.meshUI = &meshDashboard{}
	for _, r := range m.mouseRegions() {
		if r.key == "nav8" {
			m.acceptMouse(dashboardMouseMsg{frame: m.mouseFrame(), region: r}, m.mouseFrame())
		}
	}
	if m.view != viewGuardian {
		t.Fatal("Guardian tab from Mesh", m.view)
	}

	// An armed undo is dropped by any key path that does not confirm it,
	// including reconnection.
	press(m, "x")
	if m.guardianConfirm == nil {
		t.Fatal("not armed")
	}
	press(m, "?")
	if m.guardianConfirm != nil || undone != 0 || m.notice != "Guardian action kept unchanged." {
		t.Fatal("armed undo survived another key", m.notice)
	}
	press(m, "esc")
	press(m, "x")
	m.Update(connectedMsg{session: s, data: m.data})
	press(m, "y")
	if undone != 0 || m.record != nil || m.recovery != nil {
		t.Fatal("undo armed before reconnect was sent")
	}

	// x in the live feed never targets a guardian action.
	m.feedFocus = true
	m.selected = 0
	press(m, "x")
	if m.guardianConfirm != nil {
		t.Fatal("feed cursor armed an undo")
	}
	m.feedFocus = false

	// A reconnect retires an in-flight read and reads again at once.
	m.guardianLoading = true
	_, cmd := m.Update(connectedMsg{session: s, data: m.data})
	// The batch also waits on the event stream, which never ends here.
	for _, c := range cmd().(tea.BatchMsg) {
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			if msg != nil {
				m.Update(msg)
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if m.guardianLoading || m.data.guardian == nil {
		t.Fatal("read stuck or reading lost after reconnect")
	}

	// A reconnect never shows the previous backend's reading, even when the
	// new backend cannot report its guardian.
	m.view = 0
	m.Update(connectedMsg{session: s, data: m.data})
	if m.data.guardian != nil {
		t.Fatal("reconnect kept the previous backend's guardian")
	}
	m.view = viewGuardian
	s.reply = func(op string, _ any, _ any) error { return errors.New("unknown operation guardian_status") }
	drain(t, m, m.readGuardian(true))
	if text := ansi.Strip(m.View().Content); !strings.Contains(text, "Guardian status unavailable") || m.guardianConfirm != nil {
		t.Fatal("reconnected page shows an old reading", text)
	}
	press(m, "x")
	if m.guardianConfirm != nil {
		t.Fatal("undo armed with no reading")
	}
	m.data.guardian = guardianFixture()
	s.reply = func(op string, _ any, result any) error {
		switch op {
		case "guardian_undo":
			undone++
		case "guardian_status":
			g := guardianFixture()
			if undone > 0 {
				now := time.Now()
				g.Actions[0].RevertedAt = &now
			}
			*result.(*model.GuardianStatus) = *g
		}
		return nil
	}

	// A snapshot does not replace the visible page's reading.
	kept := m.data.guardian
	m.applySnapshot(snapshot{status: m.data.status})
	if m.data.guardian != kept {
		t.Fatal("snapshot replaced the guardian reading")
	}

	// Monitor selector click regions do not cover the Guardian page.
	if len(m.monitorSelectorRegions()) != 0 {
		t.Fatal("monitor regions on Guardian")
	}

	// j keeps the selected action visible.
	m.syncViewport()
	m.viewport.GotoBottom()
	bottom := m.viewport.YOffset()
	press(m, "j")
	if bottom > 0 && m.viewport.YOffset() == 0 {
		t.Fatal("j scrolled the selected action out of view")
	}

	// After an undo, the action shows nothing to undo until the next reading.
	m.selected = 0
	press(m, "x")
	drain(t, m, press(m, "y"))
	press(m, "x")
	if undone != 1 || m.guardianConfirm != nil {
		t.Fatal("reverted action could be undone again", undone)
	}
}
