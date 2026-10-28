package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestRunsCompactStatusKeepsCleanupAndReporterLossHonest(t *testing.T) {
	for _, state := range []string{"queued", "provisioning", "ending", "reclaiming", "needs_attention"} {
		run := model.Run{State: state, Outcome: "expired"}
		if !activeRunRecord(run) || runStatusLabel(run) == "Expired" {
			t.Fatal("unfinished cleanup presented as expired", state)
		}
	}
	if got := runStatusLabel(model.Run{State: "ending", Outcome: "cancelled"}); got != "Stopping" {
		t.Fatal(got)
	}
	if got := runStatusLabel(model.Run{State: "interrupted", Outcome: "reporter_lost"}); got != "Reporter lost" {
		t.Fatal(got)
	}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Minute)
	if got := runListTime(model.Run{State: "active", Created: start}, now); got != "2m elapsed" {
		t.Fatal(got)
	}
	if got := runListTime(model.Run{State: "terminal", Ended: &start}, now); got != "2m ago" {
		t.Fatal(got)
	}
}
func TestRunsCompactListBoundsOrderingAndMouseRows(t *testing.T) {
	for _, width := range []int{45, 70, 140} {
		m, _, _ := runFixture(t, nil)
		m.width, m.height = width, 32
		m.view = 7
		now := m.timeNow
		m.runsUI = &runsDashboard{records: []model.Run{
			{ID: "private-id-past", Name: "four-word-generated-name", State: "terminal", Created: now, Command: []string{"/private/tools/go", "test", "./..."}, Reason: "Run unit tests", Agent: "agent"},
			{ID: "private-id-active", Name: "four-word-generated-name-active", State: "active", Created: now.Add(-time.Hour), Command: []string{"/private/tools/compiler", strings.Repeat("/long/path", 40) + "/file"}, Reason: "Compile " + strings.Repeat("unbroken", 30), Agent: strings.Repeat("agent", 30)},
		}}
		render := m.runListRender()
		text := ansi.Strip(render.text)
		for _, line := range strings.Split(text, "\n") {
			if ansi.StringWidth(line) > m.mainWidth()-4 {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
		if strings.Contains(text, "private-id") || strings.Contains(text, "four-word") || strings.Contains(text, "/private/tools") {
			t.Fatal("technical identity leaked into scan list", text)
		}
		if m.filteredRuns()[0].ID != "private-id-active" {
			t.Fatal("active run buried behind recent history")
		}
		m.syncViewport()
		m.viewport.GotoTop()
		for _, region := range m.mouseRegions() {
			if region.row == 0 && !region.feed && !region.record {
				if region.height != 2 {
					t.Fatal("run row mouse target is not two lines", region)
				}
				break
			}
		}
		m.filter = "private-id-past"
		if got := m.filteredRuns(); len(got) != 1 || got[0].ID != "private-id-past" {
			t.Fatal("full identity search lost")
		}
	}
}
func TestRunsCompactKeyboardSelectionAndRefreshKeepExactRows(t *testing.T) {
	m, s, _ := runFixture(t, nil)
	m.width, m.height = 70, 14
	m.view = 7
	m.runsUI = &runsDashboard{}
	for i := 0; i < 20; i++ {
		m.runsUI.records = append(m.runsUI.records, model.Run{ID: fmt.Sprintf("run-%02d", i), State: "terminal", Reason: fmt.Sprintf("Test %02d", i), Created: m.timeNow.Add(-time.Duration(i) * time.Minute)})
	}
	for i := 0; i < 12; i++ {
		press(m, "j")
		row := m.runListSelectionRow()
		if row < m.viewport.YOffset() || row+1 >= m.viewport.YOffset()+m.viewport.Height() {
			t.Fatal("keyboard selection second line is hidden", i, row, m.viewport.YOffset(), m.viewport.Height())
		}
	}
	id := m.selectionID()
	anchorBefore := m.runListSelectionRow() - m.viewport.YOffset()
	records := append([]model.Run(nil), m.runsUI.records...)
	records[0].State = "active"
	records[19].State = "active"
	m.acceptRunsRead(runsReadMsg{ui: m.runsUI, session: s, generation: m.runsUI.generation, records: records, hasInventory: true})
	if m.runListSelectionRow()-m.viewport.YOffset() != anchorBefore {
		t.Fatal("refresh moved the selected row within the viewport")
	}
	if m.selectionID() != id {
		t.Fatal("refresh/reordering changed selected identity", id, m.selectionID())
	}
	render := m.runListRender()
	selectedRow := -1
	for row, index := range render.rows {
		if index == m.selected {
			selectedRow = row
		}
	}
	if selectedRow < 0 {
		t.Fatal("selected row lacks matching render map")
	}
}

func TestRunsCaptureFailureAndUnknownReporterStayVisible(t *testing.T) {
	zero := 0
	if got := runStatusLabel(model.Run{State: "terminal", Outcome: "log_failed", ExitCode: &zero}); got != "Log failed" {
		t.Fatal(got)
	}
	m, _, _ := runFixture(t, nil)
	m.view = 7
	m.runsUI = &runsDashboard{records: []model.Run{{ID: "done", State: "terminal", Created: m.timeNow}, {ID: "lost", State: "interrupted", Outcome: "reporter_lost", Created: m.timeNow.Add(-time.Hour)}}}
	if m.filteredRuns()[0].ID != "lost" {
		t.Fatal("unknown reporter buried behind completed history")
	}
	text := ansi.Strip(m.runListRender().text)
	if !strings.Contains(text, "CURRENT") || !strings.Contains(text, "Reporter lost") {
		t.Fatal("uncertain current work not exposed", text)
	}
}

func TestRunsCompactSearchMatchesDisplayedFailure(t *testing.T) {
	zero := 0
	m, _, _ := runFixture(t, nil)
	m.view = 7
	m.runsUI = &runsDashboard{records: []model.Run{{ID: "failure", State: "terminal", Outcome: "log_failed", ExitCode: &zero, Reason: "Compile"}}}
	for _, query := range []string{"Log failed", "log_failed"} {
		m.filter = query
		if got := m.filteredRuns(); len(got) != 1 || got[0].ID != "failure" {
			t.Fatal("displayed status/outcome search missed run", query)
		}
	}
}

func TestRunsRosterAnchorsExcludeAssociatedLeaseAndOverlays(t *testing.T) {
	m, s, _ := runFixture(t, nil)
	m.view = 7
	m.runsUI = &runsDashboard{records: []model.Run{{ID: "run", State: "active"}}}
	lease := activeLease("associated")
	lease.Request.Reason = strings.Repeat("A long lease detail ", 80)
	m.detail = &lease
	m.selected = 7
	m.syncViewport()
	m.viewport.SetYOffset(3)
	before := m.viewport.YOffset()
	if m.runRosterVisible() {
		t.Fatal("associated lease detail mistaken for run roster")
	}
	m.keepSelectionVisible()
	if m.viewport.YOffset() != before {
		t.Fatal("roster selection scrolled associated lease")
	}
	m.acceptRunsRead(runsReadMsg{ui: m.runsUI, session: s, generation: m.runsUI.generation, hasInventory: true, records: []model.Run{{ID: "other", State: "terminal"}}})
	if m.selected != 7 || m.viewport.YOffset() != before {
		t.Fatal("background roster changed lease selection or scroll")
	}
	for _, region := range m.mouseRegions() {
		if region.row >= 0 && !region.feed && !region.record {
			t.Fatal("run roster mouse row leaked into lease detail", region)
		}
	}
	m.detail = nil
	m.recovery = &recoveryBrowser{}
	if m.runRosterVisible() {
		t.Fatal("recovery mistaken for roster")
	}
	m.recovery = nil
	m.record = &recordTree{}
	if m.runRosterVisible() {
		t.Fatal("record mistaken for roster")
	}
	if got := runStatusLabel(model.Run{State: "terminal", Outcome: "rejected"}); got != "Rejected" {
		t.Fatal(got)
	}
}
