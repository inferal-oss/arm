package tui

import (
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestMonitorRemainingMeasurementsAndAvailableRAMHistory(t *testing.T) {
	m := visualDashboard(t)
	s := m.page.Latest
	ram := ansi.Strip(m.memoryWindow(s, 90))
	disk := ansi.Strip(m.storageWindow(s, 90))
	for _, want := range []string{"RAM remaining", "Swap remaining", "RAM used", "RAM total", "19.2 GiB", "3.5 GiB"} {
		if !strings.Contains(ram, want) {
			t.Fatal(want, ram)
		}
	}
	for _, want := range []string{"space remaining", "Space used", "Space total", "600 GiB", "user-available space"} {
		if !strings.Contains(disk, want) {
			t.Fatal(want, disk)
		}
	}
	for i := range m.history {
		m.history[i].MemoryAvailable = nil
	}
	ram = ansi.Strip(m.memoryWindow(s, 90))
	if !strings.Contains(ram, "History 60s "+strings.Repeat("·", 60)) {
		t.Fatal("available RAM chart used the used-RAM series", ram)
	}
	s.Memory.Available.Value = nil
	s.Swap.Available.Value = nil
	s.Filesystem.Available.Value = nil
	ram = ansi.Strip(m.memoryWindow(s, 90))
	disk = ansi.Strip(m.storageWindow(s, 90))
	if !strings.Contains(ram, "unavailable RAM remaining") || !strings.Contains(ram, "Swap remaining") || !strings.Contains(ram, "unavailable / 4 GiB") || !strings.Contains(disk, "unavailable space remaining") {
		t.Fatal("unknown remaining values became fabricated zero", ram, disk)
	}
}

func TestMonitorRemainingFreshnessAndKnownZero(t *testing.T) {
	for _, state := range []string{"stale", "aged", "disconnected"} {
		t.Run(state, func(t *testing.T) {
			m := visualDashboard(t)
			s := m.page.Latest
			switch state {
			case "stale":
				m.page.Stale = true
			case "aged":
				m.timeNow = m.timeMonitor.Add(4 * time.Second)
			case "disconnected":
				m.connected = false
			}
			ram, disk := ansi.Strip(m.memoryWindow(s, 90)), ansi.Strip(m.storageWindow(s, 90))
			for _, got := range []string{ram, disk} {
				if !strings.Contains(got, "STALE") || !strings.Contains(got, "unavailable") || strings.Contains(got, "19.2 GiB RAM remaining") || strings.Contains(got, "600 GiB space remaining") {
					t.Fatal(state, got)
				}
			}
			if !strings.Contains(ansi.Strip(m.cpuWindow(s, 90)), "busy") || !strings.Contains(ansi.Strip(m.ioWindow(s.DiskIO, true, 90)), "Read") {
				t.Fatal("CPU activity or I/O throughput semantics changed")
			}
		})
	}
	m := visualDashboard(t)
	zero := 0.0
	m.page.Latest.Memory.Available.Value = &zero
	m.page.Latest.Swap.Available.Value = &zero
	m.page.Latest.Filesystem.Available.Value = &zero
	ram, disk := ansi.Strip(m.memoryWindow(m.page.Latest, 90)), ansi.Strip(m.storageWindow(m.page.Latest, 90))
	if !strings.Contains(ram, "0 B RAM remaining") || !strings.Contains(ram, "0 B / 4 GiB") || !strings.Contains(disk, "0 B space remaining") {
		t.Fatal("known zero rendered as unknown", ram, disk)
	}
}

func TestResourceCardsUseAuthoritativeBudgetsAndAgeLocally(t *testing.T) {
	m := visualDashboard(t)
	m.view = 1
	m.data.status.Resources = append(m.data.status.Resources, model.ResourceView{ID: "disk", Kind: "disk", Unit: "bytes", BudgetTotal: "1099511627776", Reserved: "1073741824", Available: "1098437885952"})
	m.data.status.Disk = &model.DiskBudget{Known: true, StaleAfter: 3 * time.Second, Capacity: 6 * 1024 * 1024 * 1024, Reserved: 1024 * 1024 * 1024, Available: 5 * 1024 * 1024 * 1024}
	m.data.status.Memory.Capacity = 4 * 1024 * 1024 * 1024
	m.data.status.Memory.Reserved = 8 * 1024 * 1024 * 1024
	m.data.status.Memory.Available = 0
	m.data.status.Memory.Deficit = 4 * 1024 * 1024 * 1024
	cards := ansi.Strip(m.resourcesText())
	if strings.Count(cards, "To lease") != 3 || !strings.Contains(cards, "8 GiB") || !strings.Contains(cards, "5 GiB") {
		t.Fatal("resource cards did not show authoritative budget availability and promises", cards)
	}
	for _, id := range []string{"memory", "disk"} {
		m.resourceID = id
		text := ansi.Strip(m.resourceDetailText())
		if strings.Index(text, "To lease:") > strings.Index(text, "Live pool:") || strings.Index(text, "Live pool:") > strings.Index(text, "Promised:") {
			t.Fatal("remaining capacity did not lead resource detail", text)
		}
		if id == "memory" && (!strings.Contains(text, "To lease: 0 B") || !strings.Contains(text, "Promised: 8 GiB") || !strings.Contains(text, "Shortfall: 4 GiB")) {
			t.Fatal("known zero/shortfall missing", text)
		}
		if id == "disk" && (!strings.Contains(text, "To lease: 5 GiB") || strings.Contains(text, "1023 GiB")) {
			t.Fatal("static disk resource displaced backend budget", text)
		}
	}
	for _, state := range []string{"aged", "disconnected"} {
		t.Run(state, func(t *testing.T) {
			m.connected = true
			m.timeNow = m.lastRefresh
			if state == "aged" {
				m.timeNow = m.lastRefresh.Add(11 * time.Second)
			} else {
				m.connected = false
			}
			for _, id := range []string{"memory", "disk"} {
				m.resourceID = id
				text := ansi.Strip(m.resourceDetailText())
				if !strings.Contains(text, "To lease: unavailable") || !strings.Contains(text, "Live pool: unavailable") || !strings.Contains(text, "Shortfall: unavailable") || !strings.Contains(text, "Promised:") {
					t.Fatal("stale headroom or promises misrepresented", state, text)
				}
			}
		})
	}
}
