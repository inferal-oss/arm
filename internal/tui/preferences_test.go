package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func TestLeaseOutcomeLabelsPreserveUnresolvedLifecycle(t *testing.T) {
	for outcome, want := range map[string]string{"released": "Released", "cancelled": "Cancelled", "expired": "Expired", "completed": "Completed", "failed": "Failed", "": "Inactive", "revoked": "Revoked", "handed_off": "Handed off", "future_outcome": "Future outcome"} {
		l := model.Lease{State: "terminal", Outcome: outcome}
		if leaseLabel(l) != want {
			t.Fatal(outcome, leaseLabel(l))
		}
	}
	m, _ := fixtureDashboard(t)
	m.hideInactive = true
	for _, state := range []string{"active", "queued", "ending", "needs_attention", "provisioning", "reclaiming", "future_state", "terminal"} {
		m.data.status.Leases = append(m.data.status.Leases, model.Lease{ID: state, State: state, Outcome: "expired"})
	}
	if got := m.filteredLeases(); len(got) != 7 {
		t.Fatal("unresolved obligations hidden", got)
	}
	l := activeLease("historical")
	l.OwnerUsername = ""
	m.detail = &l
	text := m.leaseDetailText()
	if !strings.Contains(text, "alice (UID 501) · current account; historical name unknown") {
		t.Fatal("historical current-account fallback missing", text)
	}
	l.Host = "other-host"
	if strings.Contains(m.leaseDetailText(), "alice") {
		t.Fatal("crosshost owner guessed")
	}
	l.Host = "localhost"
	l.OwnerUsername = "recorded"
	if !strings.Contains(m.leaseDetailText(), "recorded (UID 501)") || strings.Contains(m.leaseDetailText(), "current account") {
		t.Fatal("recorded owner did not win")
	}
}
func TestPrivateClientPreferenceRoundTripAndUnsafeFiles(t *testing.T) {
	home := t.TempDir()
	os.Chmod(home, 0700)
	m := newDashboard(t.Context(), home)
	m.view = 2
	press(m, "h")
	if !m.hideInactive {
		t.Fatal("toggle failed")
	}
	second := newDashboard(t.Context(), home)
	if !second.hideInactive {
		t.Fatal("toggle not remembered", m.notice, second.notice)
	}
	info, err := os.Stat(filepath.Join(home, preferenceFile))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings not private", err)
	}
	for name, data := range map[string]string{"oversize": strings.Repeat("x", 4097), "unknown": `{"version":99}`, "trailing": `{"version":1} {}`, "extra": `{"version":1,"unexpected":true}`} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			os.Chmod(home, 0700)
			os.WriteFile(filepath.Join(home, preferenceFile), []byte(data), 0600)
			if _, err := readPreferences(home); err == nil {
				t.Fatal("unsafe settings accepted")
			}
		})
	}
	unsafe := t.TempDir()
	os.Chmod(unsafe, 0700)
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("unchanged"), 0600)
	os.Symlink(target, filepath.Join(unsafe, preferenceFile))
	if _, err := readPreferences(unsafe); err == nil {
		t.Fatal("symlink read")
	}
	if err := writePreferences(unsafe, preferences{Version: 1, HideInactive: true}); err == nil {
		t.Fatal("symlink replaced")
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "unchanged" {
		t.Fatal("external settings touched")
	}
	public := t.TempDir()
	os.Chmod(public, 0755)
	if _, err := readPreferences(public); err == nil {
		t.Fatal("public home accepted")
	}
}

func TestLeaseTableCurrentAccountFallbackIsMarked(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width = 140
	m.view = 2
	historical := activeLease("legacy")
	historical.OwnerUsername = ""
	recorded := activeLease("recorded")
	m.data.status.Leases = []model.Lease{historical, recorded}
	screen := ansi.Strip(m.leasesText())
	if !strings.Contains(screen, "alice* · UID 501") || !strings.Contains(screen, "alice · UID 501") || !strings.Contains(screen, "* Current account lookup; historical name unknown") {
		t.Fatal("historical account display indistinguishable", screen)
	}
}
