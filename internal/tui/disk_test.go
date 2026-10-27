package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func TestLiveDiskLabelsKeepUnknownAndOutstandingPromisesDistinct(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 1
	m.data.status.Resources = []model.ResourceView{{ID: "disk", Kind: "disk", Unit: "bytes", BudgetTotal: "1048576", Reserved: "524288", Available: "524288"}}
	now := time.Now()
	m.timeNow, m.lastRefresh = now, now
	ceiling := int64(2097152)
	m.data.status.Disk = &model.DiskBudget{Known: true, Ceiling: &ceiling, Capacity: 1048576, Reserved: 524288, Available: 524288, StaleAfter: 4 * time.Second}
	text := ansi.Strip(m.resourcesText())
	for _, want := range []string{"LIVE HEADROOM", "To lease", "512 KiB", "Operator ceiling", "full promises remain charged"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	m.data.status.Disk.Known = false
	text = ansi.Strip(m.resourcesText())
	if strings.Count(text, "unavailable") != 3 || !strings.Contains(text, "512 KiB") {
		t.Fatal("unknown reading hid actual promises or became zero", text)
	}
	m.resourceID = "disk"
	text = ansi.Strip(m.resourceDetailText())
	if !strings.Contains(text, "Promised: 512 KiB") || !strings.Contains(text, "To lease: unavailable") {
		t.Fatal(text)
	}
}
