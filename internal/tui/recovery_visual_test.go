package tui

import (
	"github.com/charmbracelet/x/ansi"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRecoveryPreviews(t *testing.T) {
	dir := os.Getenv("ARM_TUI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("optional synthetic visual artifact")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute preview directory required")
	}
	os.MkdirAll(dir, 0700)
	for _, name := range []string{"recovery-roster", "recovery-compact", "recovery-storage-error"} {
		m := visualDashboard(t)
		m.view = 1
		message := pendingFixture(t, "message")
		pool := pendingFixture(t, "pool")
		m.recovery = &recoveryBrowser{records: []pendingRecord{pool, message}}
		if name == "recovery-compact" {
			m.width, m.height = 70, 24
		}
		if name == "recovery-storage-error" {
			m.recovery.err = "Pending directory permissions changed. Restore private same-user access, then Refresh; no retained file was removed."
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
