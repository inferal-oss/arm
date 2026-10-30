package tui

// Source-prepared, opt-in inventory fixture. No production Init/Run/program,
// endpoint, clock/stream loop, PTY or arbitrary Cmd executor is used here.
// A separate review/allocation must precede compilation or execution.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

const directFixtureOutput = "ARM_TUI_DIRECT_FIXTURE_DIR"
const directFixtureCheckpointLimit = 48
const directFixtureDTOBytes = 64 << 10
const directFixtureLogBytes = 8 << 10
const directFixturePageBytes = 2 << 10

// All values are synthetic. Typed clone/decode avoids a float64/any number
// roundtrip. It is fixture isolation, not a replacement product read adapter.
func directFixtureClone[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > directFixtureDTOBytes {
		t.Fatal("synthetic typed clone exceeds its bound or cannot encode")
	}
	var copy T
	if json.Unmarshal(raw, &copy) != nil {
		t.Fatal("synthetic typed clone cannot decode")
	}
	return copy
}

type directFixtureData struct {
	Host   string
	Leases []model.Lease
	Runs   []model.Run
	Log    []byte
}

func newDirectFixtureData(t *testing.T) directFixtureData {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Second)
	d := directFixtureData{Host: strings.Repeat("a", 32)}
	leaseStates := []string{"active", "queued", "provisioning", "ending", "reclaiming", "needs_attention", "active", "future_state", "terminal", "terminal", "terminal", "terminal"}
	for i, state := range leaseStates {
		expires := at.Add(time.Hour)
		l := model.Lease{ID: fmt.Sprintf("%032x", i+1), Host: d.Host, Owner: "1000", OwnerUsername: "synthetic-user", Revision: i + 7, State: state, Created: at.Add(-time.Hour), Expires: &expires,
			Request: model.Request{Host: d.Host, Reason: fmt.Sprintf("Synthetic reservation %d", i+1), Agent: "synthetic-worker", Project: "synthetic-project", Resources: model.Resources{CPU: 2, Memory: 9007199254740993, Disk: 8 << 30}}}
		if state == "terminal" {
			l.Outcome = []string{"completed", "failed", "expired", "reporter_lost"}[i-8]
		}
		if i == 0 {
			l.Request.Reason = "DETAIL FIRST · " + strings.Repeat("bounded reason words ", 18) + "DETAIL MIDDLE · " + strings.Repeat("longsegment", 40) + "\nInert hint text [y] Release lease [n/Esc] Keep lease · DETAIL FINAL"
		}
		if len(l.Request.Reason) > 2048 {
			t.Fatal("synthetic reason exceeds bound")
		}
		d.Leases = append(d.Leases, l)
	}
	d.Log = []byte("LOG FIRST\n" + strings.Repeat("Synthetic line · glyph sample 界 🙂\n", 35) + "LOG MIDDLE\n" + strings.Repeat("longsegment", 65) + "\n" + strings.Repeat("Saved synthetic output only\n", 35) + "inert controls: \x1b]52;c;synthetic\a\t\u202e\nLOG FINAL\n")
	for i, state := range []string{"active", "queued", "needs_attention", "future_state", "interrupted", "terminal", "terminal", "terminal", "terminal", "terminal"} {
		r := model.Run{ID: fmt.Sprintf("%032x", i+129), Name: fmt.Sprintf("synthetic-run-%d", i+1), Host: d.Host, State: state, Reason: fmt.Sprintf("Synthetic run purpose %d", i+1), Agent: "synthetic-worker", CWD: "/workspace/synthetic", Command: []string{"/workspace/tools/synthetic-tool", "--inspect-only", "synthetic-input"}, Created: at.Add(-time.Duration(i+1) * time.Minute), LogBytes: int64(len(d.Log))}
		if i == 0 {
			r.LeaseID = d.Leases[0].ID
			r.Reason = "Synthetic long purpose " + strings.Repeat("boundedsegment", 10)
		}
		if i >= 5 {
			r.Outcome = []string{"reporter_lost", "completed", "failed", "expired", "cancelled"}[i-5]
		}
		d.Runs = append(d.Runs, r)
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > directFixtureDTOBytes || len(d.Log) > directFixtureLogBytes || len(d.Leases) > 12 || len(d.Runs) > 10 {
		t.Fatal("synthetic data exceeds reviewed inventory bounds")
	}
	for _, r := range d.Runs {
		if len(r.Name) > 256 || len(r.Reason) > 256 || len(r.Agent) > 256 || len(r.CWD) > 256 {
			t.Fatal("synthetic run label exceeds bound")
		}
	}
	return d
}

