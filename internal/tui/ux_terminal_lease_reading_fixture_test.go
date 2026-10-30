package tui

// SOURCE PREPARATION ONLY: separate source/build/PTY/browser review required.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

const leaseReadingFilter = "no-synthetic-match"

// Keeps the original model and unchanged strict direct helper. Init never
// starts native transport, clocks, streams, reconnect or arbitrary commands.
type terminalLeaseReadingFixture struct {
	f                                                        *directFixture
	initial                                                  *dashboardModel
	progress, expectedID                                     string
	grid                                                     [2]int
	revision, inputs, delivered, ignored, recordKeys, pastes int
	produced, active                                         atomic.Int64
	initSuppressed, quitRequested, returned, sameWrapper     bool
	sawExactNumber, sawReview, declined                      bool
}

func (a *terminalLeaseReadingFixture) Init() tea.Cmd { a.initSuppressed = true; return nil }
func (a *terminalLeaseReadingFixture) safeKey(k string) bool {
	m := a.f.m
	if k == "q" {
		return m.inputMode == ""
	}
	if m.inputMode == "filter" {
		return k == "enter" || k == "esc"
	}
	if m.review != nil {
		return k == "end" || k == "pgdown" || k == "pgup" || k == "n" || k == "esc"
	}
	if m.record != nil {
		return k == "down" || k == "up" || k == "enter" || k == "esc"
	}
	if m.detail != nil {
		return k == "i" || k == "x" || k == "esc" || k == "end" || k == "pgdown"
	}
	return m.view == 2 && !m.feedFocus && (k == "/" || k == "esc")
}
func (a *terminalLeaseReadingFixture) safeMouse(msg tea.MouseMsg) bool {
	m := a.f.m
	p := msg.Mouse()
	if p.Button != tea.MouseLeft || p.X < 0 || p.Y < 0 || p.X >= m.width || p.Y >= m.height {
		return false
	}
	for _, r := range m.mouseRegions() {
		if p.X < r.x || p.Y < r.y || p.X >= r.x+r.width || p.Y >= r.y+r.height {
			continue
		}
		if r.feed || r.record || r.mesh || r.pool || r.recovery || r.picker {
			return false
		}
		if m.review != nil {
			return r.key == "n" && !m.pending && m.review.request.Action == "release"
		}
		return m.view == 2 && m.detail == nil && m.record == nil && !m.modal() && !m.feedFocus && r.key == "" && r.row == 0 && m.selected == 0
	}
	return false
}
func (a *terminalLeaseReadingFixture) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		if [2]int{v.Width, v.Height} != a.grid {
			panic("lease reading grid drift")
		}
		return msg
	case tea.ColorProfileMsg, tea.EnvMsg:
		return msg
	case tea.KeyPressMsg:
		if !a.safeKey(v.String()) {
			panic("lease reading key refused")
		}
		return msg
	case tea.PasteMsg:
		if a.f.m.inputMode != "filter" || v.Content != leaseReadingFilter || a.pastes != 0 {
			panic("lease reading paste refused")
		}
		return msg
	case tea.MouseClickMsg:
		if !a.safeMouse(v) {
			panic("lease reading mouse refused")
		}
		return msg
	case dashboardMouseMsg:
		return msg // Only decorated original pure OnMouse Cmd.
	case tea.QuitMsg:
		if !a.quitRequested {
			panic("unarmed lease reading quit")
		}
		return msg
	default:
		a.ignored++
		return nil // No Raw/exec/clipboard/Batch/receipt/stream/timer.
	}
}
func (a *terminalLeaseReadingFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.inputs++
	m := a.f.m
	switch v := msg.(type) {
	case tea.ColorProfileMsg, tea.EnvMsg:
	case tea.WindowSizeMsg:
		a.f.update(v, directNoCommand, "lease-native-size")
	case tea.PasteMsg:
		a.pastes++
		a.f.update(v, directSuppressUI, "lease-native-filter-paste")
	case tea.KeyPressMsg:
		k := v.String()
		if k == "q" {
			inner, cmd := m.Update(v)
			if inner != a.initial || cmd == nil {
				panic("lease original quit identity")
			}
			a.f.invariant()
			quit, ok := cmd().(tea.QuitMsg)
			if !ok {
				panic("lease original quit result")
			}
			a.quitRequested = true
			return a, func() tea.Msg { return quit }
		}
		if m.record != nil && k != "esc" {
			a.recordKeys++
			if a.recordKeys > 64 {
				panic("lease record key budget")
			}
		}
		reviewing := m.review != nil
		a.f.key(k)
		if reviewing && m.review == nil && (k == "n" || k == "esc") {
			a.declined = true
		}
	case tea.MouseClickMsg: // Framework separately invokes original OnMouse.
	case dashboardMouseMsg:
		a.delivered++
		a.f.mouseCommands++
		policy := directNoCommand
		reviewing := m.review != nil
		if v.frame == m.mouseFrame() {
			if reviewing && v.region.key == "n" {
				policy = directNoCommand
			} else if m.detail == nil && m.record == nil && !m.modal() && v.region.key == "" && v.region.row == 0 {
				policy = directInspect
			} else {
				panic("lease typed mouse target refused")
			}
		}
		a.f.update(v, policy, "lease-native-typed-mouse")
		if reviewing && m.review == nil {
			a.declined = true
		}
	default:
		panic("lease unreviewed update message")
	}
	if a.f.m != a.initial || m.view != 2 || m.runsUI != nil {
		panic("lease original identity/view drift")
	}
	a.f.invariant()
	return a, nil // Never return a library/Batch/timer command.
}
func (a *terminalLeaseReadingFixture) restored() bool {
	m := a.f.m
	return m.detail == nil && m.record == nil && m.review == nil && m.inputMode == "" && m.filter == "" && !m.pending && m.unknownLease == "" && !m.feedFocus && m.hideInactive && m.hideInactiveRuns && len(m.filteredLeases()) == 8 && m.selectionID() == a.expectedID
}
func (a *terminalLeaseReadingFixture) View() tea.View {
	m := a.f.m
	view := m.View()
	original := view.OnMouse
	view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		cmd := original(msg)
		if cmd == nil {
			return nil
		}
		return func() tea.Msg {
			a.active.Add(1)
			defer a.active.Add(-1)
			value := cmd()
			if _, ok := value.(dashboardMouseMsg); !ok {
				panic("lease unreviewed mouse Cmd")
			}
			a.produced.Add(1)
			return value
		}
	}
	a.revision++
	regions := []map[string]any{}
	for _, r := range m.mouseRegions() {
		regions = append(regions, map[string]any{"x": r.x, "y": r.y, "width": r.width, "height": r.height, "key": r.key, "row": r.row, "feed": r.feed, "record": r.record, "mesh": r.mesh, "pool": r.pool, "recovery": r.recovery, "picker": r.picker})
	}
	state := map[string]any{"revision": a.revision, "inputs": a.inputs, "grid": a.grid, "viewContent": view.Content, "altScreenFlag": view.AltScreen, "regions": regions, "initSuppressed": a.initSuppressed, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "recordKeys": a.recordKeys, "pastes": a.pastes, "readClosures": a.f.reads, "calls": a.f.s.calls, "suppressed": a.f.suppressed, "defaultLeases": m.hideInactive, "defaultRuns": m.hideInactiveRuns, "inputMode": m.inputMode, "inputValue": m.input.Value(), "filter": m.filter, "recordOpen": m.record != nil, "reviewOpen": m.review != nil, "detailOpen": m.detail != nil, "restored": a.restored(), "sameModel": m == a.initial, "pending": m.pending, "unknownEmpty": m.unknownLease == "", "viewport": map[string]any{"offset": m.viewport.YOffset(), "height": m.viewport.Height(), "total": m.viewport.TotalLineCount()}}
	ids := []string{}
	for _, l := range m.filteredLeases() {
		ids = append(ids, l.ID)
	}
	state["visibleLeaseIDs"] = ids
	if m.detail != nil {
		state["detailID"] = m.detail.ID
		state["detailRevision"] = m.detail.Revision
	}
	if m.review != nil {
		a.sawReview = true
		state["reviewID"] = m.review.request.ID
		state["reviewRevision"] = m.review.request.Revision
		state["reviewAction"] = m.review.request.Action
	}
	if m.record != nil {
		paths := []string{}
		for _, line := range m.record.lines {
			paths = append(paths, line.path)
		}
		state["recordPaths"] = paths
		state["recordSelected"] = m.record.selected
		line := m.record.lines[m.record.selected]
		state["recordPath"] = line.path
		if n, ok := line.value.(json.Number); ok {
			state["recordValueDecimal"] = n.String()
			state["recordValueType"] = "json.Number"
			if line.path == `["request","resources","memory_bytes"]` && n.String() == "9007199254740993" {
				a.sawExactNumber = true
			}
		}
	}
	loopFixtureWrite(a.f.t, a.progress, state)
	return view
}
func TestUXTerminalLeaseReadingFixture(t *testing.T) {
	root := os.Getenv("ARM_TUI_LEASE_ROOT")
	if root == "" {
		t.Skip("reviewed lease reading source requires separate build/PTY allocation")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("resolved private lease root required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private lease root required")
	}
	var grid [2]int
	switch os.Getenv("ARM_TUI_LEASE_GRID") {
	case "72x28":
		grid = [2]int{72, 28}
	case "100x35":
		grid = [2]int{100, 35}
	case "160x50":
		grid = [2]int{160, 50}
	default:
		t.Fatal("exact lease grid required")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("lease source cwd")
	}
	repo := filepath.Clean(filepath.Join(cwd, "..", ".."))
	var guard struct {
		SourceEntries map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"sourceEntries"`
	}
	raw, err := os.ReadFile(filepath.Join(repo, "docs/validation/ux-terminal-source-classification-2026-10-29.json"))
	sum := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != "574c62ed1e2d66828082880beeaa4d706b7690cf4f38b1ff3a229aa1165cae5d" || json.Unmarshal(raw, &guard) != nil || len(guard.SourceEntries) != 50 {
		t.Fatal("lease classification guard")
	}
	for file, item := range guard.SourceEntries {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		h := sha256.Sum256(b)
		if e != nil || hex.EncodeToString(h[:]) != item.SHA256 {
			t.Fatal("lease classified source drift")
		}
	}
	for file, want := range map[string]string{"internal/tui/ux_terminal_direct_fixture_test.go": "ef37d0bdf89bce2dcd68ae7b2de2961f046c57f868edcb9d2e8ccc5952e37420", "internal/tui/ux_terminal_loop_fixture_test.go": "1dce0f1852791781f941eff7086c3cb6420c7d1305f87868182ae3f0a0ded168"} {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		h := sha256.Sum256(b)
		if e != nil || hex.EncodeToString(h[:]) != want {
			t.Fatal("lease frozen helper drift")
		}
	}
	report := &directFixtureReport{Scope: "Original lease reading Program/PTY wrapper; no mutation/native transport"}
	f := prepareDirectFixture(t, root, grid, newDirectFixtureData(t), report)
	a := &terminalLeaseReadingFixture{f: f, initial: f.m, progress: filepath.Join(root, f.grid, "progress.json"), grid: grid, expectedID: f.s.data.Leases[0].ID}
	ctx, cancel := context.WithTimeout(f.m.ctx, 25*time.Second)
	defer cancel()
	p := tea.NewProgram(a, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout), tea.WithoutSignalHandler(), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithFilter(a.filter))
	started := false
	defer func() {
		if started {
			p.Kill()
			p.Wait()
		}
		cancel()
		f.close()
		loopFixtureWrite(t, filepath.Join(root, f.grid, "child-final.json"), map[string]any{"testFailed": t.Failed(), "programStarted": started, "programReturned": a.returned, "sameWrapperReturned": a.sameWrapper, "sameModel": a.f.m == a.initial, "restored": a.restored(), "quitRequested": a.quitRequested, "initSuppressed": a.initSuppressed, "sawExactNumber": a.sawExactNumber, "sawReview": a.sawReview, "declined": a.declined, "pastes": a.pastes, "recordKeys": a.recordKeys, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "reads": f.reads, "calls": f.s.calls, "forbidden": f.s.forbidden, "activeFakeCalls": f.s.active, "fakeClosed": f.s.closed, "contextCanceled": f.m.ctx.Err() != nil, "cleanupQualification": "Original Program/fake/context/Popen/PTY observations only; no blanket goroutine/descendant reap or production startup/auth"})
	}()
	started = true
	final, runErr := p.Run()
	a.returned = true
	a.sameWrapper = final == a
	if runErr != nil || !a.sameWrapper || !a.quitRequested || !a.restored() || !a.sawExactNumber || !a.sawReview || !a.declined || a.pastes != 1 || a.recordKeys > 64 || a.active.Load() != 0 || a.produced.Load() != 2 || a.delivered != 2 || f.reads != 1 || len(f.s.calls) != 1 || f.s.calls[0].Operation != "inspect" {
		t.Error(fmt.Sprintf("lease reading original outcome incomplete: %T", runErr))
	}
}
