package tui

// SOURCE PREPARATION ONLY: separate source/build/PTY/browser review required.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

const leaseReadingFilter = "no-synthetic-match"

// Keeps the original model and unchanged strict direct helper. Init never
// starts native transport, clocks, streams, reconnect or arbitrary commands.
type terminalLeaseReadingFixture struct {
	diag                                                     *leaseReadiness
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
		a.diag.event("startup-size", 0, 0, 0, false)
		if [2]int{v.Width, v.Height} != a.grid {
			panic("lease reading grid drift")
		}
		return msg
	case tea.ColorProfileMsg, tea.EnvMsg:
		a.diag.event("startup-profile-env", 0, 0, 0, false)
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
		mouse := v.Mouse()
		a.diag.event("parsed-click", 0, mouse.X, mouse.Y, true)
		if !a.safeMouse(v) {
			a.diag.event("filter-rejected", 0, mouse.X, mouse.Y, true)
			panic("lease reading mouse refused")
		}
		a.diag.seal()
		a.diag.event("filter-admitted", 0, mouse.X, mouse.Y, true)
		return msg
	case dashboardMouseMsg:
		return msg // Only decorated original pure OnMouse Cmd.
	case tea.QuitMsg:
		if !a.quitRequested {
			panic("unarmed lease reading quit")
		}
		return msg
	case tea.MouseReleaseMsg:
		a.diag.event("parsed-release", 0, 0, 0, false)
		a.ignored++
		return nil
	case tea.MouseMotionMsg:
		a.diag.event("parsed-motion", 0, 0, 0, false)
		a.ignored++
		return nil
	case tea.MouseWheelMsg:
		a.diag.event("parsed-wheel", 0, 0, 0, false)
		a.ignored++
		return nil
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
		a.diag.event("update-click", 0, v.Mouse().X, v.Mouse().Y, true)
	case dashboardMouseMsg:
		a.diag.event("typed-delivery", 0, 0, 0, true)
		a.delivered++
		a.f.mouseCommands++
		policy := directNoCommand
		reviewing := m.review != nil
		if v.frame == m.mouseFrame() {
			a.diag.event("typed-frame", 0, 0, 0, true)
			if reviewing && v.region.key == "n" {
				policy = directNoCommand
			} else if m.detail == nil && m.record == nil && !m.modal() && v.region.key == "" && v.region.row == 0 {
				policy = directInspect
			} else {
				panic("lease typed mouse target refused")
			}
		}
		readsBefore := a.f.reads
		a.f.update(v, policy, "lease-native-typed-mouse")
		if a.f.reads == readsBefore+1 {
			a.diag.event("inspect-complete", 0, 0, 0, true)
		}
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
	generation := a.revision + 1
	view.OnMouse = func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		a.diag.event("callback-entry", generation, mouse.X, mouse.Y, true)
		cmd := original(msg) // Original callback is never invoked under an observer lock.
		if cmd == nil {
			a.diag.event("cmd-nil", generation, mouse.X, mouse.Y, true)
			return nil
		}
		a.diag.event("cmd-created", generation, mouse.X, mouse.Y, true)
		return func() tea.Msg {
			a.diag.event("cmd-start", generation, mouse.X, mouse.Y, true)
			a.active.Add(1)
			defer a.active.Add(-1)
			value := cmd()
			if _, ok := value.(dashboardMouseMsg); !ok {
				panic("lease unreviewed mouse Cmd")
			}
			a.produced.Add(1)
			a.diag.event("cmd-produced", generation, mouse.X, mouse.Y, true)
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
	a.diag.startup(a.startupFrame(generation, ids))
	state["diagnostics"] = a.diag.summary()
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
	a.diag = newLeaseReadiness(filepath.Join(root, f.grid, "readiness.json"))
	observedOutput := &leaseObservedOutput{file: os.Stdout, diag: a.diag}
	a.diag.descriptors(os.Stdout, observedOutput, grid)
	p := tea.NewProgram(a, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(observedOutput), tea.WithoutSignalHandler(), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithFilter(a.filter))
	started := false
	defer func() {
		if started {
			p.Kill()
			p.Wait()
		}
		cancel()
		f.close()
		loopFixtureWrite(t, filepath.Join(root, f.grid, "child-final.json"), map[string]any{"testFailed": t.Failed(), "programStarted": started, "programReturned": a.returned, "sameWrapperReturned": a.sameWrapper, "sameModel": a.f.m == a.initial, "restored": a.restored(), "quitRequested": a.quitRequested, "initSuppressed": a.initSuppressed, "sawExactNumber": a.sawExactNumber, "sawReview": a.sawReview, "declined": a.declined, "pastes": a.pastes, "recordKeys": a.recordKeys, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "reads": f.reads, "calls": f.s.calls, "forbidden": f.s.forbidden, "activeFakeCalls": f.s.active, "fakeClosed": f.s.closed, "contextCanceled": f.m.ctx.Err() != nil, "diagnostics": a.diag.summary(), "cleanupQualification": "Original Program/fake/context/Popen/PTY observations only; no blanket goroutine/descendant reap or production startup/auth"})
	}()
	started = true
	final, runErr := p.Run()
	a.returned = true
	a.diag.program(runErr)
	a.sameWrapper = final == a
	if !a.diag.healthy() || runErr != nil || !a.sameWrapper || !a.quitRequested || !a.restored() || !a.sawExactNumber || !a.sawReview || !a.declined || a.pastes != 1 || a.recordKeys > 64 || a.active.Load() != 0 || a.produced.Load() != 2 || a.delivered != 2 || f.reads != 1 || len(f.s.calls) != 1 || f.s.calls[0].Operation != "inspect" {
		t.Error(fmt.Sprintf("lease reading original outcome incomplete: %T", runErr))
	}
}

// A fresh sibling binding admits only the reviewed product and regression delta.
func TestUXTerminalModalShadowFixture(t *testing.T) {
	root := os.Getenv("ARM_TUI_MODAL_SHADOW_ROOT")
	if root == "" {
		t.Skip("reviewed modal shadow source requires separate build/PTY allocation")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("resolved private lease root required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("private lease root required")
	}
	if os.Getenv("ARM_TUI_MODAL_SHADOW_GRID") != "72x28" {
		t.Fatal("exact modal shadow grid required")
	}
	grid := [2]int{72, 28}
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
		t.Fatal("modal shadow original classification guard")
	}
	var binding struct {
		Schema                int               `json:"schema"`
		SourceOverrides       map[string]string `json:"sourceOverrides"`
		EffectiveSourceSHA256 map[string]string `json:"effectiveSourceSHA256"`
		SourceBinding         struct {
			RequiredEntries int      `json:"requiredEntries"`
			Members         []string `json:"members"`
			TestSelector    string   `json:"testSelector"`
		} `json:"sourceBinding"`
	}
	raw, err = os.ReadFile(filepath.Join(repo, "docs/validation/ux-terminal-modal-shadow-binding-2026-10-30.json"))
	sum = sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != "c6c6858e9b97dd4a71bc54f05ab1c22a1856b5ef730c3c92e7c51019965b532d" || json.Unmarshal(raw, &binding) != nil || binding.Schema != 1 || len(binding.SourceOverrides) != 2 || len(binding.EffectiveSourceSHA256) != 50 || binding.SourceBinding.RequiredEntries != 63 || len(binding.SourceBinding.Members) != 63 || binding.SourceBinding.TestSelector != "^TestUXTerminalModalShadowFixture$" {
		t.Fatal("modal shadow sibling binding guard")
	}
	wantOverrides := map[string]string{
		"internal/tui/presentation.go": "5c06dca64d42f7c8f27905fe2e4b7f2125671eabcc2dbf9a5c895c8697ccc466",
		"internal/tui/polish_test.go":  "04ff15993dbdac1a7d7866aaa429587df6f3364e5730e61d4c9374a204d90a34",
	}
	for file, want := range wantOverrides {
		if binding.SourceOverrides[file] != want {
			t.Fatal("modal shadow override drift")
		}
	}
	for file, item := range guard.SourceEntries {
		want := item.SHA256
		if override, ok := wantOverrides[file]; ok {
			want = override
		}
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		h := sha256.Sum256(b)
		if e != nil || binding.EffectiveSourceSHA256[file] != want || hex.EncodeToString(h[:]) != want {
			t.Fatal("modal shadow effective source drift")
		}
	}
	for file, want := range map[string]string{"internal/tui/ux_terminal_direct_fixture_test.go": "ef37d0bdf89bce2dcd68ae7b2de2961f046c57f868edcb9d2e8ccc5952e37420", "internal/tui/ux_terminal_loop_fixture_test.go": "1dce0f1852791781f941eff7086c3cb6420c7d1305f87868182ae3f0a0ded168", "scripts/dashboard-screenshots/ux-terminal-loop-capture.py": "980fc761382c0de2854f43decb854658c69462ac05ad5b899105585eac8231d6", "docs/validation/ux-terminal-lease-reading-plan-2026-10-30.json": "10c195a224dd78573bf11320efa32e559f09635a7431a03fa232cc1a2b5746f9"} {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		h := sha256.Sum256(b)
		if e != nil || hex.EncodeToString(h[:]) != want {
			t.Fatal("lease frozen helper drift")
		}
	}
	report := &directFixtureReport{Scope: "Fixed72x28 modal shadow Program/PTY wrapper; no mutation/native transport"}
	f := prepareDirectFixture(t, root, grid, newDirectFixtureData(t), report)
	a := &terminalLeaseReadingFixture{f: f, initial: f.m, progress: filepath.Join(root, f.grid, "progress.json"), grid: grid, expectedID: f.s.data.Leases[0].ID}
	ctx, cancel := context.WithTimeout(f.m.ctx, 25*time.Second)
	defer cancel()
	a.diag = newLeaseReadiness(filepath.Join(root, f.grid, "readiness.json"))
	observedOutput := &leaseObservedOutput{file: os.Stdout, diag: a.diag}
	a.diag.descriptors(os.Stdout, observedOutput, grid)
	p := tea.NewProgram(a, tea.WithContext(ctx), tea.WithInput(os.Stdin), tea.WithOutput(observedOutput), tea.WithoutSignalHandler(), tea.WithColorProfile(colorprofile.TrueColor), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}), tea.WithFilter(a.filter))
	started := false
	defer func() {
		if started {
			p.Kill()
			p.Wait()
		}
		cancel()
		f.close()
		loopFixtureWrite(t, filepath.Join(root, f.grid, "child-final.json"), map[string]any{"testFailed": t.Failed(), "programStarted": started, "programReturned": a.returned, "sameWrapperReturned": a.sameWrapper, "sameModel": a.f.m == a.initial, "restored": a.restored(), "quitRequested": a.quitRequested, "initSuppressed": a.initSuppressed, "sawExactNumber": a.sawExactNumber, "sawReview": a.sawReview, "declined": a.declined, "pastes": a.pastes, "recordKeys": a.recordKeys, "mouseProduced": a.produced.Load(), "mouseDelivered": a.delivered, "mouseActive": a.active.Load(), "reads": f.reads, "calls": f.s.calls, "forbidden": f.s.forbidden, "activeFakeCalls": f.s.active, "fakeClosed": f.s.closed, "contextCanceled": f.m.ctx.Err() != nil, "diagnostics": a.diag.summary(), "cleanupQualification": "Original Program/fake/context/Popen/PTY observations only; no blanket goroutine/descendant reap or production startup/auth"})
	}()
	started = true
	final, runErr := p.Run()
	a.returned = true
	a.diag.program(runErr)
	a.sameWrapper = final == a
	if !a.diag.healthy() || runErr != nil || !a.sameWrapper || !a.quitRequested || !a.restored() || !a.sawExactNumber || !a.sawReview || !a.declined || a.pastes != 1 || a.recordKeys > 64 || a.active.Load() != 0 || a.produced.Load() != 2 || a.delivered != 2 || f.reads != 1 || len(f.s.calls) != 1 || f.s.calls[0].Operation != "inspect" {
		t.Error(fmt.Sprintf("lease reading original outcome incomplete: %T", runErr))
	}
}