type directFixtureCall struct {
	Operation string
	ID        string `json:",omitempty"`
	Offset    int64  `json:",omitempty"`
	Bytes     int    `json:",omitempty"`
	Canceled  bool   `json:",omitempty"`
}

type directFixtureTransport struct {
	t         *testing.T
	data      directFixtureData
	calls     []directFixtureCall
	forbidden []string
	active    int
	closed    bool
	closes    int
}

func (s *directFixtureTransport) refuse(what string) error {
	s.forbidden = append(s.forbidden, what)
	return fmt.Errorf("unreviewed synthetic operation refused")
}
func (s *directFixtureTransport) Call(ctx context.Context, op string, params, result any) error {
	if s.closed || len(s.calls) >= 64 {
		return s.refuse("closed-or-call-bound")
	}
	if op != "inspect" && op != "runs" && op != "run_output" {
		return s.refuse("operation:" + op)
	}
	s.active++
	defer func() { s.active-- }()
	call := directFixtureCall{Operation: op}
	if err := ctx.Err(); err != nil {
		call.Canceled = true
		s.calls = append(s.calls, call)
		return err
	}
	switch op {
	case "inspect":
		q, ok := params.(map[string]string)
		out, typed := result.(*leaseRenderView)
		if !ok || !typed || len(q) != 1 {
			return s.refuse("inspect-shape")
		}
		for _, l := range s.data.Leases {
			if q["id"] == l.ID {
				*out = directFixtureClone(s.t, leaseRenderView{Lease: l, DisplayName: "Synthetic exact lease", HostDisplayName: "Synthetic host"})
				call.ID = l.ID
				s.calls = append(s.calls, call)
				return nil
			}
		}
		return s.refuse("inspect-id")
	case "runs":
		out, ok := result.(*[]model.Run)
		if params != nil || !ok {
			return s.refuse("runs-shape")
		}
		*out = directFixtureClone(s.t, s.data.Runs)
		s.calls = append(s.calls, call)
		return nil
	case "run_output":
		q, ok := params.(map[string]any)
		out, typed := result.(*model.RunLog)
		if !ok || !typed || len(q) != 2 {
			return s.refuse("output-shape")
		}
		id, idOK := q["id"].(string)
		offset, offsetOK := q["offset"].(int64)
		if !idOK || !offsetOK || id != s.data.Runs[0].ID || offset < 0 || offset > int64(len(s.data.Log)) {
			return s.refuse("output-id-or-cursor")
		}
		end := min(int64(len(s.data.Log)), offset+directFixturePageBytes)
		*out = directFixtureClone(s.t, model.RunLog{Run: s.data.Runs[0], Data: s.data.Log[offset:end], Offset: offset, NextOffset: end, Done: end == int64(len(s.data.Log))})
		call.ID, call.Offset, call.Bytes = id, offset, int(end-offset)
		s.calls = append(s.calls, call)
		return nil
	}
	return s.refuse("unreachable-operation")
}
func (s *directFixtureTransport) Events() <-chan model.Event {
	s.refuse("event-stream")
	s.t.Fatal("unreviewed stream method refused before any wait")
	return nil
}
func (s *directFixtureTransport) Telemetry() <-chan model.MonitorPage {
	s.refuse("telemetry-stream")
	s.t.Fatal("unreviewed stream method refused before any wait")
	return nil
}
func (s *directFixtureTransport) Done() <-chan struct{} {
	s.refuse("done-stream")
	s.t.Fatal("unreviewed stream method refused before any wait")
	return nil
}
func (s *directFixtureTransport) Err() error {
	err := s.refuse("stream-error")
	s.t.Fatal("unreviewed stream error method")
	return err
}
func (s *directFixtureTransport) Close() {
	if !s.closed {
		s.closed = true
		s.closes++
	}
}

type directFixtureReport struct {
	Scope        string
	SourceSHA256 map[string]string
	DataSHA256   string
	Checkpoints  []map[string]any
	Geometries   []map[string]any
	Complete     bool
	Limits       []string
}

