package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agentprojects"
)

func onboardTypeaheadFixture(t *testing.T) *rolloutOnboardModel {
	t.Helper()
	m := newRolloutOnboard(t.Context(), filepath.Join(t.TempDir(), "absent-state"), "picker fixture", nil)
	m.Update(onboardCatalogMsg{agentprojects.Catalog{Projects: []agentprojects.Project{
		{Directory: "/projects/first", Names: []string{"Alpha", "Hidden searchable name", "Jade", "Keep", "Quick task", "Zebra needle"}},
		{Directory: "/projects/second", Names: []string{"Beta", "Quick followup", "日本語"}},
	}}})
	return m
}

func onboardType(t *testing.T, m *rolloutOnboardModel, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		if m.stage != onboardProjects || m.cancelled {
			t.Fatal("typing left picker", text, m.stage)
		}
	}
}

func TestRolloutOnboardTypeaheadPrintableNamesAndPaths(t *testing.T) {
	for _, tc := range []struct{ query, directory string }{
		{"q", ""}, {"j", ""}, {"k", ""},
		{"Zebra needle", "/projects/first"}, {"projects/second", "/projects/second"}, {"日本語", "/projects/second"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			m := onboardTypeaheadFixture(t)
			onboardType(t, m, tc.query)
			if m.projects.FilterInput.Value() != tc.query || m.projects.FilterState() != list.Filtering || len(m.projects.VisibleItems()) == 0 {
				t.Fatal("typeahead did not filter", m.projects.FilterInput.Value())
			}
			if tc.directory != "" {
				item, ok := m.projects.SelectedItem().(onboardItem)
				if !ok || item.project.Directory != tc.directory || len(m.projects.VisibleItems()) != 1 {
					t.Fatal("wrong directory match", m.projects.VisibleItems())
				}
			}
			if _, err := os.Stat(m.home); !os.IsNotExist(err) {
				t.Fatal("typing created policy state", err)
			}
		})
	}
}

func TestRolloutOnboardTypeaheadEditingNavigationAndNoMatch(t *testing.T) {
	m := onboardTypeaheadFixture(t)
	onboardType(t, m, "Quick")
	first := m.projects.SelectedItem().(onboardItem).project.Directory
	onboardKey(m, "down")
	if m.projects.SelectedItem().(onboardItem).project.Directory == first || m.projects.FilterState() != list.Filtering {
		t.Fatal("down did not navigate while filtering")
	}
	onboardKey(m, "up")
	if m.projects.SelectedItem().(onboardItem).project.Directory != first {
		t.Fatal("up did not restore selection")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	onboardType(t, m, "日本語")
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.projects.FilterInput.Value() != "日本" || len(m.projects.VisibleItems()) != 1 {
		t.Fatal("backspace did not remove one Unicode rune")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.projects.FilterInput.Value() != "" || len(m.projects.VisibleItems()) != 3 {
		t.Fatal("empty query did not restore choices")
	}
	onboardType(t, m, "no-such-project")
	if len(m.projects.VisibleItems()) != 0 || onboardKey(m, "enter") != nil || m.stage != onboardProjects || m.applyStarted {
		t.Fatal("no-match Enter selected a directory or policy")
	}
	onboardKey(m, "esc")
	if m.stage != onboardProjects || m.projects.FilterState() != list.Unfiltered || len(m.projects.VisibleItems()) != 3 {
		t.Fatal("Escape did not clear the filter")
	}
	// The legacy slash shortcut focuses an empty filter; later slashes are text.
	onboardKey(m, "/")
	if m.projects.FilterInput.Value() != "" || m.projects.FilterState() != list.Filtering {
		t.Fatal("slash did not focus filter")
	}
	onboardType(t, m, "/projects/second")
	if m.projects.SelectedItem().(onboardItem).project.Directory != "/projects/second" {
		t.Fatal("path separators were not editable filter text")
	}
	if !strings.Contains(m.View().Content, "Type to filter names or paths") {
		t.Fatal("typeahead hint missing")
	}
}

func TestRolloutOnboardTypeaheadLatestQueryWinsBeforeEnter(t *testing.T) {
	m := onboardTypeaheadFixture(t)
	onboardType(t, m, "Alpha")
	old := m.projects
	queued := old.SetItems(old.Items())
	if queued == nil {
		t.Fatal("expected an old asynchronous Bubbles filter command")
	}
	stale := queued()
	if _, ok := stale.(list.FilterMatchesMsg); !ok {
		t.Fatal("expected a queued filter result")
	}
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	onboardType(t, m, "Be")
	m.projects.SetFilterState(list.FilterApplied)
	onboardType(t, m, "ta")
	if m.projects.FilterInput.Value() != "Beta" {
		t.Fatal("typing did not continue an applied filter")
	}
	m.Update(stale)
	if m.projects.SelectedItem().(onboardItem).project.Directory != "/projects/second" {
		t.Fatal("stale result replaced current matches")
	}
	// No filter commands are drained between typing and Enter.
	if cmd := onboardKey(m, "enter"); cmd == nil || m.stage != onboardResolving || m.input.Value() != "/projects/second" || m.applyStarted {
		t.Fatal("Enter did not choose the latest match immediately")
	}
}

func TestRolloutOnboardTypeaheadPasteManualAndCancellation(t *testing.T) {
	m := onboardTypeaheadFixture(t)
	m.Update(tea.PasteMsg{Content: "another directory"})
	if len(m.projects.VisibleItems()) != 1 || !m.projects.SelectedItem().(onboardItem).manual {
		t.Fatal("paste did not find manual directory choice")
	}
	if cmd := onboardKey(m, "enter"); cmd == nil || m.stage != onboardDirectory {
		t.Fatal("manual directory route unavailable from filter")
	}
	onboardKey(m, "esc")
	if m.stage != onboardProjects {
		t.Fatal("manual Escape did not return to picker")
	}
	onboardKey(m, "esc")
	if m.stage != onboardProjects || m.cancelled {
		t.Fatal("first Escape cancelled instead of clearing")
	}
	onboardKey(m, "esc")
	if m.stage != onboardDone || !m.cancelled || m.applyStarted {
		t.Fatal("empty-filter Escape did not cancel")
	}
	m = onboardTypeaheadFixture(t)
	onboardType(t, m, "qjk")
	onboardKey(m, "ctrl+c")
	if m.stage != onboardDone || !m.cancelled || m.applyStarted {
		t.Fatal("Ctrl+C did not cancel active filtering")
	}
}