func (a *terminalLeaseReadingFixture) startupFrame(generation int, ids []string) leaseStartupFrame {
	m := a.f.m
	var region [4]int
	found := false
	for _, r := range m.mouseRegions() {
		if r.key == "" && r.row == 0 && !r.feed && !r.record && !r.mesh && !r.pool && !r.recovery && !r.picker && r.x >= 0 && r.y >= 0 && r.width > 0 && r.height > 0 && r.x+r.width <= m.width && r.y+r.height <= m.height {
			region = [4]int{r.x, r.y, r.width, r.height}
			found = true
			break
		}
	}
	raw, _ := json.Marshal(struct {
		Grid            [2]int
		Region          [4]int
		Frame, Selected string
		IDs             []string
	}{a.grid, region, m.mouseFrame(), m.selectionID(), ids})
	h := sha256.Sum256(raw)
	return leaseStartupFrame{Fingerprint: hex.EncodeToString(h[:]), Generation: generation, Grid: a.grid, Region: region, Valid: found && a.restored() && m.selected == 0 && len(ids) == 8}
}

// Readiness observations are test-only. They never supply input or screen bytes.
const leaseDiagnosticBytes = 16 * 1024
const leaseDiagnosticRecords = 48 // Parent actions have a separate sixteen-record cap.