type directFixture struct {
	t                                                *testing.T
	m                                                *dashboardModel
	s                                                *directFixtureTransport
	cancel                                           context.CancelFunc
	root, home, grid                                 string
	report                                           *directFixtureReport
	suppressed                                       map[string]int
	reads, mouseCommands, lateReceipts, heldReceipts int
	closed                                           bool
}

type directCommandPolicy int

const (
	directNoCommand directCommandPolicy = iota
	directSuppressUI
	directInspect
	directRunRead
)

// Deliberately no tea.Model adapter/Init method/program in this first fixture.
// Only an audited callsite can supply a read closure; library Cmds are dropped.
func (f *directFixture) command(cmd tea.Cmd, policy directCommandPolicy, origin string) {
	f.t.Helper()
	if cmd == nil {
		return
	}
	if policy == directSuppressUI {
		f.suppressed[origin]++
		return
	}
	if policy == directNoCommand || f.reads >= 32 {
		f.t.Fatal("unclassified command or read budget refused before execution")
	}
	f.reads++
	msg := cmd() // Only loadDetail/readRunPage or the audited original drain.
	switch policy {
	case directInspect:
		read, ok := msg.(detailMsg)
		if !ok {
			f.t.Fatal("inspect closure returned an unreviewed message")
		}
		f.update(read, directNoCommand, "inspect-result")
	case directRunRead:
		read, ok := msg.(runsReadMsg)
		if !ok || read.ui != f.m.runsUI || read.session != f.s {
			f.t.Fatal("run closure returned an unreviewed message or owner")
		}
		// The only permitted returned command here is the original bounded
		// same-reader continuation from acceptRunsRead; never Batch or timers.
		f.update(read, directRunRead, "run-read-result")
	default:
		f.t.Fatal("command policy is not an audited read")
	}
}
func (f *directFixture) invariant() {
	f.t.Helper()
	m := f.m
	if m.session != f.s || m.ctx == nil || m.home != f.home || (m.view != 2 && m.view != 7) || m.nodeUI != nil || m.meshUI != nil || m.poolUI != nil || m.nameUI != nil || m.composer != nil || m.checkins != nil || m.ioPicker != nil || m.recovery != nil || m.pending || m.unknownLease != "" || m.pendingMessage != nil || m.poolPending != nil || len(m.pendingRecords) != 0 || !m.hideInactive || !m.hideInactiveRuns || (m.inputMode != "" && m.inputMode != "filter") {
		f.t.Fatal("fixture left the reviewed owner/view/default/effect boundary")
	}
	if len(f.s.forbidden) != 0 || f.s.active != 0 {
		f.t.Fatal("synthetic operation was refused or remains active")
	}
}
func (f *directFixture) update(msg tea.Msg, policy directCommandPolicy, origin string) {
	f.t.Helper()
	f.invariant()
	// The closed typed set excludes Init/connected/reconnect/tick/stream and
	// mutation receipts, even if a future script accidentally supplies one.
	switch msg.(type) {
	case tea.WindowSizeMsg:
		size := msg.(tea.WindowSizeMsg)
		if (size.Width != 72 || size.Height != 28) && (size.Width != 100 || size.Height != 35) && (size.Width != 160 || size.Height != 50) {
			f.t.Fatal("unreviewed model grid")
		}
	case tea.KeyPressMsg:
		key := msg.(tea.KeyPressMsg).String()
		if key == " " {
			key = "space"
		}
		if f.keyPolicy(key) != policy {
			f.t.Fatal("key command policy mismatch")
		}
	case tea.PasteMsg:
		if f.m.inputMode != "filter" || len(msg.(tea.PasteMsg).Content) > 256 || policy != directSuppressUI {
			f.t.Fatal("paste is not a bounded filter draft")
		}
	case dashboardMouseMsg:
		mouse := msg.(dashboardMouseMsg)
		if mouse.wheel != 0 {
			f.t.Fatal("wheel injection is outside this first scripted fixture")
		}
		if mouse.frame != f.m.mouseFrame() {
			if policy != directNoCommand {
				f.t.Fatal("stale mouse must have no effect command")
			}
		} else {
			r := mouse.region
			if r.feed || r.record || r.mesh || r.pool || r.picker || r.recovery {
				f.t.Fatal("unreviewed mouse context")
			}
			expected := directNoCommand
			if r.key == "nav2" {
				expected = f.keyPolicy("3")
			} else if r.key == "nav7" {
				expected = f.keyPolicy("8")
			} else if r.key != "" {
				expected = f.keyPolicy(r.key)
			} else if r.row == f.m.selected {
				expected = f.keyPolicy("enter")
			}
			if policy != expected {
				f.t.Fatal("mouse command policy mismatch")
			}
		}
	case detailMsg:
		if policy != directNoCommand {
			f.t.Fatal("inspect result cannot schedule another command")
		}
	case runsReadMsg:
		if policy != directRunRead && policy != directNoCommand {
			f.t.Fatal("run result command policy")
		}

	default:
		f.t.Fatal("unreviewed input message refused before Update")
	}
	inner, cmd := f.m.Update(msg)
	if inner != f.m {
		f.t.Fatal("original model identity changed")
	}
	f.invariant()
	f.command(cmd, policy, origin)
}

