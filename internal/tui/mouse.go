package tui

import (
	"crypto/sha256"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type mouseControl struct{ key, label string }
type mouseRegion struct {
	mesh                bool
	recovery            bool
	pool                bool
	picker              bool
	x, y, width, height int
	key                 string
	row                 int
	feed, record        bool
}
type dashboardMouseMsg struct {
	frame  string
	region mouseRegion
	wheel  int
}

func (m *dashboardModel) controls() []mouseControl {
	if m.nameUI != nil {
		if m.nameUI.busy {
			return nil
		}
		if m.nameUI.loading {
			return []mouseControl{{"esc", "Close"}}
		}
		if m.nameUI.snapshot == nil {
			return []mouseControl{{"ctrl+r", "Read"}, {"esc", "Close"}}
		}
		label := "Exact ref"
		if m.nameUI.manual {
			label = "Loaded ref"
		}
		return []mouseControl{{"ctrl+t", label}, {"ctrl+s", "Set name"}, {"ctrl+d", "Clear"}, {"ctrl+r", "Read"}, {"esc", "Close"}}
	}
	controls := m.baseControls()
	if m.nodeUI == nil && m.recovery == nil && m.connected && !m.modal() && m.record == nil {
		controls = append([]mouseControl{{"ctrl+n", "Name"}}, controls...)
	}
	return controls
}
func (m *dashboardModel) baseControls() []mouseControl {
	switch {
	case m.nodeUI != nil && m.record == nil:
		return []mouseControl{{"7", "LOCAL Mesh"}, {"r", "Read node"}, {"i", "Record"}, {"pgdown", "↓"}}
	case m.recovery != nil && m.record == nil:
		return m.recoveryControls()
	case m.ioPicker != nil:
		return []mouseControl{{"a", "All"}, {"n", "None"}, {"esc", "Done"}, {"enter", "Toggle"}, {"?", "Help"}}
	case m.inputMode != "":
		return []mouseControl{{"enter", "Continue"}, {"esc", "Back"}}
	case m.checkins != nil && m.record == nil:
		return m.checkinControls()
	case m.review != nil:
		if m.pending {
			return nil
		}
		return []mouseControl{{"y", "Confirm"}, {"n", "Keep unchanged"}, {"pgup", "↑"}, {"pgdown", "↓"}}
	case m.composer != nil:
		if m.messageBusy != "" {
			return nil
		}
		if m.composer.unknown {
			return []mouseControl{{"i", "Inspect key"}, {"y", "Retry same key"}}
		}
		if !m.composer.review {
			return []mouseControl{{"e", "Edit draft"}, {"n", "Discard"}}
		}
		return []mouseControl{{"y", "Send reviewed"}, {"n", "Keep unsent"}, {"pgup", "↑"}, {"pgdown", "↓"}}
	case m.record != nil:
		return []mouseControl{{"enter", "Expand"}, {"esc", "Back"}, {"pgup", "↑"}, {"pgdown", "↓"}}
	case m.view == 7 && m.runsUI != nil && m.runsUI.selected != nil:
		return []mouseControl{{"f", "Follow/pause"}, {"o", "Fold"}, {"z", "Fullscreen"}, {"home", "Start"}, {"n", "Next page"}, {"esc", "Runs"}}
	case m.detail != nil:
		return []mouseControl{{"e", "Extend"}, {"x", "Release"}, {"c", "Cancel"}, {"h", "Check-ins"}, {"r", "Refresh"}, {"i", "Record"}, {"esc", "Back"}}
	case m.poolUI != nil:
		return m.poolControls()
	case m.view == 6:
		return m.meshControls()
	case m.resourceID != "":
		return []mouseControl{{"enter", "Open lease"}, {"i", "Record"}, {"esc", "Budgets"}}
	case m.view == 0:
		return []mouseControl{{"d", "Disk"}, {"n", "Network"}, {"i", "Record"}, {"pgdown", "↓"}, {"?", "Help"}}
	case m.view == 1 && !m.feedFocus:
		return []mouseControl{{"p", "Pools"}, {"enter", "Inspect"}, {"/", "Filter"}, {"f", "Feed"}}
	case m.view == 2 && !m.feedFocus:
		label := "Hide inactive"
		if m.hideInactive {
			label = "Show inactive"
		}
		return []mouseControl{{"enter", "Inspect"}, {"h", label}, {"/", "Filter"}, {"f", "Feed"}}
	case m.view == 3:
		return []mouseControl{{"n", "Message"}, {"b", "Broadcast"}, {"y", "Reply"}, {"enter", "Inspect"}, {"f", "Feed"}}
	default:
		return []mouseControl{{"enter", "Inspect"}, {"/", "Filter"}, {"f", "Feed"}, {"?", "Help"}}
	}
}
func (m *dashboardModel) controlBar() (string, []mouseRegion) {
	var labels []string
	var regions []mouseRegion
	x := 1
	for _, c := range m.controls() {
		tone := ""
		if c.key == "y" && m.review != nil && m.review.request.Action != "renew" {
			tone = "danger"
		}
		if c.key == "n" || c.key == "esc" {
			tone = "quiet"
		}
		label := keycap(c.key, c.label, tone)
		width := ansi.StringWidth(label)
		if x+width > m.width {
			break
		}
		labels = append(labels, label)
		regions = append(regions, mouseRegion{x: x, y: m.height - 3, width: width, height: 1, key: c.key, row: -1})
		x += width + 1
	}
	return " " + strings.Join(labels, " "), regions
}

// Bind an input to the exact rendered state: resize, dialog replacement, pending
// transitions, selection or content changes invalidate delayed mouse commands.
func (m *dashboardModel) mouseFrame() string {
	return fmt.Sprintf("%dx%d/%d/%d/%d/%d/%d/%t/%t/%p/%p/%p/%d/%s/%s/%p/%x", m.width, m.height, m.view, m.selected, m.viewport.YOffset(), m.feedOffset, m.detailGeneration, m.pending, m.feedFocus, m.review, m.composer, m.record, m.messageOperation, m.messageBusy, m.inputMode, m.ioPicker, sha256.Sum256([]byte(m.displayContent()+fmt.Sprint(m.feedItems())+m.poolMouseFrame()))) + fmt.Sprintf("/names:%p/run-full:%t", m.nameUI, m.runFullscreen())
}

// Read-only wheel deltas can be queued from one rendered frame. Scrolling or
// telemetry refresh must not discard the next delta; navigation/modal/geometry
// changes still prevent an old gesture from scrolling a different surface.
func (m *dashboardModel) mouseScrollFrame() string {
	return fmt.Sprintf("%dx%d/%d/%d/%p/%p/%p/%s/%s/%s/%t/%t/%p", m.width, m.height, m.view, m.detailGeneration, m.review, m.composer, m.record, m.inputMode, m.resourceID, m.filter, m.feedFocus, m.hideInactive, m.ioPicker) + m.poolScrollFrame() + fmt.Sprintf("/%p/%p/%p", m.recovery, m.meshUI, m.nodeUI) + fmt.Sprintf("/names:%p/run-full:%t", m.nameUI, m.runFullscreen())
}

func mouseKey(key string) tea.KeyPressMsg {
	if strings.HasPrefix(key, "ctrl+") && len([]rune(strings.TrimPrefix(key, "ctrl+"))) == 1 {
		return tea.KeyPressMsg{Code: []rune(strings.TrimPrefix(key, "ctrl+"))[0], Mod: tea.ModCtrl}
	}
	code := rune(0)
	switch key {
	case "down":
		code = tea.KeyDown
	case "up":
		code = tea.KeyUp
	case "space":
		code = tea.KeySpace
	case "enter":
		code = tea.KeyEnter
	case "esc":
		code = tea.KeyEscape
	case "home":
		code = tea.KeyHome
	case "end":
		code = tea.KeyEnd
	case "pgup":
		code = tea.KeyPgUp
	case "pgdown":
		code = tea.KeyPgDown
	default:
		if len([]rune(key)) == 1 {
			code = []rune(key)[0]
		}
	}
	return tea.KeyPressMsg{Code: code}
}
func (m *dashboardModel) mouseRegions() []mouseRegion {
	if m.runFullscreen() {
		var regions []mouseRegion
		for _, control := range []struct{ key, label string }{{"z", "[z/Esc] Back"}, {"f", "[f] Follow"}, {"o", "[o] Fold"}, {"q", "[q] Exit"}} {
			if x := strings.Index("[z/Esc] Back [f] Follow [o] Fold [q] Exit", control.label); x < m.width {
				regions = append(regions, mouseRegion{x: x, y: m.height - 1, width: min(len(control.label), m.width-x), height: 1, key: control.key, row: -1})
			}
		}
		return regions
	}
	_, regions := m.controlBar()
	regions = append(regions, mouseRegion{x: 0, y: m.height - 1, width: 10, height: 1, key: "q", row: -1})
	if x := strings.Index(ansi.Strip(m.exitFooter()), "[u] Recovery"); x >= 0 && m.canOpenRecovery() {
		regions = append(regions, mouseRegion{x: x, y: m.height - 1, width: len("[u] Recovery"), height: 1, key: "u", row: -1})
	}
	if !m.modal() && !m.pending {
		x := 0
		for i := range viewNames {
			label := m.navigationLabel(i)
			w := ansi.StringWidth(label) + 2
			if m.width < 65 && i != m.view {
				w -= 2
			}
			regions = append(regions, mouseRegion{x: x, y: 1, width: w, height: 1, key: fmt.Sprintf("nav%d", i), row: -1})
			x += w + 1
		}
	}
	if m.modal() {
		regions = append(regions, m.meshMouseRegions()...)
		regions = append(regions, m.poolMouseRegions()...)
		regions = append(regions, m.modalButtonRegions()...)
		regions = append(regions, m.pickerMouseRegions()...)
		return regions
	}
	if m.nodeUI != nil && m.record == nil {
		return regions
	}
	if m.view == 6 && m.record == nil {
		return append(regions, m.meshMouseRegions()...)
	}
	if m.width >= 110 {
		for i := m.feedOffset; i < len(m.feedItems()); i++ {
			y := 7 + (i-m.feedOffset)*3
			if !m.connected {
				y += 2
			}
			if y >= 4+m.bodyHeight()-1 {
				break
			}
			regions = append(regions, mouseRegion{x: m.mainWidth() + 4, y: y, width: 29, height: min(2, 4+m.bodyHeight()-1-y), row: i, feed: true})
		}
	}
	if m.recovery != nil && m.record == nil {
		return append(regions, m.recoveryMouseRegions()...)
	}
	if m.view == 2 && m.detail == nil && m.record == nil && !m.feedFocus && m.resourceID == "" && m.viewport.YOffset() == 0 {
		regions = append(regions, mouseRegion{x: 2, y: 5, width: min(31, m.mainWidth()-4), height: 1, key: "h", row: -1})
	}
	if m.poolUI != nil && m.detail == nil && m.record == nil {
		return append(regions, m.poolMouseRegions()...)
	}
	regions = append(regions, m.monitorSelectorRegions()...)
	if m.detail != nil {
		return regions
	}
	// Map rendered source lines in linear time. All record/list renderers use
	// bounded fixed row groups; cards advertise their border at each start.
	starts := map[int]int{}
	switch {
	case m.record != nil:
		for i := range m.record.lines {
			starts[i] = i
		}
	case m.resourceID != "":
		starts = m.resourceDetailRender().rows
	case m.feedFocus:
		for i := range m.feedItems() {
			starts[3*i] = i
		}
	case m.view == 1:
		next := 0
		for i, line := range strings.Split(m.displayContent(), "\n") {
			plain := ansi.Strip(line)
			if strings.HasPrefix(plain, "╭─ ") || strings.HasPrefix(plain, "╔═ ") {
				starts[i] = next
				next++
			}
		}
	case m.view == 2:
		for i := range m.filteredLeases() {
			starts[4+2*i] = i
		}
	case m.view == 3:
		row := 4
		for i, item := range m.data.messages {
			starts[row] = i
			row += 4 + len(item.Deliveries)
		}
	case m.view == 7 && m.runsUI != nil && m.runsUI.selected == nil:
		starts = m.runListRender().rows
	case m.view == 4:
		for i := range m.events {
			starts[3*i] = i
		}
	}
	row := 0
	for source, line := range strings.Split(m.displayContent(), "\n") {
		if index, ok := starts[source]; ok {
			y := 5 + row - m.viewport.YOffset()
			height := 1
			if (m.view == 2 || m.view == 7 && m.runsUI != nil && m.runsUI.selected == nil) && m.record == nil && !m.feedFocus && m.resourceID == "" {
				height = 2
			}
			bottom := min(4+m.bodyHeight()-1, y+height)
			y = max(5, y)
			height = bottom - y
			if height > 0 {
				regions = append(regions, mouseRegion{x: 2, y: y, width: max(1, m.mainWidth()-4), height: height, row: index, record: m.record != nil})
			}

		}
		row += max(1, (ansi.StringWidth(line)+max(1, m.viewport.Width())-1)/max(1, m.viewport.Width()))
	}

	return regions
}
func (m *dashboardModel) mouseHandler() func(tea.MouseMsg) tea.Cmd {
	frame := m.mouseFrame()
	scrollFrame := m.mouseScrollFrame()
	regions := m.mouseRegions()
	width, height, mainWidth := m.width, m.height, m.mainWidth()
	return func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		if mouse.X < 0 || mouse.Y < 0 || mouse.X >= width || mouse.Y >= height {
			return nil
		}
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			direction := 0
			if mouse.Button == tea.MouseWheelUp {
				direction = -3
			}
			if mouse.Button == tea.MouseWheelDown {
				direction = 3
			}
			top, bottom := 4, height-4
			if m.runFullscreen() {
				top = 1
				bottom = height - 1
			}
			if direction == 0 || mouse.Y < top || mouse.Y >= bottom {
				return nil
			}
			event := dashboardMouseMsg{frame: scrollFrame, wheel: direction, region: mouseRegion{feed: !m.runFullscreen() && width >= 110 && mouse.X >= mainWidth+2}}
			return func() tea.Msg { return event }
		}
		if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft {
			return nil
		}
		for _, region := range regions {
			if mouse.X >= region.x && mouse.X < region.x+region.width && mouse.Y >= region.y && mouse.Y < region.y+region.height {
				event := dashboardMouseMsg{frame: frame, region: region}
				return func() tea.Msg { return event }
			}
		}
		return nil
	}
}
func (m *dashboardModel) acceptMouse(msg dashboardMouseMsg, frame string) tea.Cmd {
	if msg.wheel != 0 {
		if msg.frame != m.mouseScrollFrame() {
			return nil
		}
		if msg.region.feed && m.modal() {
			return nil
		}
		if msg.region.feed && !m.modal() {
			m.feedOffset = max(0, min(max(0, len(m.feedItems())-1), m.feedOffset+msg.wheel))
		} else {
			m.viewport.SetYOffset(m.viewport.YOffset() + msg.wheel)
			if m.view == 7 && m.runsUI != nil && m.runsUI.selected != nil {
				m.runsUI.following = false
			}
		}
		return nil
	}
	if msg.frame != frame {
		return nil
	}
	r := msg.region
	if strings.HasPrefix(r.key, "nav") {
		m.recovery = nil
		if m.runsUI != nil {
			m.retireRuns()
			m.runsUI.selected = nil
		}
		m.detail = nil
		m.record = nil
		m.resourceID = ""
		return m.key(mouseKey(string(rune(r.key[len(r.key)-1] - '0' + '1'))))
	}
	if r.key != "" {
		return m.key(mouseKey(r.key))
	}
	if r.mesh {
		return m.acceptMeshMouse(r)
	}
	if r.picker && m.ioPicker != nil {
		options := m.ioOptions()
		if r.row >= 0 && r.row < len(options) {
			m.ioPicker.selectedID = options[r.row].id
			m.ioSelection(m.ioPicker.disk).toggle(options[r.row].id, m.ioItems(m.ioPicker.disk))
		}
		return nil
	}
	if r.recovery {
		if m.recovery != nil && r.row >= 0 && r.row < len(m.recovery.records) {
			if m.recovery.selected == r.row {
				return m.recoveryKey("enter")
			}
			m.recovery.selected = r.row
		}
		return nil
	}
	if r.pool {
		return m.acceptPoolMouse(r)
	}
	if m.modal() {
		return nil
	}
	if r.feed {
		items := m.feedItems()
		if r.row < 0 || r.row >= len(items) {
			return nil
		}
		if items[r.row].lease != "" {
			return m.loadDetail(items[r.row].lease)
		}
		m.record, _ = newRecord(items[r.row].record, "Activity")
		m.viewport.GotoTop()
		return nil
	}
	if r.record {
		if m.record == nil || r.row >= len(m.record.lines) {
			return nil
		}
		if m.record.selected == r.row {
			m.record.toggle()
		} else {
			m.record.selected = r.row
		}
		return nil
	}
	if r.row < 0 || r.row >= m.itemCount() {
		return nil
	}
	if m.selected == r.row {
		return m.openSelected()
	}
	m.selected = r.row
	return nil
}

