package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/inferal-oss/arm/internal/model"
)

func TestReviewGaugeRetiredReceiptsCannotRenewHeldBudgets(t *testing.T) {
	for _, kind := range []string{"stream", "snapshot", "remote-detail"} {
		t.Run(kind, func(t *testing.T) {
			m := visualDashboard(t)
			available := 2.0
			m.data.status.CPU = &model.CPUBudget{Known: true, Observation: model.CPUAvailability{Known: true, Available: &available}, StaleAfter: 3 * time.Second}
			m.data.status.Disk = &model.DiskBudget{Known: true, StaleAfter: 3 * time.Second}
			held := time.Now().Add(-10 * time.Second)
			m.timeMonitor, m.lastRefresh = held, held
			selected := &nodeDashboard{connected: true, lastRead: held}
			m.nodeUI = selected
			retired := newTestTransport()
			switch kind {
			case "stream":
				m.Update(streamMsg{session: retired, page: &m.page})
			case "snapshot":
				m.Update(refreshedMsg{session: retired, data: m.data})
			case "remote-detail":
				m.Update(nodeReadMsg{ui: selected, detail: &model.Lease{ID: "detail-only"}})
			}
			if !m.timeMonitor.Equal(held) || !m.lastRefresh.Equal(held) || !selected.lastRead.Equal(held) {
				t.Fatal("unrelated or retired receipt renewed observation timestamps")
			}
			if !m.monitorStale() || memoryBudgetKnown(m.memoryBudgetForView()) || cpuBudgetKnown(m.cpuBudgetForView()) || diskBudgetKnown(m.diskBudgetForView()) {
				t.Fatal("clock advancement made expired observations available")
			}
		})
	}
}

func TestReviewGaugeClockCutPreservesOnlyCurrentRenderedClick(t *testing.T) {
	for _, name := range []string{"same-rendered-frame", "native-raw-hook", "intervening-render"} {
		intervening := name == "intervening-render"
		t.Run(name, func(t *testing.T) {
			m := visualDashboard(t)
			held := time.Now().Add(-10 * time.Second)
			m.timeNow, m.timeMonitor, m.lastRefresh = held, held, held
			m.syncViewport()
			var event tea.Msg
			var raw tea.MouseClickMsg
			for _, r := range m.mouseRegions() {
				if r.key == "d" && r.y < m.height-3 {
					raw = tea.MouseClickMsg{X: r.x, Y: r.y, Button: tea.MouseLeft}
					cmd := m.View().OnMouse(raw)
					if cmd == nil {
						t.Fatal("rendered selector did not produce a click")
					}
					event = cmd()
					break
				}
			}
			if event == nil {
				t.Fatal("fixture has no rendered chart selector")
			}
			if name != "same-rendered-frame" {
				// Bubble Tea dispatches the wrapped command asynchronously, then
				// calls Update with the raw event before consuming that command.
				m.Update(raw)
				if !m.timeNow.Equal(held) {
					t.Fatal("ignored raw event invalidated its own wrapped click")
				}
			}
			if intervening {
				m.Update(tickMsg(time.Now()))
			}
			m.Update(event)
			if intervening {
				if m.ioPicker != nil {
					t.Fatal("delayed click survived a changed rendered frame")
				}
			} else if m.ioPicker == nil || !m.ioPicker.disk {
				t.Fatal("clock cut discarded the current rendered target")
			}
			if !m.timeNow.After(held) || !m.monitorStale() || !m.timeMonitor.Equal(held) || !m.lastRefresh.Equal(held) {
				t.Fatal("click handling rolled back time or renewed expired readings")
			}
			if len(m.session.(*testTransport).operations) != 0 {
				t.Fatal("chart click issued a backend mutation")
			}
		})
	}
}
