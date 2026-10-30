package tui

// Source-only continuation. A separate review/allocation is required before
// building this test binary, opening its PTY or replaying any terminal bytes.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// Reuses the frozen strict fake, typed synthetic data and original model gates.
// There is no production Init or native transport fallback. No direct-frame
// snapshot is represented as PTY output: the parent captures the actual Program.
type terminalLoopFixture struct {
	f                                                           *directFixture
	initial                                                     *dashboardModel
	boundReader                                                 *runsDashboard
	progress                                                    string
	revision, inputs, deliveredMouse, ignored                   int
	producedMouse, activeMouse                                  atomic.Int64
	initSuppressed, quitRequested, programReturned, sameWrapper bool
	stale                                                       tea.Cmd
	staleUI                                                     *runsDashboard
	staleData                                                   string
	staleOffset                                                 int64
	staleGrid                                                   [2]int
	stalePrepared, staleRejected                                bool
	lastGrid                                                    [2]int
}

func loopFixtureGrid(w, h int) bool {
	return w == 72 && h == 28 || w == 100 && h == 35 || w == 160 && h == 50
}
func loopFixtureWrite(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil || os.WriteFile(path+".new", append(b, '\n'), 0600) != nil || os.Rename(path+".new", path) != nil {
		panic("owned loop progress write failed")
	}
}
func (a *terminalLoopFixture) Init() tea.Cmd {
	a.initSuppressed = true
	return nil // Never dashboardModel.Init(), connect(), clockTick() or Batch.
}
func (a *terminalLoopFixture) safeKey(k string) bool {
	if k == "q" {
		return true
	}
	m := a.f.m
	if m.view == 7 && m.runsUI != nil && m.runsUI.selected != nil {
		switch k {
		case "f", "o", "z", "end", "esc", "up", "down", "pgup", "pgdown":
			return true
		case "f12":
			return m.runFullscreen() && !a.stalePrepared
		}
		return false
	}
	return (m.view == 2 || m.view == 7) && m.detail == nil && m.record == nil && !m.modal() && (k == "8" || k == "enter") && !m.feedFocus
}
func (a *terminalLoopFixture) safeMouse(msg tea.MouseMsg) bool {
	m := a.f.m
	p := msg.Mouse()
	if p.Button != tea.MouseLeft {
		return false
	}
	for _, r := range m.mouseRegions() {
		if p.X < r.x || p.Y < r.y || p.X >= r.x+r.width || p.Y >= r.y+r.height {
			continue
		}
		if r.key == "nav7" {
			return m.detail == nil && m.record == nil && !m.modal()
		}
		if r.key == "" && r.row == 0 && !r.feed && !r.record && !r.mesh && !r.pool && !r.recovery && !r.picker {
			return m.detail == nil && !m.modal() && m.view == 7 && m.runRosterVisible()
		}
		return false
	}
	return false
}

