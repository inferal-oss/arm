package tui

// Source only: this entry needs its own reviewed build and native allocation.
// The accepted direct/lease/72-column entry points and guards remain unchanged.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

const filterShellBinding = "docs/validation/ux-terminal-filter-shell-proposal-2026-10-30.json"
const filterShellBindingSHA = "4e6301237a2893f4a6dfc06d8c66f50041ab6dd34e6d948598bc1f5c635d9420"
const filterShellSelector = "^TestUXTerminalFilterShellFixture$"

// Reuse only the strict fake/data/cleanup and bounded passive metadata types.
// The old resize/update/command policies and renderer recognizer are not called.
type terminalFilterShellFixture struct {
	f                                           *directFixture
	initial                                     *dashboardModel
	diag                                        *leaseReadiness
	progress                                    string
	revision, inputs                            int
	sizes, downs, pastes                        int
	wheels, clicks, delivered                   int
	produced, active                            atomic.Int64
	initSuppressed, quitRequested, quitObserved bool
	returned, sameWrapper                       bool
}

func (a *terminalFilterShellFixture) Init() tea.Cmd { a.initSuppressed = true; return nil }
func (a *terminalFilterShellFixture) safeKey(v tea.KeyPressMsg) bool {
	m := a.initial
	switch v.String() {
	case "down":
		return m.inputMode == "" && m.filter == "" && a.downs < 12
	case "/":
		return m.inputMode == ""
	case "q":
		return m.inputMode == "filter" && m.input.Value() == "probe-draft" && m.filter == "" && v.Text == "q"
	case "enter", "esc":
		return m.inputMode == "filter"
	}
	return false // Keyboard q/Ctrl+C is never the final exit in this fixture.
}
func (a *terminalFilterShellFixture) filter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		if v.Width != 45 || v.Height != 14 {
			panic("filter shell native grid")
		}
		return msg
	case tea.ColorProfileMsg, tea.EnvMsg:
		return msg
	case tea.KeyPressMsg:
		if !a.safeKey(v) {
			panic("filter shell key refused")
		}
		a.diag.seal()
		return msg
	case tea.PasteMsg:
		m := a.initial
		first := a.pastes == 0 && m.input.Value() == "" && v.Content == "probe-draft"
		second := a.pastes == 1 && m.filter == "probe-draftq" && m.input.Value() == "probe-draftq" && v.Content == "-discard"
		if m.inputMode != "filter" || !(first || second) {
			panic("filter shell paste refused")
		}
		return msg
	case tea.MouseWheelMsg:
		p := v.Mouse()
		if a.initial.inputMode != "filter" || a.initial.input.Value() != "probe-draft" || a.wheels >= 2 || p.Button != tea.MouseWheelDown || p.X != 10 || p.Y != 6 {
			panic("filter shell wheel refused")
		}
		a.wheels++
		a.diag.event("parsed-wheel", 0, p.X, p.Y, true)
		return msg
	case tea.MouseClickMsg:
		p := v.Mouse()
		m := a.initial
		valid := false
		for _, r := range m.mouseRegions() {
			if r.key == "ctrl+c" && r.y == 13 && r.height == 1 && r.x >= 0 && r.x+r.width <= 45 && p.X >= r.x && p.X < r.x+r.width && p.Y == r.y {
				valid = true
			}
		}
		if a.clicks != 0 || !valid || p.Button != tea.MouseLeft || m.inputMode != "filter" || m.input.Value() != "probe-draftq" || a.pastes != 2 {
			panic("filter shell footer press refused")
		}
		a.clicks++
		a.diag.event("parsed-click", 0, p.X, p.Y, true)
		return msg
	case dashboardMouseMsg:
		return msg // Only the original decorated pure OnMouse command.
	case tea.QuitMsg:
		if !a.quitRequested {
			panic("unarmed filter shell quit")
		}
		a.quitObserved = true
		return msg
	default:
		return nil // No timers/streams/Raw/exec/clipboard/Batch/unknown commands.
	}
}
func (a *terminalFilterShellFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.inputs++
	if a.inputs > 64 {
		panic("filter shell input budget")
	}
	m := a.initial
	uiCommand := false
	switch v := msg.(type) {
	case tea.ColorProfileMsg, tea.EnvMsg:
		return a, nil
	case tea.WindowSizeMsg:
		a.sizes++
		if a.sizes > 4 {
			panic("filter shell size budget")
		}
	case tea.KeyPressMsg:
		if v.String() == "down" {
			a.downs++
		}
		uiCommand = v.String() == "/" || m.inputMode == "filter" && v.String() == "q"
	case tea.PasteMsg:
		a.pastes++
		uiCommand = true
	case tea.MouseClickMsg, tea.MouseWheelMsg:
		return a, nil // Framework invokes the original View.OnMouse separately.
	case dashboardMouseMsg:
		frame := m.mouseFrame()
		if v.wheel != 0 {
			frame = m.mouseScrollFrame()
		}
		if v.frame != frame {
			panic("filter shell original frame changed")
		}
		a.diag.event("typed-frame", 0, 0, 0, true)
		if v.wheel != 0 {
			if v.wheel != 3 || v.region.feed || v.region.key != "" {
				panic("filter shell typed wheel")
			}
		} else if v.region.key != "ctrl+c" || v.region.y != 13 {
			panic("filter shell typed footer")
		}
		a.delivered++
		a.f.mouseCommands++
		a.diag.event("typed-delivery", 0, 0, 0, true)
	default:
		panic("filter shell unreviewed update")
	}
	inner, cmd := m.Update(msg) // Retain native KeyPressMsg.Text; never f.key/mouseKey.
	if inner != a.initial {
		panic("filter shell model identity")
	}
	a.f.invariant()
	if m.view != 2 {
		panic("filter shell view changed")
	}
	if len(a.f.s.calls) != 0 || a.f.reads != 0 {
		panic("filter shell unexpected fake operation")
	}
	if mouse, ok := msg.(dashboardMouseMsg); ok && mouse.wheel == 0 {
		if cmd == nil || reflect.ValueOf(cmd).Pointer() != reflect.ValueOf(tea.Quit).Pointer() {
			panic("filter shell footer did not return original Quit")
		}
		a.quitRequested = true
		return a, cmd // Exact original named Quit; Program executes it, once.
	}
	if cmd != nil {
		if !uiCommand {
			panic("filter shell unknown command")
		}
		a.f.suppressed["filter-input-ui"]++ // Known focus/cursor result, never scheduled.
	}
	return a, nil
}
func (a *terminalFilterShellFixture) View() tea.View {
	m := a.initial
	view := m.View()
	original := view.OnMouse
	if original == nil {
		panic("filter shell original callback missing")
	}
	generation := a.revision + 1
	view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		p := msg.Mouse()
		a.diag.event("callback-entry", generation, p.X, p.Y, true)
		cmd := original(msg)
		if cmd == nil {
			a.diag.event("cmd-nil", generation, p.X, p.Y, true)
			return nil
		}
		a.diag.event("cmd-created", generation, p.X, p.Y, true)
		return func() tea.Msg {
			a.active.Add(1)
			defer a.active.Add(-1)
			a.diag.event("cmd-start", generation, p.X, p.Y, true)
			value := cmd() // Actual original pure command; no fabricated mouse event.
			if _, ok := value.(dashboardMouseMsg); !ok {
				panic("filter shell callback type")
			}
			a.produced.Add(1)
			a.diag.event("cmd-produced", generation, p.X, p.Y, true)
			return value
		}
	}
	a.revision++
	if a.revision > 128 {
		panic("filter shell view budget")
	}
	regions := []map[string]any{}
	var exit [4]int
	for _, r := range m.mouseRegions() {
		regions = append(regions, map[string]any{"x": r.x, "y": r.y, "width": r.width, "height": r.height, "key": r.key, "row": r.row, "feed": r.feed, "record": r.record, "mesh": r.mesh, "pool": r.pool, "recovery": r.recovery, "picker": r.picker})
		if r.key == "q" && r.y == 13 {
			exit = [4]int{r.x, r.y, r.width, r.height}
		}
	}
	ids := []string{}
	for _, lease := range m.filteredLeases() {
		ids = append(ids, lease.ID)
	}
	// Startup fingerprint deliberately contains stable geometry/default/ID data,
	// not moving age/clock labels. Actual typed commands retain full original frames.
	raw, _ := json.Marshal(struct {
		Grid   [2]int
		Region [4]int
		IDs    []string
	}{[2]int{45, 14}, exit, ids})
	h := sha256.Sum256(raw)
	a.diag.startup(leaseStartupFrame{Fingerprint: hex.EncodeToString(h[:]), Generation: generation, Grid: [2]int{45, 14}, Region: exit, Valid: m.width == 45 && m.height == 14 && m.selected == 0 && m.inputMode == "" && len(ids) == 8 && exit[2] > 0})
	rows := ansi.Strip(m.viewport.View())
	state := map[string]any{"revision": a.revision, "inputs": a.inputs, "view": m.view, "grid": [2]int{m.width, m.height}, "viewContent": view.Content, "altScreenFlag": view.AltScreen, "regions": regions, "initSuppressed": a.initSuppressed, "sameModel": m == a.initial, "nativeSizeMessages": a.sizes, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "downs": a.downs, "pastes": a.pastes, "wheelInputs": a.wheels, "footerInputs": a.clicks, "readClosures": a.f.reads, "calls": a.f.s.calls, "defaultLeases": m.hideInactive, "defaultRuns": m.hideInactiveRuns, "pending": m.pending, "unknownEmpty": m.unknownLease == "", "inputMode": m.inputMode, "inputValue": m.input.Value(), "inputFocused": m.input.Focused(), "filter": m.filter, "selected": m.selected, "visibleLeaseIDs": ids, "viewport": map[string]any{"offset": m.viewport.YOffset(), "width": m.viewport.Width(), "height": m.viewport.Height(), "total": m.viewport.TotalLineCount(), "rows": rows}, "draftVisibleInViewport": m.inputMode == "filter" && m.input.Value() != "" && strings.Contains(rows, m.input.Value()), "diagnostics": a.diag.summary()}
	if m.inputMode == "filter" {
		w, h := m.modalGeometry()
		state["modal"] = map[string]int{"x": (m.mainWidth() - w) / 2, "y": 4 + (m.bodyHeight()-h)/2, "width": w, "height": h}
	}
	encoded, err := json.Marshal(state)
	if err != nil || len(encoded) > 32768 {
		panic("filter shell progress bound")
	}
	loopFixtureWrite(a.f.t, a.progress, state)
	return view
}

