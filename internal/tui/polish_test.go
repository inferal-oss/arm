package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"getarm.org/internal/model"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func profileView(t *testing.T, content string, profile colorprofile.Profile) string {
	t.Helper()
	var out bytes.Buffer
	writer := colorprofile.Writer{Forward: &out, Profile: profile}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
func TestPolishedPaletteDownsamplesWithoutLosingContent(t *testing.T) {
	n, total := float64(70), float64(100)
	raw := bar(&n, &total, 24, "blue")
	if len(regexp.MustCompile(`38;2;[0-9;]+`).FindAllString(raw, -1)) < 4 {
		t.Fatal("capable profile lost the meter gradient", raw)
	}
	for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		t.Run(profile.String(), func(t *testing.T) {
			rendered := profileView(t, raw, profile)
			if ansi.StringWidth(rendered) != 24 || ansi.Strip(rendered) != ansi.Strip(raw) {
				t.Fatal("profile changed meter geometry/text", profile, rendered)
			}
			if profile != colorprofile.TrueColor && strings.Contains(rendered, "38;2;") {
				t.Fatal("RGB colors leaked to lower profile")
			}
			if profile == colorprofile.ASCII && regexp.MustCompile(`(?:38|48);`).MatchString(rendered) {
				t.Fatal("color survived monochrome")
			}
		})
	}
	unknown := bar(nil, &total, 24, "")
	zero := float64(0)
	idle := bar(&zero, &total, 24, "")
	if ansi.Strip(unknown) == ansi.Strip(idle) {
		t.Fatal("unavailable was drawn as zero")
	}
	m := visualDashboard(t)
	m.view = 2
	raw = m.View().Content
	for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		if got := ansi.Strip(profileView(t, raw, profile)); got != ansi.Strip(raw) {
			t.Fatal("profile lost labels or selection markers", profile)
		}
	}
}
func TestCompactNavigationLabelsAndMouseRegionsStayAligned(t *testing.T) {
	for _, width := range []int{45, 54, 55, 70, 81, 82, 110, 140} {
		m := visualDashboard(t)
		m.width = width
		m.height = 24
		m.view = 0
		m.syncViewport()
		nav := strings.Split(ansi.Strip(m.View().Content), "\n")[1]
		for index := range viewNames {
			label := m.navigationLabel(index)
			if !strings.Contains(nav, label) {
				t.Fatalf("%d missing distinct navigation %s: %s", width, label, nav)
			}
			found := false
			for _, region := range m.mouseRegions() {
				if region.key == fmt.Sprintf("nav%d", index) {
					found = true
					cells := []rune(nav)
					if region.x+region.width > len(cells) || !strings.Contains(string(cells[region.x:region.x+region.width]), label) {
						t.Fatal("nav hit region diverged", width, index, region, nav)
					}
				}
			}
			if !found {
				t.Fatal("navigation region missing", index)
			}
		}
	}
}
func TestFrameScrollPositionAndModalViewportRemainBounded(t *testing.T) {
	view := viewport.New(viewport.WithWidth(36), viewport.WithHeight(4))
	view.SetContent(strings.Repeat("scrolling content\n", 30))
	for _, offset := range []int{0, 7, 26} {
		view.SetYOffset(offset)
		screen := scrollFrame(window("DETAIL", view.View(), 40, 6, true, "warning"), view, 40)
		lines := strings.Split(ansi.Strip(screen), "\n")
		if len(lines) != 6 || !strings.Contains(lines[5], fmt.Sprint(view.YOffset()+1)+"–") {
			t.Fatal("scroll position hidden", screen)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != 40 {
				t.Fatal("scroll frame shifted geometry", ansi.StringWidth(line))
			}
		}
	}
	for _, size := range [][2]int{{45, 14}, {70, 24}, {140, 42}} {
		m := visualDashboard(t)
		m.width, m.height = size[0], size[1]
		l := m.data.status.Leases[0]
		l.Request.Reason = strings.Repeat("Long reviewed purpose ", 100)
		m.detail = &l
		m.startReview("release")
		m.syncViewport()
		raw := m.View().Content
		lines := strings.Split(ansi.Strip(raw), "\n")
		if len(lines) != m.height {
			t.Fatal("modal shadow escaped height")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatal("modal shadow escaped width")
			}
		}
		if !strings.Contains(ansi.Strip(raw), "[y] Confirm") {
			t.Fatal("fixed review confirmation clipped")
		}
	}
}
func TestLeaseStatusColorsPreserveOutcomeMeaning(t *testing.T) {
	active := model.Lease{State: "active"}
	failed := model.Lease{State: "terminal", Outcome: "failed"}
	released := model.Lease{State: "terminal", Outcome: "released"}
	uncertain := model.Lease{State: "needs_attention"}
	rgb := func(l model.Lease) string { r, g, b, _ := leaseInk(l).RGBA(); return fmt.Sprint(r, g, b) }
	if rgb(active) == rgb(failed) || rgb(failed) == rgb(uncertain) || rgb(active) == rgb(released) {
		t.Fatal("all outcomes use the success accent")
	}
}
func TestWritePolishedColorProfilePreviews(t *testing.T) {
	dir := os.Getenv("ARM_TUI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("optional deterministic synthetic artifacts")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute output path required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"leases-states", "leases-mono", "leases-ansi16", "leases-256", "review-scroll", "monitor-45"} {
		m := visualDashboard(t)
		m.view = 2
		for _, state := range []struct{ state, outcome, reason string }{{"queued", "", "Waiting for an available build slot"}, {"ending", "", "Waiting for owned processes to settle"}, {"terminal", "completed", "Release binary exported; build completed"}, {"terminal", "failed", "Compiler returned a nonzero exit"}, {"terminal", "released", "Finished using the reserved ports"}} {
			l := m.data.status.Leases[0]
			l.ID = state.state + state.outcome
			l.State, l.Outcome = state.state, state.outcome
			l.Request.Reason = state.reason
			m.data.status.Leases = append(m.data.status.Leases, l)
		}
		profile := colorprofile.TrueColor
		switch name {
		case "leases-mono":
			profile = colorprofile.ASCII
		case "leases-ansi16":
			profile = colorprofile.ANSI
		case "leases-256":
			profile = colorprofile.ANSI256
		case "review-scroll":
			l := m.data.status.Leases[0]
			l.Request.Reason = strings.Repeat("Retain the release binary before cleanup. ", 20)
			m.detail = &l
			m.startReview("release")
			m.viewport.SetYOffset(3)
		case "monitor-45":
			m.view = 0
			m.width, m.height = 45, 22
		}
		m.syncViewport()
		raw := profileView(t, m.View().Content, profile)
		for extension, content := range map[string]string{"ansi": raw, "txt": ansi.Strip(raw)} {
			if err := os.WriteFile(filepath.Join(dir, name+"."+extension), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