// Bubble Tea's filter runs before its internal Raw/Batch/clipboard/exec handlers.
// Only these concrete messages may proceed; payloads of ignored framework
// messages and environment values are never copied into the model or report.
func (a *terminalLoopFixture) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		if !loopFixtureGrid(v.Width, v.Height) {
			panic("unreviewed PTY grid")
		}
		return msg
	case tea.ColorProfileMsg, tea.EnvMsg:
		return msg
	case tea.KeyPressMsg:
		if !a.safeKey(v.String()) {
			panic("unreviewed native key refused")
		}
		return msg
	case tea.MouseClickMsg:
		if !a.safeMouse(v) {
			panic("unreviewed native mouse refused")
		}
		return msg
	case dashboardMouseMsg:
		return msg // Only the audited original OnMouse closure can produce this.
	case tea.QuitMsg:
		if !a.quitRequested {
			panic("unarmed quit message")
		}
		return msg
	default:
		a.ignored++
		return nil // No internal arbitrary command, RawMsg, stream or timer path.
	}
}
func (a *terminalLoopFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.inputs++
	m := a.f.m
	switch v := msg.(type) {
	case tea.ColorProfileMsg, tea.EnvMsg:
		// Framework startup messages are not original dashboard inputs.
	case tea.WindowSizeMsg:
		a.f.update(v, directNoCommand, "native-window-size")
		a.lastGrid = [2]int{v.Width, v.Height}
		if a.stale != nil && [2]int{v.Width, v.Height} != a.staleGrid {
			cmd := a.stale
			a.stale = nil
			mouse, ok := cmd().(dashboardMouseMsg) // Audited original typed event only.
			if !ok {
				panic("delayed native callback type")
			}
			a.f.mouseCommands++
			a.f.update(mouse, directNoCommand, "controlled-delayed-resize-mouse")
			a.staleRejected = m.runsUI == a.staleUI && m.runFullscreen() && string(m.runsUI.data) == a.staleData && m.runsUI.offset == a.staleOffset
			if !a.staleRejected {
				panic("delayed callback changed reader/presentation")
			}
		}
	case tea.KeyPressMsg:
		k := v.String()
		if k == "q" {
			inner, cmd := m.Update(v) // Original q retires reads and returns tea.Quit.
			if inner != a.initial || cmd == nil {
				panic("original quit model/command")
			}
			a.f.invariant()
			quit, ok := cmd().(tea.QuitMsg) // Audited pinned Quit: returns QuitMsg only.
			if !ok {
				panic("unexpected original quit result")
			}
			a.quitRequested = true
			return a, func() tea.Msg { return quit }
		}
		if k == "f12" {
			// Explicit fixture-only controlled delay, not an OS queue claim.
			for _, r := range m.mouseRegions() {
				if r.key == "z" {
					a.stale = m.View().OnMouse(tea.MouseClickMsg{X: r.x, Y: r.y, Button: tea.MouseLeft})
					break
				}
			}
			if a.stale == nil {
				panic("original fullscreen exit region absent")
			}
			a.stalePrepared = true
			a.staleUI = m.runsUI
			a.staleData = string(m.runsUI.data)
			a.staleOffset = m.runsUI.offset
			a.staleGrid = [2]int{m.width, m.height}
		} else {
			a.f.key(k)
		}
	case tea.MouseClickMsg:
		// The framework separately invokes the original View.OnMouse Cmd.
		// dashboardModel.Update itself ignores raw mouse before advancing time.
	case dashboardMouseMsg:
		a.deliveredMouse++
		a.f.mouseCommands++
		policy := directNoCommand
		if v.frame == m.mouseFrame() {
			if v.region.key == "nav7" {
				policy = a.f.keyPolicy("8")
			} else if v.region.key == "" && v.region.row == m.selected {
				policy = a.f.keyPolicy("enter")
			}
		}
		a.f.update(v, policy, "native-typed-mouse")
	default:
		panic("message passed filter without an audited Update case")
	}
	if a.f.m != a.initial {
		panic("original model replaced")
	}
	if m.runsUI != nil {
		if a.boundReader == nil {
			a.boundReader = m.runsUI
		} else if a.boundReader != m.runsUI {
			panic("original reader replaced")
		}
	}
	return a, nil // Never switch to inner dashboardModel or return a library Cmd.
}
func (a *terminalLoopFixture) View() tea.View {
	view := a.f.m.View()
	original := view.OnMouse
	view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		cmd := original(msg)
		if cmd == nil {
			return nil
		}
		return func() tea.Msg {
			a.activeMouse.Add(1)
			defer a.activeMouse.Add(-1)
			v := cmd() // Audited original mouseHandler closure: typed event only.
			if _, ok := v.(dashboardMouseMsg); !ok {
				panic("unreviewed mouse Cmd result")
			}
			a.producedMouse.Add(1)
			return v
		}
	}
	a.revision++
	m := a.f.m
	regions := []map[string]any{}
	for _, r := range m.mouseRegions() {
		regions = append(regions, map[string]any{"x": r.x, "y": r.y, "width": r.width, "height": r.height, "key": r.key, "row": r.row, "feed": r.feed, "record": r.record, "mesh": r.mesh, "pool": r.pool, "recovery": r.recovery, "picker": r.picker})
	}
	state := map[string]any{"revision": a.revision, "inputs": a.inputs, "grid": a.lastGrid, "view": m.view, "viewContent": view.Content, "altScreenFlag": view.AltScreen, "regions": regions, "defaultLeases": m.hideInactive, "defaultRuns": m.hideInactiveRuns, "readClosures": a.f.reads, "calls": a.f.s.calls, "suppressed": a.f.suppressed, "mouseProduced": a.producedMouse.Load(), "mouseDelivered": a.deliveredMouse, "mouseActive": a.activeMouse.Load(), "stalePrepared": a.stalePrepared, "staleRejected": a.staleRejected, "ignoredFrameworkMessages": a.ignored, "initSuppressed": a.initSuppressed, "sameReader": a.boundReader == a.f.m.runsUI, "viewport": map[string]any{"offset": m.viewport.YOffset(), "height": m.viewport.Height(), "total": m.viewport.TotalLineCount()}}
	if ui := m.runsUI; ui != nil {
		h := sha256.Sum256(ui.data)
		id := ""
		if ui.selected != nil {
			id = ui.selected.ID
		}
		state["reader"] = map[string]any{"id": id, "bufferBytes": len(ui.data), "bufferSHA256": hex.EncodeToString(h[:]), "cursor": ui.offset, "following": ui.following, "done": ui.done, "folded": ui.folded, "full": ui.full, "generation": ui.generation, "busy": ui.busy}
	}
	loopFixtureWrite(a.f.t, a.progress, state)
	return view // Original content/layout/parents; only pure mouse Cmd observed.
}

