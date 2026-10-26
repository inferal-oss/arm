package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
)

// A native collection/refresh may arrive between render ticks. In particular,
// the receipt must never be newer than the clock used to render that receipt.
func TestGaugeReceiptBetweenTicks(t *testing.T) {
	for _, kind := range []string{"connect", "telemetry", "snapshot", "remote"} {
		t.Run(kind, func(t *testing.T) {
			m := visualDashboard(t)
			available := 2.0
			m.data.status.CPU = &model.CPUBudget{Known: true, Observation: model.CPUAvailability{Known: true, Available: &available}, Capacity: 2, Available: 2, StaleAfter: 3 * time.Second}
			m.data.status.Disk = &model.DiskBudget{Known: true, Capacity: 1024, Available: 1024, StaleAfter: 3 * time.Second}
			session := m.session
			m.timeNow = time.Now().Add(-time.Second)
			switch kind {
			case "connect":
				m.Update(connectedMsg{session: session, data: m.data, page: m.page})
			case "telemetry":
				m.Update(streamMsg{session: session, page: &m.page})
			case "snapshot":
				m.Update(refreshedMsg{session: session, data: m.data})
			case "remote":
				ui := &nodeDashboard{connected: true}
				m.nodeUI = ui
				m.Update(nodeReadMsg{ui: ui, summary: nodeSummary{Host: "selected-peer"}})
				if m.timeNow.Before(ui.lastRead) {
					t.Fatal("remote receipt is newer than its render clock")
				}
				return
			}
			if kind != "snapshot" {
				text := ansi.Strip(m.memoryWindow(m.page.Latest, 90) + m.storageWindow(m.page.Latest, 90))
				if strings.Contains(text, "unavailable") || strings.Contains(text, "STALE") {
					t.Fatal("fresh monitor receipt flickered before next tick", text)
				}
			}
			if kind != "telemetry" && (!memoryBudgetKnown(m.memoryBudgetForView()) || !cpuBudgetKnown(m.cpuBudgetForView()) || !diskBudgetKnown(m.diskBudgetForView())) {
				t.Fatal("fresh budget receipt flickered before next tick", m.memoryBudgetForView())
			}
		})
	}
}

func TestQueuedTickCannotRollReceiptClockBack(t *testing.T) {
	m := visualDashboard(t)
	oldTick := time.Now().Add(-time.Second)
	m.Update(streamMsg{session: m.session, page: &m.page})
	receipt := m.timeMonitor
	m.Update(tickMsg(oldTick))
	if m.timeNow.Before(receipt) || m.monitorStale() {
		t.Fatal("queued timer timestamp rolled the render clock behind a receipt")
	}
}

func TestGaugeFreshnessStillFailsClosed(t *testing.T) {
	for _, state := range []string{"deadline", "future-receipt", "negative-age", "failed", "missing", "disconnected"} {
		t.Run(state, func(t *testing.T) {
			m := visualDashboard(t)
			session := m.session
			m.Update(connectedMsg{session: session, data: m.data, page: m.page})
			switch state {
			case "deadline":
				m.timeNow = m.timeMonitor.Add(m.page.StaleAfter - m.page.Age)
				m.data.status.Memory.StaleAfter = m.page.StaleAfter
			case "future-receipt":
				m.timeNow = m.timeMonitor.Add(-time.Nanosecond)
			case "negative-age":
				m.page.Age = -1
				m.data.status.Memory.Age = -1
			case "failed":
				m.page.Stale = true
				m.data.status.Memory.Stale = true
			case "missing":
				m.page.Latest.Memory.Available.Value = nil
				m.data.status.Memory.Observation.Available = nil
			case "disconnected":
				m.Update(streamMsg{session: session, err: errors.New("lost")})
			}
			if !strings.Contains(ansi.Strip(m.memoryWindow(m.page.Latest, 90)), "unavailable RAM remaining") || memoryBudgetKnown(m.memoryBudgetForView()) {
				t.Fatal("invalid headroom remained available", state)
			}
			// Replacing the observation recovers, without extending the old deadline.
			fresh := visualDashboard(t)
			m.Update(connectedMsg{session: newTestTransport(), data: fresh.data, page: fresh.page})
			if m.monitorStale() || !memoryBudgetKnown(m.memoryBudgetForView()) {
				t.Fatal("fresh successor did not recover", state)
			}
			before := m.page
			old := model.MonitorPage{Epoch: "retired-host", Latest: &model.MonitorSnapshot{}}
			m.Update(streamMsg{session: session, page: &old})
			m.Update(refreshedMsg{session: session, data: snapshot{status: model.Status{Host: "retired-host"}}})
			if m.page.Epoch != before.Epoch || m.data.status.Host == "retired-host" {
				t.Fatal("retired session replaced successor readings")
			}
		})
	}
}

func TestRetiredRemoteGaugeReadCannotReplaceSelectedHost(t *testing.T) {
	m := visualDashboard(t)
	retired := &nodeDashboard{connected: true, summary: nodeSummary{Host: "retired-peer"}}
	selected := &nodeDashboard{connected: true, summary: nodeSummary{Host: "selected-peer"}}
	m.nodeUI = selected
	m.Update(nodeReadMsg{ui: retired, summary: nodeSummary{Host: "retired-peer"}})
	if m.nodeUI != selected || selected.summary.Host != "selected-peer" || !selected.lastRead.IsZero() {
		t.Fatal("retired host completion refreshed the selected authority")
	}
	m.nodeUI = nil // Closing the remote context also retires pending reads.
	m.Update(nodeReadMsg{ui: selected, summary: nodeSummary{Host: "selected-peer"}})
	if m.nodeUI != nil {
		t.Fatal("closed remote context was restored by a late reading")
	}
}

func TestUnrelatedUpdateDoesNotRefreshGaugeDeadlines(t *testing.T) {
	m := visualDashboard(t)
	held := time.Now().Add(-20 * time.Second)
	m.timeMonitor, m.lastRefresh = held, held
	m.Update(streamMsg{session: m.session, event: &model.Event{Sequence: 999, Type: "unrelated"}})
	if !m.timeMonitor.Equal(held) || !m.lastRefresh.Equal(held) || !m.monitorStale() || memoryBudgetKnown(m.memoryBudgetForView()) {
		t.Fatal("render clock update extended a held observation's lifetime")
	}
}
