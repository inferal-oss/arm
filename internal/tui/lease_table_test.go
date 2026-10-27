package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func TestLeaseTableResponsiveColumnsAndFullDetails(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 2
	first, second := activeLease("first-exact-lease-id"), activeLease("second-exact-lease-id")
	first.Request.Reason = strings.Repeat("A complete purpose with spaces. ", 8)
	first.Request.Resources = model.Resources{CPU: 4, Memory: 4 << 30, Disk: 8 << 30}
	second.State, second.Outcome = "terminal", "expired"
	m.data.status.Leases = []model.Lease{first, second}
	for _, width := range []int{45, 70, 140} {
		m.width = width
		m.syncViewport()
		text := ansi.Strip(m.leasesText())
		for _, want := range []string{"Lease / Owner", "Status", "alice", "UID 501", "Expired"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d lost %q: %s", width, want, text)
			}
		}
		if strings.Contains(text, "Claims") != (width == 140) || strings.Contains(text, "Reason") != (width >= 70) {
			t.Fatalf("columns do not adapt to width %d: %s", width, text)
		}
		m.detail = &first
		full := ansi.Strip(m.leaseDetailText())
		for _, want := range []string{first.ID, first.Request.Reason, "4 cores", "4 GiB", "8 GiB"} {
			if !strings.Contains(full, want) {
				t.Fatalf("width %d lost full detail %q", width, want)
			}
		}
		m.detail = nil
	}
}

func TestLeaseTableRefreshAndHistoryToggleKeepSelectedIdentity(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 2
	first, selected, ended := activeLease("first"), activeLease("selected"), activeLease("ended")
	ended.State, ended.Outcome = "terminal", "completed"
	m.data.status.Leases = []model.Lease{first, selected, ended}
	m.selected = 1
	updated := m.data
	updated.status.Leases = []model.Lease{activeLease("inserted"), ended, first, selected}
	m.applySnapshot(updated)
	if m.selectionID() != selected.ID {
		t.Fatal("refresh selected a different lease")
	}
	press(m, "h")
	if m.selectionID() != selected.ID || len(m.filteredLeases()) != 3 {
		t.Fatal("history toggle changed selection or hid active work")
	}
	m.filter = "selected"
	m.restoreSelection(selected.ID, m.selected)
	if got := m.filteredLeases(); len(got) != 1 || got[0].ID != selected.ID {
		t.Fatal("table filter lost the selected lease")
	}
}