func TestUXTerminalLoopFixture(t *testing.T) {
	root := os.Getenv("ARM_TUI_LOOP_ROOT")
	if root == "" {
		t.Skip("source-reviewed loop child requires separate compile/PTY allocation")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("loop root must be resolved absolute")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("fresh private parent loop root required")
	}
	var size [2]int
	switch os.Getenv("ARM_TUI_LOOP_GRID") {
	case "72x28":
		size = [2]int{72, 28}
	case "100x35":
		size = [2]int{100, 35}
	case "160x50":
		size = [2]int{160, 50}
	default:
		t.Fatal("exact loop grid required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("source cwd unavailable")
	}
	repo := filepath.Clean(filepath.Join(cwd, "..", ".."))
	var guard struct {
		SourceEntries map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"sourceEntries"`
	}
	raw, err := os.ReadFile(filepath.Join(repo, "docs/validation/ux-terminal-source-classification-2026-10-29.json"))
	h := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(h[:]) != "574c62ed1e2d66828082880beeaa4d706b7690cf4f38b1ff3a229aa1165cae5d" || json.Unmarshal(raw, &guard) != nil || len(guard.SourceEntries) != 50 {
		t.Fatal("original source classification changed")
	}
	for file, item := range guard.SourceEntries {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		sum := sha256.Sum256(b)
		if e != nil || hex.EncodeToString(sum[:]) != item.SHA256 {
			t.Fatal("classified source changed")
		}
	}
	legacy, legacyErr := os.ReadFile(filepath.Join(repo, "internal/tui/ux_terminal_direct_fixture_test.go"))
	legacyHash := sha256.Sum256(legacy)
	if legacyErr != nil || hex.EncodeToString(legacyHash[:]) != "ef37d0bdf89bce2dcd68ae7b2de2961f046c57f868edcb9d2e8ccc5952e37420" {
		t.Fatal("frozen direct fixture helper changed")
	}
	report := &directFixtureReport{Scope: "Original Program with test-only model wrapper; parent captures actual owned PTY bytes"}
	f := prepareDirectFixture(t, root, size, newDirectFixtureData(t), report)
	progress := filepath.Join(root, f.grid, "progress.json")
	a := &terminalLoopFixture{f: f, initial: f.m, progress: progress, lastGrid: size}
	ctx, cancel := context.WithTimeout(f.m.ctx, 25*time.Second)
	defer cancel()
	p := tea.NewProgram(a, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout), tea.WithoutSignalHandler(), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithFilter(a.filter))
	started := false
	defer func() {
		if started {
			p.Kill()
			p.Wait()
		} // Exact owned original Program, also on Goexit.
		cancel()
		f.close()
		loopFixtureWrite(t, filepath.Join(root, f.grid, "child-final.json"), map[string]any{"testFailed": t.Failed(), "programStarted": started, "programReturned": a.programReturned, "sameWrapperReturned": a.sameWrapper, "sameOriginalModel": a.f.m == a.initial, "sameReader": a.boundReader == a.f.m.runsUI, "initSuppressed": a.initSuppressed, "quitRequested": a.quitRequested, "staleRejected": a.staleRejected, "mouseProduced": a.producedMouse.Load(), "mouseDelivered": a.deliveredMouse, "mouseActive": a.activeMouse.Load(), "contextCanceled": f.m.ctx.Err() != nil, "fakeClosed": f.s.closed, "activeFakeCalls": f.s.active, "forbidden": f.s.forbidden, "reads": f.reads, "calls": f.s.calls, "suppressed": f.suppressed, "cleanupQualification": "Original Program Run/Kill/Wait and owned context/fake outcomes only; no blanket goroutine/OS descendant-reap or production Run startup/auth/report claim"})
	}()
	started = true
	final, runErr := p.Run()
	a.programReturned = true
	a.sameWrapper = final == a
	if runErr != nil || !a.sameWrapper || !a.quitRequested || a.activeMouse.Load() != 0 || a.producedMouse.Load() != int64(a.deliveredMouse) || a.stale != nil || !a.staleRejected {
		t.Error("owned Program/adapter/mouse/resize outcome did not complete")
	}
	if a.f.m != a.initial || f.s.active != 0 || len(f.s.forbidden) != 0 {
		t.Error("owned model/effect boundary changed")
	}
}
