package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestActivityPreferenceDefaultsAndLegacyOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, raw             string
		leases, runs, invalid bool
	}{
		{"missing", "", true, true, false},
		{"old opt out", `{"version":1,"hide_inactive_leases":false}`, false, true, false},
		{"both opt out", `{"version":1,"hide_inactive_leases":false,"hide_inactive_runs":false}`, false, false, false},
		{"independent", `{"version":1,"hide_inactive_runs":false}`, true, false, false},
		{"malformed", `{"version":1,"hide_inactive_leases":false,`, true, true, true},
		{"unsafe extra", `{"version":1,"hide_inactive_runs":false,"unexpected":true}`, true, true, true},
		{"invalid type", `{"version":1,"hide_inactive_leases":"false"}`, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Chmod(home, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.raw != "" {
				if err := os.WriteFile(filepath.Join(home, preferenceFile), []byte(tc.raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			p, err := readPreferences(home)
			if (err != nil) != tc.invalid || p.HideInactive != tc.leases || p.HideInactiveRuns != tc.runs {
				t.Fatal(p, err)
			}
		})
	}
	home := t.TempDir()
	os.Chmod(home, 0755)
	p, err := readPreferences(home)
	if err == nil || !p.HideInactive || !p.HideInactiveRuns {
		t.Fatal("unsafe settings changed defaults", p, err)
	}
}
func TestActivityFiltersKeyboardMouseAndIndependentPersistence(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width, m.height = 140, 40
	m.view = 7
	if !m.hideInactive || !m.hideInactiveRuns {
		t.Fatal("history visible by default")
	}
	m.runsUI = &runsDashboard{}
	for _, state := range []string{"active", "queued", "provisioning", "ending", "reclaiming", "needs_attention", "future_state", "interrupted", ""} {
		m.runsUI.records = append(m.runsUI.records, model.Run{ID: state, State: state, Reason: "Visible " + state})
	}
	m.runsUI.records = append(m.runsUI.records, model.Run{ID: "lost", State: "terminal", Outcome: "reporter_lost", Reason: "Uncertain"}, model.Run{ID: "done", State: "terminal", Outcome: "completed", Reason: "History"})
	if got := len(m.filteredRuns()); got != 10 {
		t.Fatal("unresolved work hidden", got)
	}
	if !strings.Contains(ansi.Strip(m.runsText()), "[x] Hide inactive runs") || strings.Contains(m.runsText(), "History") {
		t.Fatal(m.runsText())
	}
	m.selected = 0
	id := m.selectionID()
	press(m, "h")
	if m.hideInactiveRuns || len(m.filteredRuns()) != 11 || m.selectionID() != id {
		t.Fatal("run toggle lost identity")
	}
	p, err := readPreferences(m.home)
	if err != nil || !p.HideInactive || p.HideInactiveRuns {
		t.Fatal(p, err)
	}
	m.view = 2
	press(m, "h")
	p, err = readPreferences(m.home)
	if err != nil || p.HideInactive || p.HideInactiveRuns {
		t.Fatal("lease toggle reset runs", p, err)
	}
	m.view = 7
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "h" })
	p, err = readPreferences(m.home)
	if err != nil || p.HideInactive || !p.HideInactiveRuns {
		t.Fatal("mouse toggle reset leases", p, err)
	}
	restored := newDashboard(t.Context(), m.home)
	if restored.hideInactive || !restored.hideInactiveRuns {
		t.Fatal("preferences not restored")
	}
	// A retained historical transcript remains selected and paused even when hidden in the roster.
	done := m.runsUI.records[len(m.runsUI.records)-1]
	m.runsUI.selected = &done
	m.runsUI.data = []byte("saved output")
	m.runsUI.offset = 12
	m.runsUI.following = false
	m.runsUI.folded = true
	generation := m.runsUI.generation
	press(m, "h")
	if m.runsUI.selected != &done || m.runsUI.following || !m.runsUI.folded || m.runsUI.offset != 12 || m.runsUI.generation != generation {
		t.Fatal("history visibility altered selected reader")
	}
}