// Exact state-aware script allowlist. It is an effect gate, not a substitute
// dispatcher or an assertion about all keys in the product.
func (f *directFixture) keyPolicy(key string) directCommandPolicy {
	f.t.Helper()
	m := f.m
	if m.inputMode == "filter" {
		if key == "enter" || key == "esc" {
			return directNoCommand
		}
		f.t.Fatal("only filter apply/escape is permitted")
	}
	if m.review != nil {
		if key == "n" || key == "esc" || key == "pgdown" || key == "pgup" || key == "home" || key == "end" {
			return directNoCommand
		}
		f.t.Fatal("review confirmation is never permitted")
	}
	if m.record != nil {
		switch key {
		case "enter", "space", "esc", "up", "down", "j", "k":
			return directNoCommand
		case "pgup", "pgdown", "home", "end":
			return directSuppressUI
		}
		f.t.Fatal("unreviewed record key")
	}
	if m.view == 7 && m.runsUI != nil && m.runsUI.selected != nil {
		switch key {
		case "f", "home", "end", "n", "r", "esc":
			return directRunRead
		case "l":
			return directInspect
		case "z", "o", "i", "up", "down", "j", "k", "pgup", "pgdown":
			return directNoCommand
		case "3":
			return directNoCommand
		case "8":
			return directRunRead
		}
		f.t.Fatal("unreviewed selected-reader key")
	}
	if m.detail != nil {
		switch key {
		case "r":
			return directInspect
		case "esc", "i", "x":
			return directNoCommand
		case "up", "down", "j", "k", "pgup", "pgdown", "home", "end":
			return directSuppressUI
		}
		f.t.Fatal("unreviewed lease detail key")
	}
	switch key {
	case "3", "i", "up", "down", "j", "k", "tab", "f", "esc":
		return directNoCommand
	case "8":
		return directRunRead
	case "/", "pgup", "pgdown", "home", "end":
		return directSuppressUI
	case "enter":
		if m.feedFocus {
			f.t.Fatal("feed drilldown is outside this first fixture")
		}
		if m.view == 2 {
			return directInspect
		}
		return directRunRead
	}
	f.t.Fatal("key is outside the reviewed direct fixture")
	return directNoCommand
}
func (f *directFixture) key(key string) {
	f.t.Helper()
	policy := f.keyPolicy(key)
	f.update(mouseKey(key), policy, "key:"+key)
}
func (f *directFixture) recordPath(path []string) {
	f.t.Helper()
	if f.m.record == nil {
		f.t.Fatal("record is not open")
	}
	encoded, err := json.Marshal(path)
	if err != nil {
		f.t.Fatal("record path")
	}
	target := -1
	for i, line := range f.m.record.lines {
		if line.path == string(encoded) {
			target = i
			break
		}
	}
	if target < 0 || target > 128 {
		f.t.Fatal("bounded record path is not projected")
	}
	for f.m.record.selected < target {
		f.key("down")
	}
	for f.m.record.selected > target {
		f.key("up")
	}
}
func (f *directFixture) filter(value string) {
	f.t.Helper()
	if len(value) > 256 {
		f.t.Fatal("filter draft bound")
	}
	f.key("/")
	f.update(tea.PasteMsg{Content: value}, directSuppressUI, "filter-paste")
	f.capture("filter-draft")
	f.key("enter")
}
func (f *directFixture) mouse(region mouseRegion) {
	f.t.Helper()
	if region.feed || region.record || region.mesh || region.pool || region.picker || region.recovery {
		f.t.Fatal("unreviewed mouse region")
	}
	policy := directNoCommand
	if region.key != "" {
		if region.key == "nav2" {
			policy = f.keyPolicy("3")
		} else if region.key == "nav7" {
			policy = f.keyPolicy("8")
		} else {
			policy = f.keyPolicy(region.key)
		}
	} else {
		if region.row < 0 {
			f.t.Fatal("mouse row is absent")
		}
		if region.row == f.m.selected {
			policy = f.keyPolicy("enter")
		}
	}
	view := f.m.View()
	cmd := view.OnMouse(tea.MouseClickMsg{X: region.x, Y: region.y, Button: tea.MouseLeft})
	if cmd == nil {
		f.t.Fatal("original mouse region produced no command")
	}
	f.mouseCommands++
	msg, ok := cmd().(dashboardMouseMsg) // Audited mouseHandler: typed event only.
	if !ok {
		f.t.Fatal("original mouse closure returned unreviewed message")
	}
	f.update(msg, policy, "mouse-event")
}
func (f *directFixture) findRegion(key string, row int) mouseRegion {
	f.t.Helper()
	for _, r := range f.m.mouseRegions() {
		if r.key == key && (key != "" || r.row == row) && !r.feed && !r.record && !r.mesh && !r.pool && !r.recovery && !r.picker && r.x >= 0 && r.y >= 0 && r.x+r.width <= f.m.width && r.y+r.height <= f.m.height {
			return r
		}
	}
	f.t.Fatal("required bounded native region not present; preserve this failure")
	return mouseRegion{}
}

func directWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil || os.WriteFile(path, append(b, '\n'), 0600) != nil {
		t.Error("private fixture JSON write failed")
	}
}
func directHomeEntries(t *testing.T, home string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	err := filepath.WalkDir(home, func(path string, item os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == home {
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err != nil || item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected home entry")
		}
		if item.IsDir() {
			entries[filepath.ToSlash(rel)] = "directory"
			return nil
		}
		info, err := item.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			return fmt.Errorf("unexpected nonempty home file")
		}
		entries[filepath.ToSlash(rel)] = "empty regular file"
		return nil
	})
	if err != nil {
		t.Error("synthetic home inspection failed")
	}
	return entries
}
func (f *directFixture) capture(name string) {
	f.t.Helper()
	f.invariant()
	if len(f.report.Checkpoints) >= directFixtureCheckpointLimit {
		f.t.Fatal("checkpoint budget exceeded")
	}
	f.m.syncViewport()
	view := f.m.View()
	frame := []byte(view.Content)
	if len(frame) > 64<<10 || strings.Contains(view.Content, "\x1b]52;") {
		f.t.Fatal("model frame bound or inert-output escape failed")
	}
	file := fmt.Sprintf("%s/%02d-%s.view.ansi", f.grid, len(f.report.Checkpoints)+1, name)
	if os.WriteFile(filepath.Join(f.root, filepath.FromSlash(file)), frame, 0600) != nil {
		f.t.Fatal("model frame write failed")
	}
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	widths := make([]int, len(lines))
	for i, line := range lines {
		widths[i] = ansi.StringWidth(line)
	}
	regions := []map[string]any{}
	for _, r := range f.m.mouseRegions() {
		regions = append(regions, map[string]any{"x": r.x, "y": r.y, "width": r.width, "height": r.height, "key": r.key, "row": r.row, "feed": r.feed, "record": r.record, "mesh": r.mesh, "pool": r.pool, "recovery": r.recovery, "picker": r.picker})
	}
	frameHash := sha256.Sum256(frame)
	m := f.m
	item := map[string]any{"grid": f.grid, "name": name, "file": file, "sha256": hex.EncodeToString(frameHash[:]), "cols": m.width, "rows": m.height, "view": m.view, "altScreenFlag": view.AltScreen, "windowTitle": view.WindowTitle, "modelFrameLines": len(lines), "ansiCellWidths": widths, "regions": regions, "selected": m.selected, "selectionID": m.selectionID(), "feedFocus": m.feedFocus, "feedOpen": m.feedOpen, "modal": m.modal(), "recordOpen": m.record != nil, "detailOpen": m.detail != nil, "reviewDraft": m.review != nil, "inputMode": m.inputMode, "hideInactiveLeases": m.hideInactive, "hideInactiveRuns": m.hideInactiveRuns, "viewport": map[string]any{"width": m.viewport.Width(), "height": m.viewport.Height(), "offset": m.viewport.YOffset(), "totalLines": m.viewport.TotalLineCount()}, "readClosuresExecuted": f.reads, "mouseClosuresExecuted": f.mouseCommands, "suppressedUnexecutedLibraryCmd": directFixtureClone(f.t, f.suppressed), "calls": directFixtureClone(f.t, f.s.calls), "qualification": "Original View.Content only; no native program/PTY/terminal cells/pixels or focus/timer-loop acceptance"}
	if m.view == 2 && m.detail == nil && m.record == nil && !m.modal() {
		ids := []string{}
		for _, l := range m.filteredLeases() {
			ids = append(ids, l.ID)
		}
		item["visibleLeaseIDs"] = ids
	}
	if m.runRosterVisible() {
		ids := []string{}
		for _, r := range m.filteredRuns() {
			ids = append(ids, r.ID)
		}
		item["visibleRunIDs"] = ids
	}
	if ui := m.runsUI; ui != nil {
		selected := ""
		if ui.selected != nil {
			selected = ui.selected.ID
		}
		item["reader"] = map[string]any{"selectedID": selected, "offset": ui.offset, "bufferBytes": len(ui.data), "following": ui.following, "folded": ui.folded, "full": ui.full, "done": ui.done, "busy": ui.busy, "generation": ui.generation, "displayCut": ui.displayCut, "diagnostic": safe(ui.err)}
	}
	if m.record != nil && len(m.record.lines) > 0 {
		line := m.record.lines[m.record.selected]
		item["recordSelection"] = map[string]any{"path": line.path, "label": line.label, "value": line.value, "qualification": "Typed underlying node, not an untruncated native glyph guarantee"}
	}
	f.report.Checkpoints = append(f.report.Checkpoints, item)
}
func (f *directFixture) close() {
	if f.closed {
		return
	}
	f.closed = true
	f.m.stopPeerReads()
	f.m.retireRuns()
	f.cancel()
	f.s.Close()
	entries := directHomeEntries(f.t, f.home)
	if len(entries) != 2 || entries["dashboard-pending"] != "directory" || entries["dashboard-pending/.lock"] != "empty regular file" {
		f.t.Error("constructor home effects differ from reviewed pending-only entries")
	}
	if f.s.active != 0 || f.heldReceipts != 0 || len(f.s.forbidden) != 0 || f.s.closes != 1 || f.m.ctx.Err() == nil || (f.m.runsUI != nil && (f.m.runsUI.busy || f.m.runsUI.cancel != nil)) || (f.m.peers != nil && f.m.peers.cancel != nil) {
		f.t.Error("fixture cleanup/forbidden-operation gate failed")
	}
	f.report.Geometries = append(f.report.Geometries, map[string]any{"grid": f.grid, "calls": f.s.calls, "readClosuresExecuted": f.reads, "mouseClosuresExecuted": f.mouseCommands, "lateReceipts": f.lateReceipts, "heldReceipts": f.heldReceipts, "runSelectionCleared": f.m.runsUI == nil || f.m.runsUI.selected == nil, "suppressedUnexecutedLibraryCmd": f.suppressed, "homeEffects": entries, "contextCanceled": f.m.ctx.Err() != nil, "fakeSessionClosed": f.s.closed, "activeFakeCalls": f.s.active, "forbidden": f.s.forbidden, "qualification": "Synchronous direct fixture and cancel/Close return observations only; no actual Run, event loop, streams, goroutine/process/PTY cleanup"})
}

