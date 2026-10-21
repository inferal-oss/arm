package tui

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
	"os"
	"path/filepath"
	"testing"
)

// Optional review artifacts contain synthetic pool/owner data only.
func TestWritePoolDashboardPreviews(t *testing.T) {
	dir := os.Getenv("ARM_TUI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set ARM_TUI_PREVIEW_DIR for pool previews")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute preview path required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pool-table", "pool-detail", "pool-review", "pool-journal", "pool-compact"} {
		m := visualDashboard(t)
		m.view = 1
		v := poolFixture()
		v.Pool.Authority = m.data.status.Host
		v.Review.Authority = m.data.status.Host
		m.poolUI = &poolDashboard{mode: "list", views: []model.PoolView{v}, current: &v}
		switch name {
		case "pool-detail":
			m.poolUI.mode = "detail"
		case "pool-review":
			p, err := preparePoolPlan(poolDraft{operation: "set", value: "0", reason: "Temporary capacity reduction; retain active builds"}, &v, m.data.status.Host)
			if err != nil {
				t.Fatal(err)
			}
			m.poolUI.review = p
		case "pool-journal":
			m.poolUI.mode = "history"
			m.poolUI.history = model.PoolHistory{Entries: []model.PoolChange{poolChangeFor(poolPlanFixture(t).request)}}
		case "pool-compact":
			m.width = 70
			m.height = 24
		}
		m.syncViewport()
		raw := m.View().Content
		for ext, content := range map[string]string{"ansi": raw, "txt": ansi.Strip(raw)} {
			if err := os.WriteFile(filepath.Join(dir, name+"."+ext), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
