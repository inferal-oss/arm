package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
)

func cpuPresentationFixture(m *dashboardModel, capacity, reserved, available float64) {
	now := time.Now()
	m.timeNow = now
	m.lastRefresh = now
	m.data.status.CPU = &model.CPUBudget{Known: true, StaleAfter: 4 * time.Second, Capacity: capacity, Reserved: reserved, Available: available, Observation: model.CPUAvailability{Known: true, At: now, Available: &capacity}}
}

func TestLiveCPUHeadroomPreservesPromisesAndSeparatesUnknownFromZero(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 1
	m.data.status.Resources = []model.ResourceView{{ID: "cpu", Kind: "cpu", Host: "local-host", Unit: "logical-core-equivalents", BudgetTotal: "64", Reserved: "1.25", Available: "62.75"}}
	cpuPresentationFixture(m, 2.5, 1.25, 1.25)
	ceiling := 8.0
	m.data.status.CPU.Ceiling = &ceiling
	text := ansi.Strip(m.resourcesText())
	for _, want := range []string{"LIVE HEADROOM", "2.5 cores", "1.25 cores", "Shortfall", "0 cores", "Operator ceiling", "full promises stay charged"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	if strings.Contains(text, "64 cores") || strings.Contains(text, "62.75 cores") {
		t.Fatal("static core count replaced live headroom", text)
	}
	m.resourceID = "cpu"
	for _, kind := range []string{"unknown", "stale", "aged", "disconnected"} {
		t.Run(kind, func(t *testing.T) {
			cpuPresentationFixture(m, 2.5, 1.25, 1.25)
			m.connected = true
			switch kind {
			case "unknown":
				m.data.status.CPU.Known = false
			case "stale":
				m.data.status.CPU.Stale = true
			case "aged":
				m.timeNow = m.lastRefresh.Add(5 * time.Second)
			case "disconnected":
				m.connected = false
			}
			text := ansi.Strip(m.resourceDetailText())
			for _, want := range []string{"Live headroom: unavailable", "Promised: 1.25 cores", "To lease: unavailable", "Shortfall: unavailable", "new CPU grants wait"} {
				if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
					t.Fatal(want, text)
				}
			}
		})
	}
	m.connected = true
	cpuPresentationFixture(m, 0, 1.25, 0)
	m.data.status.CPU.Deficit = 1.25
	text = ansi.Strip(m.resourceDetailText())
	if !strings.Contains(text, "Live headroom: 0 cores") || !strings.Contains(text, "Shortfall: 1.25 cores") || strings.Contains(text, "To lease: unavailable") {
		t.Fatal("known busy zero became unknown", text)
	}
}

func TestRemoteCPUFreshnessUsesOnlyDestinationBudget(t *testing.T) {
	m, _ := fixtureDashboard(t)
	cpuPresentationFixture(m, 64, 0, 64)
	now := time.Now()
	ui := &nodeDashboard{connected: true, lastRead: now, summary: nodeSummary{CPU: json.RawMessage(`{"known":true,"stale":false,"observed_available_cores":2.5,"capacity_cores":2.5,"reserved_cores":1.25,"available_cores":1.25,"deficit_cores":0,"age_ns":1000000000,"stale_after_ns":4000000000}`)}}
	if nodeBudgetUnavailable(ui, "cpu", now) {
		t.Fatal("fresh destination budget rejected")
	}
	if !nodeBudgetUnavailable(ui, "cpu", now.Add(4*time.Second)) {
		t.Fatal("cached destination reading stayed live")
	}
	valid := append(json.RawMessage(nil), ui.summary.CPU...)
	for _, field := range []string{"observed_available_cores", "capacity_cores", "reserved_cores", "available_cores", "deficit_cores"} {
		var budget map[string]any
		if err := json.Unmarshal(valid, &budget); err != nil {
			t.Fatal(err)
		}
		delete(budget, field)
		ui.summary.CPU, _ = json.Marshal(budget)
		if !nodeBudgetUnavailable(ui, "cpu", now) {
			t.Fatal("missing destination field accepted", field)
		}
	}
	ui.summary.CPU = nil
	if !nodeBudgetUnavailable(ui, "cpu", now) {
		t.Fatal("local CPU budget substituted for missing destination")
	}
}

func TestCPUDisplayPrecision(t *testing.T) {
	for _, row := range []struct{ raw, want string }{{"7.03584", "7.04 cores"}, {"18", "18 cores"}, {"0.25", "0.25 cores"}, {"0.0004", "<0.01 cores"}, {"0", "0 cores"}} {
		if got := cpuQuantity(json.Number(row.raw)); got != row.want {
			t.Fatal(row, got)
		}
	}
}
