package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func newShellFilterDashboard(t *testing.T, width, height int) *dashboardModel {
	t.Helper()
	m, transport := fixtureDashboard(t)
	m.view = 2
	for i := 0; i < 12; i++ {
		lease := activeLease(fmt.Sprintf("shell-filter-%02d", i))
		lease.Request.Reason = "Synthetic filter reading fixture"
		m.data.status.Leases = append(m.data.status.Leases, lease)
	}
	if m.pendingStoreError != "" {
		t.Fatal("private fixture storage setup failed")
	}
	t.Cleanup(func() {
		transport.mu.Lock()
		calls := len(transport.operations)
		transport.mu.Unlock()
		transport.Close()
		if calls != 0 {
			t.Errorf("shell/filter test executed %d transport calls", calls)
		}
	})
	shellFilterUpdate(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// Drive original Update without scheduling focus/cursor/backend commands.
func shellFilterUpdate(t *testing.T, m *dashboardModel, msg tea.Msg) tea.Cmd {
	t.Helper()
	original, cmd := m.Update(msg)
	if original != m {
		t.Fatal("Update changed model identity")
	}
	return cmd
}

func requireShellFilterQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	// Execute only the reviewed named Quit command as a pure message control.
	if cmd == nil || reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(tea.Quit).Pointer() {
		t.Fatal("explicit Exit did not return the original named Quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("original Quit did not return QuitMsg")
	}
}

func openShellFilter(t *testing.T, m *dashboardModel, draft string) {
	t.Helper()
	shellFilterUpdate(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if draft != "" {
		shellFilterUpdate(t, m, tea.PasteMsg{Content: draft})
	}
	if m.inputMode != "filter" || !m.input.Focused() {
		t.Fatal("filter did not open with input focus")
	}
}

func shellFilterMouseCommand(t *testing.T, m *dashboardModel, mouse tea.MouseMsg) tea.Msg {
	t.Helper()
	view := m.View()
	if view.OnMouse == nil {
		t.Fatal("rendered view has no mouse callback")
	}
	cmd := view.OnMouse(mouse)
	if cmd == nil {
		t.Fatal("original mouse callback returned no command")
	}
	msg := cmd() // Exact command returned by this original view, once only.
	typed, ok := msg.(dashboardMouseMsg)
	if !ok {
		t.Fatalf("original callback returned %T", msg)
	}
	frame := m.mouseFrame()
	if typed.wheel != 0 {
		frame = m.mouseScrollFrame()
	}
	if typed.frame != frame {
		t.Fatal("mouse command was not bound to its original frame")
	}
	return msg
}

func shellFilterFooterClick(t *testing.T, m *dashboardModel, key string) tea.Msg {
	t.Helper()
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	if len(rows) != m.height {
		t.Fatal("ordinary view escaped requested height")
	}
	footer := rows[len(rows)-1]
	if !strings.Contains(footer, "["+key+"] Exit") {
		t.Fatalf("footer does not advertise available exit %q: %s", key, footer)
	}
	var targets []mouseRegion
	for _, region := range m.mouseRegions() {
		if region.y == m.height-1 && region.key == key && region.x >= 0 && region.x+region.width <= m.width && strings.Contains(ansi.Cut(footer, region.x, region.x+region.width), "["+key+"] Exit") {
			targets = append(targets, region)
		}
	}
	if len(targets) != 1 {
		t.Fatalf("footer has %d matching visible exit targets", len(targets))
	}
	r := targets[0]
	return shellFilterMouseCommand(t, m, tea.MouseClickMsg{X: r.x + r.width/2, Y: r.y, Button: tea.MouseLeft})
}

func requireShellFilterFieldVisible(t *testing.T, m *dashboardModel) {
	t.Helper()
	if m.inputMode != "filter" || !m.input.Focused() || m.input.Value() == "" || !strings.Contains(ansi.Strip(m.viewport.View()), m.input.Value()) {
		t.Fatalf("focused filter draft is not visible: mode=%q focused=%t offset=%d draft=%q viewport=%q", m.inputMode, m.input.Focused(), m.viewport.YOffset(), m.input.Value(), ansi.Strip(m.viewport.View()))
	}
}

func scrollShellFilterHelp(t *testing.T, m *dashboardModel) {
	t.Helper()
	for i := 0; i < 2; i++ {
		msg := shellFilterMouseCommand(t, m, tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
		shellFilterUpdate(t, m, msg)
	}
	if m.viewport.YOffset() == 0 || !strings.Contains(ansi.Strip(m.viewport.View()), "[Enter] Apply filter") || !m.input.Focused() {
		t.Fatal("wheel could not expose filter help/controls while retaining input focus")
	}
}

func TestShellFilterFooterExit(t *testing.T) {
	for _, grid := range [][2]int{{45, 14}, {72, 28}, {100, 35}, {160, 50}} {
		t.Run(fmt.Sprintf("%dx%d", grid[0], grid[1]), func(t *testing.T) {
			m := newShellFilterDashboard(t, grid[0], grid[1])
			openShellFilter(t, m, "probe-draft")
			cmd := shellFilterUpdate(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"})
			if cmd != nil && reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer() {
				t.Fatal("typed q returned Quit during filtering")
			}
			if m.input.Value() != "probe-draftq" || m.filter != "" || m.inputMode != "filter" {
				t.Fatal("typed q did not remain unapplied filter text")
			}
			requireShellFilterFieldVisible(t, m)
			click := shellFilterFooterClick(t, m, "ctrl+c")
			requireShellFilterQuit(t, shellFilterUpdate(t, m, click))
			if m.input.Value() != "probe-draftq" || m.filter != "" {
				t.Fatal("footer Exit edited or applied the filter draft")
			}
		})
	}
}

func TestShellFilterQuitControls(t *testing.T) {
	t.Run("outside-editor-q", func(t *testing.T) {
		m := newShellFilterDashboard(t, 45, 14)
		requireShellFilterQuit(t, shellFilterUpdate(t, m, tea.KeyPressMsg{Code: 'q', Text: "q"}))
	})
	t.Run("outside-editor-footer", func(t *testing.T) {
		m := newShellFilterDashboard(t, 45, 14)
		requireShellFilterQuit(t, shellFilterUpdate(t, m, shellFilterFooterClick(t, m, "q")))
	})
	t.Run("name-editor-footer", func(t *testing.T) {
		// Naming validates canonical authority/lease IDs before opening. Use
		// the existing naming fixture rather than the list-scroll fixture.
		m, transport, _ := nameFixture(t)
		t.Cleanup(func() {
			transport.mu.Lock()
			calls := len(transport.operations)
			transport.mu.Unlock()
			transport.Close()
			if calls != 0 {
				t.Errorf("name footer test executed %d transport calls", calls)
			}
		})
		if m.pendingStoreError != "" {
			t.Fatal("private naming fixture storage setup failed")
		}
		shellFilterUpdate(t, m, tea.WindowSizeMsg{Width: 45, Height: 14})
		// Open the original editor, leaving its read command unexecuted.
		readCmd := shellFilterUpdate(t, m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
		if m.nameUI == nil || readCmd == nil {
			t.Fatal("name editor did not open with its original read command")
		}
		requireShellFilterQuit(t, shellFilterUpdate(t, m, shellFilterFooterClick(t, m, "ctrl+c")))
	})
	t.Run("editing-keyboard-ctrl-c", func(t *testing.T) {
		m := newShellFilterDashboard(t, 45, 14)
		openShellFilter(t, m, "draft")
		requireShellFilterQuit(t, shellFilterUpdate(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	})
	t.Run("below-minimum-while-editing", func(t *testing.T) {
		m := newShellFilterDashboard(t, 45, 14)
		openShellFilter(t, m, "draft")
		shellFilterUpdate(t, m, tea.WindowSizeMsg{Width: 44, Height: 13})
		view := m.View()
		if !strings.Contains(ansi.Strip(view.Content), "Ctrl+C exits") || strings.Contains(ansi.Strip(view.Content), "q or") || view.OnMouse != nil {
			t.Fatal("minimum-size message does not offer an available keyboard exit")
		}
		requireShellFilterQuit(t, shellFilterUpdate(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}))
	})
}

func TestShellFilterFieldVisibility(t *testing.T) {
	t.Run("open-from-scrolled-list", func(t *testing.T) {
		m := newShellFilterDashboard(t, 45, 14)
		for i := 0; i < 12 && m.viewport.YOffset() < 3; i++ {
			shellFilterUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
		}
		if m.viewport.YOffset() < 3 {
			t.Fatal("list-scroll precondition not reached")
		}
		openShellFilter(t, m, "visible-draft")
		requireShellFilterFieldVisible(t, m)
	})
	for _, action := range []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"typing-after-help", tea.KeyPressMsg{Code: 'z', Text: "z"}, "draftz"},
		{"paste-after-help", tea.PasteMsg{Content: "-pasted"}, "draft-pasted"},
		{"cursor-after-help", tea.KeyPressMsg{Code: tea.KeyHome}, "draft"},
		{"resize-after-help", tea.WindowSizeMsg{Width: 46, Height: 14}, "draft"},
	} {
		t.Run(action.name, func(t *testing.T) {
			m := newShellFilterDashboard(t, 45, 14)
			openShellFilter(t, m, "draft")
			scrollShellFilterHelp(t, m)
			// Unrelated receipts do not take the viewport away from help.
			shellFilterUpdate(t, m, tickMsg(m.timeNow))
			if m.viewport.YOffset() == 0 {
				t.Fatal("clock receipt unexpectedly hid the scrolled help")
			}
			shellFilterUpdate(t, m, action.msg)
			if m.input.Value() != action.want || m.filter != "" {
				t.Fatal("resuming editing changed the draft or applied it")
			}
			requireShellFilterFieldVisible(t, m)
		})
	}
}

func TestShellFilterApplyCancel(t *testing.T) {
	m := newShellFilterDashboard(t, 45, 14)
	openShellFilter(t, m, "  shell-filter-  ")
	shellFilterUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.filter != "shell-filter-" || m.selected != 0 || m.inputMode != "" || m.input.Focused() || m.viewport.YOffset() != 0 || len(m.filteredLeases()) != 12 {
		t.Fatal("Enter did not trim/apply the local filter and return to its results")
	}
	openShellFilter(t, m, "-discard")
	shellFilterUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.filter != "shell-filter-" || m.inputMode != "" || m.input.Focused() {
		t.Fatal("Escape changed the applied filter or retained input focus")
	}
	openShellFilter(t, m, "")
	if m.input.Value() != "shell-filter-" {
		t.Fatal("reopening filter restored the discarded draft")
	}
	requireShellFilterFieldVisible(t, m)
}

func TestShellFilterStaleFooterClick(t *testing.T) {
	m := newShellFilterDashboard(t, 45, 14)
	openShellFilter(t, m, "draft")
	click := shellFilterFooterClick(t, m, "ctrl+c")
	shellFilterUpdate(t, m, tea.WindowSizeMsg{Width: 46, Height: 14})
	if cmd := shellFilterUpdate(t, m, click); cmd != nil {
		t.Fatal("resized-away footer click returned a command")
	}
	if m.input.Value() != "draft" || m.inputMode != "filter" || !m.input.Focused() {
		t.Fatal("stale footer click changed the active filter")
	}
}