type leaseOutputFile interface {
	io.ReadWriteCloser
	Fd() uintptr
	Name() string
}

type leaseStartupFrame struct {
	Fingerprint string `json:"fingerprint"`
	Generation  int    `json:"generation"`
	Grid        [2]int `json:"grid"`
	Region      [4]int `json:"region"`
	Valid       bool   `json:"valid"`
	Drift       bool   `json:"drift"`
	Sealed      bool   `json:"sealed"`
}
type leaseOutputRange struct {
	Sequence     uint64 `json:"sequence"`
	Start        int64  `json:"start"`
	End          int64  `json:"end"`
	Requested    int    `json:"requested"`
	Returned     int    `json:"returned"`
	SHA256       string `json:"sha256"`
	Full         bool   `json:"full"`
	Fingerprint  string `json:"fingerprint"`
	ReturnedAtNS int64  `json:"returnedAtNS"`
}
type leaseWriteOutcome struct {
	Sequence     uint64 `json:"sequence"`
	Requested    int    `json:"requested"`
	Returned     int    `json:"returned"`
	ErrNil       bool   `json:"errNil"`
	ErrorType    string `json:"errorType"`
	ReturnedAtNS int64  `json:"returnedAtNS"`
}
type leaseDiagnosticRecord struct {
	Stage      string `json:"stage"`
	Generation int    `json:"generation"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	AtNS       int64  `json:"atNS"`
}
type leaseDiagnosticState struct {
	Schema        int                     `json:"schema"`
	Sequence      uint64                  `json:"sequence"`
	Unknown       bool                    `json:"unknown"`
	Startup       leaseStartupFrame       `json:"startup"`
	Frame         *leaseOutputRange       `json:"frame"`
	LastWrite     *leaseWriteOutcome      `json:"lastWrite"`
	Writes        uint64                  `json:"writes"`
	OutputBytes   int64                   `json:"outputBytes"`
	Counters      map[string]int          `json:"counters"`
	Records       []leaseDiagnosticRecord `json:"records"`
	Descriptor    map[string]bool         `json:"descriptor"`
	ProgramResult string                  `json:"programResult"`
}
type leaseReadiness struct {
	mu             sync.Mutex
	publishMu      sync.Mutex // Concrete file operations only; no model or injected callback.
	started        time.Time
	path           string
	state          leaseDiagnosticState
	unknown        atomic.Bool
	metadataFailed atomic.Bool
	metadataHook   func([]byte) error // Regression-only hook, invoked with no observer lock held.
}

func newLeaseReadiness(path string) *leaseReadiness {
	return &leaseReadiness{path: path, started: time.Now(), state: leaseDiagnosticState{Schema: 1, Counters: map[string]int{}, Records: []leaseDiagnosticRecord{}}}
}
func (d *leaseReadiness) markUnknown() { d.unknown.Store(true) }
func (d *leaseReadiness) event(stage string, generation, x, y int, record bool) {
	// Diagnostic failure cannot replace an original callback/command outcome.
	defer func() {
		if recover() != nil {
			d.markUnknown()
		}
	}()
	d.mu.Lock()
	func() {
		defer d.mu.Unlock()
		d.state.Sequence++
		d.state.Counters[stage]++
		if record {
			if len(d.state.Records) >= leaseDiagnosticRecords {
				d.markUnknown()
				return
			}
			d.state.Records = append(d.state.Records, leaseDiagnosticRecord{stage, generation, x, y, time.Since(d.started).Nanoseconds()})
		}
	}()
	d.publish()
}
func (d *leaseReadiness) startup(frame leaseStartupFrame) {
	d.mu.Lock()
	if !d.state.Startup.Sealed {
		frame.Drift = d.state.Startup.Drift
		if d.state.Startup.Fingerprint != "" && d.state.Startup.Fingerprint != frame.Fingerprint {
			frame.Drift = true
			d.markUnknown()
		}
		d.state.Startup = frame
		d.state.Sequence++
	}
	d.mu.Unlock()
	d.publish()
}
func (d *leaseReadiness) seal() {
	d.mu.Lock()
	d.state.Startup.Sealed = true
	d.state.Sequence++
	d.mu.Unlock()
	d.publish()
}
func leaseInitialRendererWrite(p []byte) bool {
	// Capability queries alone cannot satisfy these renderer-mode and roster gates.
	for _, token := range [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1002h"), []byte("\x1b[?1006h"), []byte("LEASES"), []byte("Synthetic lease 1"), []byte("Inspect")} {
		if !bytes.Contains(p, token) {
			return false
		}
	}
	return true
}
func (d *leaseReadiness) output(p []byte, n int, err error) {
	defer func() {
		if recover() != nil {
			d.markUnknown()
		}
	}()
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
	if d.state.Frame == nil && !d.state.Startup.Sealed && d.state.Startup.Valid && !d.state.Startup.Drift && leaseInitialRendererWrite(p) {
		h := sha256.Sum256(p)
		d.state.Frame = &leaseOutputRange{Sequence: d.state.Writes, Start: start, End: d.state.OutputBytes, Requested: len(p), Returned: n, SHA256: hex.EncodeToString(h[:]), Full: true, Fingerprint: d.state.Startup.Fingerprint, ReturnedAtNS: time.Since(d.started).Nanoseconds()}
	}
}
func (d *leaseReadiness) encoded() ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state.Unknown = d.unknown.Load()
	raw, err := json.Marshal(d.state)
	if err == nil && len(raw)+1 > leaseDiagnosticBytes {
		return nil, fmt.Errorf("diagnostic byte bound")
	}
	return append(raw, '\n'), err
}
func (d *leaseReadiness) publish() {
	if d.metadataFailed.Load() {
		return
	} // First failed observation remains unknown; no metadata retry.
	defer func() {
		if recover() != nil {
			d.markUnknown()
			d.metadataFailed.Store(true)
		}
	}()
	if d.metadataHook != nil {
		raw, err := d.encoded()
		if err == nil {
			err = d.metadataHook(raw)
		} // No write/state/publish lock is held here.
		if err != nil {
			d.markUnknown()
			d.metadataFailed.Store(true)
		}
		return
	}
	d.publishMu.Lock()
	defer d.publishMu.Unlock()
	if d.metadataFailed.Load() {
		return
	}
	raw, err := d.encoded() // Snapshot after serialization, so a late publisher never rewinds observations.
	if err == nil {
		var f *os.File
		f, err = os.OpenFile(d.path+".new", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			written, writeErr := f.Write(raw)
			err = writeErr
			if err == nil && written != len(raw) {
				err = io.ErrShortWrite
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				err = os.Rename(d.path+".new", d.path)
			}
		}
	}
	if err != nil {
		d.markUnknown()
		d.metadataFailed.Store(true)
	} // Retain any original partial .new file.
}
func (d *leaseReadiness) summary() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	counts := map[string]int{}
	for k, v := range d.state.Counters {
		counts[k] = v
	}
	summary := map[string]any{"unknown": d.unknown.Load(), "metadataFailed": d.metadataFailed.Load(), "sequence": d.state.Sequence, "fingerprint": d.state.Startup.Fingerprint, "startupDrift": d.state.Startup.Drift, "frameObserved": d.state.Frame != nil, "records": len(d.state.Records), "counters": counts, "programResult": d.state.ProgramResult}
	raw, err := json.Marshal(summary)
	if err != nil || len(raw) > 2048 {
		d.markUnknown()
		return map[string]any{"unknown": true, "summaryOverflow": true}
	}
	return summary
}
func (d *leaseReadiness) descriptors(original leaseOutputFile, observed *leaseObservedOutput, grid [2]int) {
	w, h, err := term.GetSize(original.Fd())
	values := map[string]bool{"inputUnwrapped": true, "inputTerminal": term.IsTerminal(os.Stdin.Fd()), "outputTerminal": term.IsTerminal(original.Fd()), "sameOutputFD": observed.Fd() == original.Fd(), "sameOutputName": observed.Name() == original.Name(), "termFilePreserved": true, "sizeMatches": err == nil && [2]int{w, h} == grid}
	d.mu.Lock()
	d.state.Descriptor = values
	d.state.Sequence++
	d.mu.Unlock()
	for _, v := range values {
		if !v {
			d.markUnknown()
		}
	}
	d.publish()
}
func (d *leaseReadiness) program(err error) {
	kind := "nil"
	if err != nil {
		kind = "error"
		if errors.Is(err, tea.ErrProgramPanic) {
			kind = "panic"
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			kind = "context"
		}
	}
	d.mu.Lock()
	d.state.ProgramResult = kind
	d.state.Sequence++
	d.mu.Unlock()
	d.event("program-return", 0, 0, 0, true)
}
func (d *leaseReadiness) healthy() bool {
	if d.unknown.Load() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Frame == nil || !d.state.Startup.Valid || d.state.Startup.Drift || d.state.ProgramResult != "nil" {
		return false
	}
	for _, stage := range []string{"parsed-click", "filter-admitted", "update-click", "callback-entry", "cmd-created", "cmd-start", "cmd-produced", "typed-delivery", "typed-frame"} {
		if d.state.Counters[stage] != 2 {
			return false
		}
	}
	return d.state.Counters["inspect-complete"] == 1 && d.state.Counters["cmd-nil"] == 0
}

type leaseObservedOutput struct {
	mu   sync.Mutex // Original file write order and byte ranges share this serialization.
	file leaseOutputFile
	diag *leaseReadiness
}

var _ term.File = (*leaseObservedOutput)(nil)

func (o *leaseObservedOutput) Read(p []byte) (int, error) { return o.file.Read(p) }
func (o *leaseObservedOutput) Close() error               { return o.file.Close() }
func (o *leaseObservedOutput) Fd() uintptr                { return o.file.Fd() }
func (o *leaseObservedOutput) Name() string               { return o.file.Name() }
func (o *leaseObservedOutput) Write(p []byte) (n int, err error) {
	o.mu.Lock()
	func() {
		defer o.mu.Unlock()
		n, err = o.file.Write(p) // Exactly once; original panic escapes unchanged and releases the mutex.
		o.diag.output(p, n, err) // Safe passive recording only; no model or callback.
	}()
	o.diag.publish() // Metadata errors/panics cannot change original (n,err); no observer write lock held.
	return n, err
}

// Regression source is prepared only. Execution needs a separate allocated test window.
type leaseOutputRegressionFile struct {
	write                 func([]byte) (int, error)
	readCalls, closeCalls int
}

func (f *leaseOutputRegressionFile) Write(p []byte) (int, error) { return f.write(p) }
func (f *leaseOutputRegressionFile) Read(p []byte) (int, error) {
	f.readCalls++
	return copy(p, "r"), io.EOF
}
func (f *leaseOutputRegressionFile) Close() error { f.closeCalls++; return io.EOF }
func (f *leaseOutputRegressionFile) Fd() uintptr  { return 123 }
func (f *leaseOutputRegressionFile) Name() string { return "synthetic-output" }
func TestUXTerminalLeaseOutputPassThrough(t *testing.T) {
	sentinel := errors.New("synthetic-write-error")
	for _, test := range []struct {
		name    string
		n       int
		err     error
		unknown bool
	}{{"full", 4, nil, false}, {"short", 2, nil, true}, {"error", 1, sentinel, true}} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			var got []byte
			f := &leaseOutputRegressionFile{write: func(p []byte) (int, error) { calls++; got = append([]byte(nil), p...); return test.n, test.err }}
			d := newLeaseReadiness("")
			d.metadataHook = func([]byte) error { return nil }
			o := &leaseObservedOutput{file: f, diag: d}
			n, err := o.Write([]byte("same"))
			if n != test.n || err != test.err || calls != 1 || string(got) != "same" || d.unknown.Load() != test.unknown {
				t.Fatal("original write/result was changed")
			}
			if d.state.LastWrite == nil || d.state.LastWrite.Returned != test.n || d.state.LastWrite.ErrNil != (test.err == nil) {
				t.Fatal("original result observation lost")
			}
			b := make([]byte, 1)
			rn, re := o.Read(b)
			if rn != 1 || re != io.EOF || string(b) != "r" || o.Close() != io.EOF || f.readCalls != 1 || f.closeCalls != 1 || o.Fd() != f.Fd() || o.Name() != f.Name() {
				t.Fatal("file interface forwarding changed")
			}
			if _, ok := any(o).(io.StringWriter); ok {
				t.Fatal("unobserved string fast path")
			}
			if _, ok := any(o).(io.ReaderFrom); ok {
				t.Fatal("unobserved reader fast path")
			}
		})
	}
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprintf("metadata-panic-%v", panics), func(t *testing.T) {
			f := &leaseOutputRegressionFile{write: func(p []byte) (int, error) { return len(p), nil }}
			d := newLeaseReadiness("")
			o := &leaseObservedOutput{file: f, diag: d}
			calls := 0
			d.metadataHook = func([]byte) error {
				calls++
				if !o.mu.TryLock() {
					t.Fatal("metadata callback under output lock")
				}
				o.mu.Unlock()
				if !d.mu.TryLock() {
					t.Fatal("metadata callback under observation lock")
				}
				d.mu.Unlock()
				if !d.publishMu.TryLock() {
					t.Fatal("metadata callback under publication lock")
				}
				d.publishMu.Unlock()
				if panics {
					panic(sentinel)
				}
				return sentinel
			}
			for i := 0; i < 2; i++ {
				n, err := o.Write([]byte("same"))
				if n != 4 || err != nil {
					t.Fatal("metadata changed original result")
				}
			}
			if !d.unknown.Load() || !d.metadataFailed.Load() || calls != 1 {
				t.Fatal("metadata failure was not sticky unknown")
			}
		})
	}
	t.Run("metadata-file-error", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, "blocker")
		if err := os.WriteFile(blocker, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
		f := &leaseOutputRegressionFile{write: func(p []byte) (int, error) { return len(p), nil }}
		d := newLeaseReadiness(filepath.Join(blocker, "readiness.json"))
		o := &leaseObservedOutput{file: f, diag: d}
		n, err := o.Write([]byte("same"))
		if n != 4 || err != nil || !d.unknown.Load() || !d.metadataFailed.Load() {
			t.Fatal("file metadata failure changed original result or appeared known")
		}
		raw, err := os.ReadFile(blocker)
		if err != nil || string(raw) != "original" {
			t.Fatal("original metadata blocker changed")
		}
	})
	t.Run("original-panic", func(t *testing.T) {
		f := &leaseOutputRegressionFile{write: func([]byte) (int, error) { panic(sentinel) }}
		d := newLeaseReadiness("")
		observations := 0
		d.metadataHook = func([]byte) error { observations++; return nil }
		o := &leaseObservedOutput{file: f, diag: d}
		func() {
			defer func() {
				if recover() != sentinel {
					t.Error("original panic identity changed")
				}
			}()
			_, _ = o.Write([]byte("same"))
			t.Error("original panic swallowed")
		}()
		if observations != 0 || !o.mu.TryLock() {
			t.Fatal("panic path recorded false success or retained lock")
		}
		o.mu.Unlock()
	})
}

func TestUXTerminalLeaseInitialOutputRecognition(t *testing.T) {
	query := []byte("\x1b[?2026$p\x1b[?2027$p")
	if leaseInitialRendererWrite(query) {
		t.Fatal("query bytes accepted as a renderer frame")
	}
	frame := append(append([]byte(nil), query...), []byte("\x1b[?1049h\x1b[?1002h\x1b[?1006h LEASES Synthetic lease 1 Inspect")...)
	if !leaseInitialRendererWrite(frame) {
		t.Fatal("complete source-scoped initial output not recognized")
	}
	d := newLeaseReadiness("")
	d.metadataHook = func([]byte) error { return nil }
	d.startup(leaseStartupFrame{Fingerprint: "synthetic-fingerprint", Valid: true})
	f := &leaseOutputRegressionFile{write: func(p []byte) (int, error) { return len(p), nil }}
	o := &leaseObservedOutput{file: f, diag: d}
	if _, err := o.Write(query); err != nil {
		t.Fatal(err)
	}
	if d.state.Frame != nil {
		t.Fatal("query published a frame range")
	}
	if _, err := o.Write(frame); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(frame)
	r := d.state.Frame
	if r == nil || r.Start != int64(len(query)) || r.End != int64(len(query)+len(frame)) || r.SHA256 != hex.EncodeToString(sum[:]) || !r.Full {
		t.Fatal("original write range/hash changed")
	}
}
