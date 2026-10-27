package tui

import (
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestReviewGaugeCombinedMemoryUsageRemainsUnknown(t *testing.T) {
	m := visualDashboard(t)
	const gib = int64(1024 * 1024 * 1024)
	total, headroom := 8*gib, 2*gib
	scope := "host+complete-memory-cgroup-ancestry"
	measure := func(value int64) model.Measurement {
		n := float64(value)
		return model.Measurement{Value: &n, Unit: "bytes", Scope: scope, Quality: "reported"}
	}
	m.page.Latest.Memory = model.ResourceMeasurement{
		Total: measure(total), Available: measure(headroom),
		Used: model.Measurement{Unit: "bytes", Scope: scope, Quality: "unavailable"},
	}
	m.data.status.Memory = &model.MemoryBudget{
		Observation: model.MemoryAvailability{Total: &total, Available: &headroom, Scope: scope},
		Capacity:    headroom, Reserved: gib, Available: gib, StaleAfter: 3 * time.Second,
	}
	m.Update(connectedMsg{session: m.session, data: m.data, page: m.page})
	ram := ansi.Strip(m.memoryWindow(m.page.Latest, 90))
	if !strings.Contains(ram, "2 GiB RAM remaining") || strings.Contains(ram, "unavailable RAM remaining") || !strings.Contains(ram, "RAM used") || !strings.Contains(ram, "unavailable") {
		t.Fatal("unknown combined resident usage hid measured headroom", ram)
	}
	if m.page.Latest.Memory.Used.Value != nil || !memoryBudgetKnown(m.memoryBudgetForView()) {
		t.Fatal("UI fabricated resident usage or lost a valid availability budget")
	}
	_, _, available, _ := m.resourceCapacityTexts(&model.ResourceView{ID: "memory", Kind: "memory", Unit: "bytes"})
	if available != "1 GiB" {
		t.Fatal("to-lease capacity did not retain the backend budget", available)
	}
}
