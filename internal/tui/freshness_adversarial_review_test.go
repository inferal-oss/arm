package tui

import (
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
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