func prepareDirectFixture(t *testing.T, root string, size [2]int, data directFixtureData, report *directFixtureReport) *directFixture {
	t.Helper()
	grid := fmt.Sprintf("%dx%d", size[0], size[1])
	dir := filepath.Join(root, grid)
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("fresh grid directory creation failed")
	}
	home := filepath.Join(dir, "synthetic-home")
	if os.Mkdir(home, 0700) != nil {
		t.Fatal("fresh synthetic home creation failed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m := newDashboard(ctx, home) // Explicit, disclosed constructor file effects.
	s := &directFixtureTransport{t: t, data: directFixtureClone(t, data)}
	f := &directFixture{t: t, m: m, s: s, cancel: cancel, root: root, home: home, grid: grid, report: report, suppressed: map[string]int{}}
	t.Cleanup(f.close)
	if m.pendingStoreError != "" || len(m.pendingRecords) != 0 {
		t.Fatal("fresh constructor bookkeeping failed")
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
	m.notice = "Synthetic direct model fixture; no native session or live state."
	m.lastRefresh = m.timeNow
	f.update(tea.WindowSizeMsg{Width: size[0], Height: size[1]}, directNoCommand, "resize")
	return f
}

func runDirectGeometry(t *testing.T, root string, size [2]int, data directFixtureData, report *directFixtureReport) {
	f := prepareDirectFixture(t, root, size, data, report)
	defer f.close()
	m := f.m
	f.capture("leases-default")
	// Native row selection and exact inspect use original regions/OnMouse.
	f.mouse(f.findRegion("", 0))
	if m.detail == nil || m.detail.ID != data.Leases[0].ID {
		t.Fatal("native lease inspect did not retain exact ID")
	}
	f.capture("lease-detail-first")
	f.key("pgdown")
	f.capture("lease-detail-middle")
	f.key("end")
	f.capture("lease-detail-tail")
	f.key("i")
	f.capture("lease-record")
	f.recordPath([]string{"request"})
	f.key("enter")
	f.recordPath([]string{"request", "resources"})
	f.key("enter")
	f.recordPath([]string{"request", "resources", "memory_bytes"})
	if fmt.Sprint(m.record.lines[m.record.selected].value) != "9007199254740993" {
		t.Fatal("typed record number changed")
	}
	f.capture("lease-record-exact-number")
	f.key("esc")
	f.key("x")
	f.capture("release-review-draft")
	f.mouse(f.findRegion("n", -1))
	if m.review != nil {
		t.Fatal("decline left a review")
	}
	f.key("esc")
	f.filter("no-synthetic-match")
	f.capture("leases-filter-empty")
	f.key("esc")
	// Original callback captured before resize must remain stale when delivered.
	r := f.findRegion("nav7", -1)
	stale := m.View().OnMouse(tea.MouseClickMsg{X: r.x, Y: r.y, Button: tea.MouseLeft})
	if stale == nil {
		t.Fatal("stale-mouse source command absent")
	}
	other := [2]int{72, 28}
	if size[0] == 72 {
		other = [2]int{100, 35}
	}
	f.update(tea.WindowSizeMsg{Width: other[0], Height: other[1]}, directNoCommand, "temporary-reviewed-grid-resize")
	f.mouseCommands++
	mouseMsg, ok := stale().(dashboardMouseMsg)
	if !ok {
		t.Fatal("stale callback returned unreviewed message")
	}
	f.update(mouseMsg, directNoCommand, "stale-resize-mouse")
	if m.view != 2 {
		t.Fatal("stale mouse navigated")
	}
	f.update(tea.WindowSizeMsg{Width: size[0], Height: size[1]}, directNoCommand, "restore-resize")
	f.mouse(f.findRegion("nav7", -1))
	f.capture("runs-default")
	f.mouse(f.findRegion("", 0))
	if m.runsUI == nil || m.runsUI.selected == nil || m.runsUI.selected.ID != data.Runs[0].ID {
		t.Fatal("native run inspect identity differs")
	}
	ui := m.runsUI
	if !ui.done || ui.offset != int64(len(data.Log)) {
		t.Fatal("bounded original drain did not finish")
	}
	f.key("f") // Pause; no continuation starts while following is false.
	f.key("home")
	f.capture("run-start-page")
	f.key("n")
	f.capture("run-next-page")
	f.key("f") // Original bounded drain continues to the saved end.
	f.key("f")
	f.key("home")
	f.key("n")
	f.key("n")
	f.key("n")
	f.key("pgup")
	f.capture("run-paused-reader")
	beforeData, beforeOffset := string(ui.data), ui.offset
	f.key("o")
	f.capture("run-folded")
	beforeView := m.viewport.YOffset()
	f.key("z")
	f.capture("run-fullscreen")
	f.key("esc")
	if ui.full || !ui.folded || ui.following || string(ui.data) != beforeData || ui.offset != beforeOffset || m.viewport.YOffset() != beforeView {
		t.Fatal("fullscreen exit changed the prior reader")
	}
	f.key("o")
	// Hold a genuine original read RECEIPT (no background worker), then leave
	// the view before its delivery. This is not an async pending-read proof.
	pending := m.readRuns(true)
	if pending == nil {
		t.Fatal("held receipt source command absent")
	}
	if f.reads >= 32 {
		t.Fatal("held read budget")
	}
	f.reads++
	f.heldReceipts++
	receipt, ok := pending().(runsReadMsg)
	if !ok || receipt.ui != ui || receipt.session != f.s {
		t.Fatal("held read returned unreviewed message")
	}
	f.key("3")
	f.lateReceipts++
	f.heldReceipts--
	f.update(receipt, directNoCommand, "late-run-receipt")
	if m.view != 2 || (m.runsUI != nil && m.runsUI.selected != nil) {
		t.Fatal("late receipt resurrected reader")
	}
	if size[0] == 72 {
		m.connected = false
		m.notice = "Synthetic disconnected cached observations; no reconnect command scheduled."
		f.capture("leases-disconnected-seed")
	}
}

func TestUXTerminalDirectFixture(t *testing.T) {
	root := os.Getenv(directFixtureOutput)
	if root == "" {
		t.Skip("opt-in fixture: source review and explicit allocation required")
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("output must be an absolute owned directory")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal("fixture source cwd unavailable")
	}
	repo := filepath.Clean(filepath.Join(cwd, "..", ".."))
	rel, err := filepath.Rel(repo, root)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		t.Fatal("private output must be outside the source checkout")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(root))
	if err != nil || parent != filepath.Dir(root) {
		t.Fatal("output parent must be an existing resolved directory")
	}
	if os.Mkdir(root, 0700) != nil {
		t.Fatal("output must be new; previous evidence is preserved")
	}
	report := &directFixtureReport{Scope: "Direct original model View.Content only; no native Run/Init/auth/event loop/PTY/browser", SourceSHA256: map[string]string{}, Limits: []string{"Constructor pending-directory/lock creation and sync under fresh per-grid0700 home are local fixture effects.", "Only audited original read/OnMouse closures execute synchronously. Library Cmds are recorded and not invoked; no generic Cmd/Batch/timer/stream executor.", "Source preparation is not compile or runtime validation. Model frames/region metadata are not native terminal paint/cell/focus/loop acceptance.", "No mutation confirmation, preference toggle, endpoint/session fallback, real agent/message/job/resource effects or production startup/report cleanup."}}
	t.Cleanup(func() {
		report.Complete = !t.Failed()
		directWriteJSON(t, filepath.Join(root, "direct-fixture.json"), report)
	})
	var classification struct {
		SourceEntries map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"sourceEntries"`
	}
	raw, err := os.ReadFile(filepath.Join(repo, "docs/validation/ux-terminal-source-classification-2026-10-29.json"))
	classificationHash := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(classificationHash[:]) != "574c62ed1e2d66828082880beeaa4d706b7690cf4f38b1ff3a229aa1165cae5d" || json.Unmarshal(raw, &classification) != nil || len(classification.SourceEntries) != 50 {
		t.Fatal("frozen source classification unavailable")
	}
	for file, entry := range classification.SourceEntries {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		h := sha256.Sum256(b)
		if e != nil || hex.EncodeToString(h[:]) != entry.SHA256 {
			t.Fatal("classified source input changed; stop for review")
		}
		report.SourceSHA256[file] = entry.SHA256
	}
	for _, file := range []string{"internal/tui/ux_terminal_direct_fixture_test.go", "docs/validation/ux-terminal-direct-fixture-plan-2026-10-30.json"} {
		b, e := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		if e != nil {
			t.Fatal("fixture source unavailable")
		}
		h := sha256.Sum256(b)
		report.SourceSHA256[file] = hex.EncodeToString(h[:])
	}
	data := newDirectFixtureData(t)
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal("fixture data encoding failed")
	}
	h := sha256.Sum256(encoded)
	report.DataSHA256 = hex.EncodeToString(h[:])
	for _, size := range [][2]int{{72, 28}, {100, 35}, {160, 50}} {
		runDirectGeometry(t, root, size, data, report)
	}
	if len(report.Checkpoints) > directFixtureCheckpointLimit || len(report.Geometries) != 3 {
		t.Fatal("fixture roster bound or original cleanup incomplete")
	}
}