// A separate passive writer uses a45-compatible recognition rule. The accepted
// lease writer/recognizer/healthy() method is not modified or reused as proof.
type filterShellOutput struct {
	mu   sync.Mutex
	file leaseOutputFile
	diag *leaseReadiness
}

var _ term.File = (*filterShellOutput)(nil)

func (o *filterShellOutput) Read(p []byte) (int, error) { return o.file.Read(p) }
func (o *filterShellOutput) Close() error               { return o.file.Close() }
func (o *filterShellOutput) Fd() uintptr                { return o.file.Fd() }
func (o *filterShellOutput) Name() string               { return o.file.Name() }
func (o *filterShellOutput) observe(p []byte, n int, err error) {
	defer func() {
		if recover() != nil {
			o.diag.markUnknown()
		}
	}()
	d := o.diag
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.Sequence++
	d.state.Writes++
	d.state.LastWrite = &leaseWriteOutcome{Sequence: d.state.Writes, Requested: len(p), Returned: n, ErrNil: err == nil, ErrorType: fmt.Sprintf("%T", err), ReturnedAtNS: time.Since(d.started).Nanoseconds()}
	start := d.state.OutputBytes
	if n < 0 || n > len(p) {
		d.markUnknown()
		return
	}
	d.state.OutputBytes += int64(n)
	if err != nil || n != len(p) {
		d.markUnknown()
		return
	}
	full := true
	for _, token := range [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1002h"), []byte("\x1b[?1006h"), []byte("[q] Exit"), []byte("Inspect")} {
		full = full && bytes.Contains(p, token)
	}
	if d.state.Frame == nil && !d.state.Startup.Sealed && d.state.Startup.Valid && !d.state.Startup.Drift && full {
		h := sha256.Sum256(p)
		d.state.Frame = &leaseOutputRange{Sequence: d.state.Writes, Start: start, End: d.state.OutputBytes, Requested: len(p), Returned: n, SHA256: hex.EncodeToString(h[:]), Full: true, Fingerprint: d.state.Startup.Fingerprint, ReturnedAtNS: time.Since(d.started).Nanoseconds()}
	}
}
func (o *filterShellOutput) Write(p []byte) (n int, err error) {
	o.mu.Lock()
	func() { defer o.mu.Unlock(); n, err = o.file.Write(p); o.observe(p, n, err) }()
	o.diag.publish() // No observer/output lock during metadata effects.
	return n, err
}
func (o *filterShellOutput) descriptors(grid [2]int) {
	w, h, err := term.GetSize(o.file.Fd())
	values := map[string]bool{"inputUnwrapped": true, "inputTerminal": term.IsTerminal(os.Stdin.Fd()), "outputTerminal": term.IsTerminal(o.file.Fd()), "sameOutputFD": o.Fd() == o.file.Fd(), "sameOutputName": o.Name() == o.file.Name(), "termFilePreserved": true, "sizeMatches": err == nil && [2]int{w, h} == grid}
	d := o.diag
	d.mu.Lock()
	d.state.Descriptor = values
	d.state.Sequence++
	d.mu.Unlock()
	for _, ok := range values {
		if !ok {
			d.markUnknown()
		}
	}
	d.publish()
}
func filterShellGuard(t *testing.T, repo string) {
	t.Helper()
	var b struct {
		CurrentSource struct {
			ExistingMemberSHA256 map[string]string `json:"existingMemberSHA256"`
		} `json:"currentSource"`
		SourceBinding struct {
			RequiredEntries int      `json:"requiredEntries"`
			Members         []string `json:"members"`
			TestSelector    string   `json:"testSelector"`
		} `json:"sourceBinding"`
	}
	raw, err := os.ReadFile(filepath.Join(repo, filterShellBinding))
	h := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(h[:]) != filterShellBindingSHA || json.Unmarshal(raw, &b) != nil || len(b.CurrentSource.ExistingMemberSHA256) != 65 || b.SourceBinding.RequiredEntries != 69 || len(b.SourceBinding.Members) != 69 || b.SourceBinding.TestSelector != filterShellSelector {
		t.Fatal("filter shell source binding")
	}
	seen := map[string]bool{}
	for _, name := range b.SourceBinding.Members {
		if seen[name] || filepath.IsAbs(name) || strings.Contains(name, "..") {
			t.Fatal("filter shell member roster")
		}
		seen[name] = true
	}
	for name, want := range b.CurrentSource.ExistingMemberSHA256 {
		raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(name)))
		h := sha256.Sum256(raw)
		if !seen[name] || err != nil || hex.EncodeToString(h[:]) != want {
			t.Fatal("filter shell existing source drift")
		}
	}
}
func prepareFilterShell(t *testing.T, root string) *directFixture {
	t.Helper()
	grid := "45x14"
	dir := filepath.Join(root, grid)
	home := filepath.Join(dir, "synthetic-home")
	if os.Mkdir(dir, 0700) != nil || os.Mkdir(home, 0700) != nil {
		t.Fatal("fresh filter shell home")
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m := newDashboard(ctx, home)
	data := newDirectFixtureData(t)
	s := &directFixtureTransport{t: t, data: directFixtureClone(t, data)}
	f := &directFixture{t: t, m: m, s: s, cancel: cancel, root: root, home: home, grid: grid, report: &directFixtureReport{Scope: "Native45filter source-only fixture"}, suppressed: map[string]int{}}
	t.Cleanup(f.close)
	if m.pendingStoreError != "" || len(m.pendingRecords) != 0 {
		t.Fatal("filter shell constructor bookkeeping")
	}
	m.session = s
	m.connected = true
	m.connecting = false
	m.view = 2
	m.data.status = model.Status{Host: data.Host, Owner: "1000", OwnerUsername: "synthetic-user", Leases: directFixtureClone(t, data.Leases)}
	m.data.names = renderLabels{authority: data.Host, hostName: "Synthetic host"}
	for i, l := range data.Leases {
		m.data.names.addLease(l, fmt.Sprintf("Synthetic lease %d", i+1), "Synthetic host")
	}
	m.notice = "Synthetic filter fixture; no live backend."
	m.lastRefresh = m.timeNow
	inner, cmd := m.Update(tea.WindowSizeMsg{Width: 45, Height: 14}) // Disclosed arrangement; native startup size is observed separately.
	if inner != m || cmd != nil {
		t.Fatal("filter shell original arranged size")
	}
	f.invariant()
	return f
}
func TestUXTerminalFilterShellFixture(t *testing.T) {
	root := os.Getenv("ARM_TUI_FILTER_SHELL_ROOT")
	if root == "" {
		t.Skip("native45 filter source requires separate build/native allocation")
	}
	info, err := os.Lstat(root)
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 || os.Getenv("ARM_TUI_FILTER_SHELL_GRID") != "45x14" {
		t.Fatal("private exact native45 root/grid")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("filter shell cwd")
	}
	filterShellGuard(t, filepath.Clean(filepath.Join(cwd, "..", "..")))
	f := prepareFilterShell(t, root)
	a := &terminalFilterShellFixture{f: f, initial: f.m, progress: filepath.Join(root, "45x14", "progress.json"), diag: newLeaseReadiness(filepath.Join(root, "45x14", "readiness.json"))}
	ctx, cancel := context.WithTimeout(f.m.ctx, 25*time.Second)
	defer cancel()
	output := &filterShellOutput{file: os.Stdout, diag: a.diag}
	output.descriptors([2]int{45, 14})
	p := tea.NewProgram(a, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(output), tea.WithoutSignalHandler(), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithFilter(a.filter))
	started := false
	defer func() {
		if started && !a.returned {
			p.Kill()
			p.Wait()
		}
		cancel()
		f.close()
		loopFixtureWrite(t, filepath.Join(root, "45x14", "child-final.json"), map[string]any{"testFailed": t.Failed(), "programStarted": started, "programReturned": a.returned, "sameWrapperReturned": a.sameWrapper, "sameModel": f.m == a.initial, "quitRequested": a.quitRequested, "quitMsgObserved": a.quitObserved, "initSuppressed": a.initSuppressed, "nativeSizeMessages": a.sizes, "pastes": a.pastes, "wheelInputs": a.wheels, "footerInputs": a.clicks, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "reads": f.reads, "calls": f.s.calls, "forbidden": f.s.forbidden, "activeFakeCalls": f.s.active, "fakeClosed": f.s.closed, "fakeCloses": f.s.closes, "contextCanceled": f.m.ctx.Err() != nil, "inputMode": f.m.inputMode, "inputValue": f.m.input.Value(), "inputFocused": f.m.input.Focused(), "filter": f.m.filter, "diagnostics": a.diag.summary(), "cleanupQualification": "Original Program/fake/context/PTY observations only; no production startup or universal reap"})
	}()
	started = true
	final, runErr := p.Run()
	a.returned = true
	a.sameWrapper = final == a
	a.diag.program(runErr)
	summary := a.diag.summary()
	a.diag.mu.Lock()
	frame := a.diag.state.Frame
	startup := a.diag.state.Startup
	counts := map[string]int{}
	for key, value := range a.diag.state.Counters {
		counts[key] = value
	}
	a.diag.mu.Unlock()
	if runErr != nil || !a.sameWrapper || !a.quitRequested || !a.quitObserved || !a.initSuppressed || a.sizes < 1 || a.pastes != 2 || a.wheels != 2 || a.clicks != 1 || a.produced.Load() != 3 || a.delivered != 3 || a.active.Load() != 0 || len(f.s.calls) != 0 || f.reads != 0 || summary["unknown"] != false || frame == nil || !startup.Sealed || startup.Drift || summary["programResult"] != "nil" || counts["callback-entry"] != 3 || counts["cmd-created"] != 3 || counts["cmd-start"] != 3 || counts["cmd-produced"] != 3 || counts["typed-delivery"] != 3 || counts["typed-frame"] != 3 || counts["cmd-nil"] != 0 || f.m.inputMode != "filter" || f.m.input.Value() != "probe-draftq" || f.m.filter != "probe-draftq" || !f.m.input.Focused() {
		t.Error("original native45 filter lifecycle incomplete")
	}
}