func (m *dashboardModel) modalButtonRegions() []mouseRegion {
	// Only the trusted final lease-review button row is eligible. Matching
	// arbitrary user-supplied purpose/message text must never create controls.
	if m.review == nil || m.pending || !m.connected || m.inputMode != "" {
		return nil
	}
	lines := strings.Split(m.viewportContent(), "\n")
	if len(lines) == 0 {
		return nil
	}
	line := ansi.Strip(lines[len(lines)-1])
	action := map[string]string{"renew": "Extend lease", "release": "Release lease", "cancel": "Cancel lease"}[m.review.request.Action]
	if action == "" {
		return nil
	}
	yes, no := "[y] "+action, "[n/Esc] Keep lease"
	yesIndex, noIndex := strings.Index(line, yes), strings.Index(line, no)
	if yesIndex < 0 || noIndex < 0 {
		return nil
	}
	width, height := m.modalGeometry()
	originX := max(0, (m.mainWidth()-width)/2) + 2
	originY := 4 + max(0, (m.bodyHeight()-height)/2) + 1
	row := len(lines) - 1 - m.viewport.YOffset()
	if row < 0 || row >= m.viewport.Height() {
		return nil
	}
	return []mouseRegion{{x: originX + ansi.StringWidth(line[:yesIndex]) - 1, y: originY + row, width: ansi.StringWidth(yes) + 2, height: 1, key: "y", row: -1}, {x: originX + ansi.StringWidth(line[:noIndex]) - 1, y: originY + row, width: ansi.StringWidth(no) + 2, height: 1, key: "n", row: -1}}
}
