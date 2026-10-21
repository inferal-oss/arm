// Package tui implements the interactive local dashboard over the same
// authenticated request/event/telemetry protocol as ARM's web console.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

var viewNames = []string{"Monitor", "Resources", "Leases", "Messages", "Activity", "Host"}

type dashboardModel struct {
	poolUI                            *poolDashboard
	poolPending                       *poolOperation
	poolSequence                      uint64
	hideInactive                      bool
	ctx                               context.Context
	home                              string
	session                           transport
	data                              snapshot
	connected                         bool
	connecting                        bool
	retry                             int
	refreshing                        bool
	dirty                             bool
	lastRefresh, timeNow, timeMonitor time.Time
	page                              model.MonitorPage
	history                           []model.MonitorPoint
	diskSelection, networkSelection   ioSelection
	ioPicker                          *ioPicker
	events                            []model.Event
	width, height, view, selected     int
	feedFocus, feedOpen               bool
	feedOffset                        int
	viewport                          viewport.Model
	notice                            string
	detail                            *model.Lease
	resourceID                        string
	detailGeneration                  uint64
	record                            *recordTree
	filter                            string
	input                             textinput.Model
	inputMode                         string
	review                            *leaseReview
	pending                           bool
	unknownLease                      string
	composer                          *messageComposer
	pendingMessage                    *manager.MessageRequest
	messageOperation                  uint64
	messageBusy                       string
}

// Run owns only the terminal and its client connection. Exiting never releases
// leases or stops the backend. The caller must validate that input/output are TTYs.
func Run(ctx context.Context, home string, input io.Reader, output io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newDashboard(ctx, home)
	opts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler()}
	if os.Getenv("NO_COLOR") != "" {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	final, err := tea.NewProgram(m, opts...).Run()
	if result, ok := final.(*dashboardModel); ok {
		if result.session != nil {
			result.session.Close()
		}
		if result.pending || result.unknownLease != "" {
			fmt.Fprintln(output, "Lease action outcome may be unknown; inspect lease", safe(result.unknownLease), "before another action.")
		}
		if op := result.poolPending; op != nil && (op.state == "unknown" || op.busy != "") {
			fmt.Fprintln(output, "Pool outcome may be unknown; retain exact request/key:", safe(poolRequestJSON(op.request)))
		}
		if result.pendingMessage != nil {
			fmt.Fprintln(output, "Message outcome may be unknown; inspect idempotency key", safe(result.pendingMessage.Key), "before another send.")
			if raw, marshalErr := json.Marshal(result.pendingMessage); marshalErr == nil {
				fmt.Fprintln(output, "Exact reviewed request (no automatic retry):", safe(string(raw)))
			}
		}
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func newDashboard(ctx context.Context, home string) *dashboardModel {
	input := textinput.New()
	input.CharLimit = 16384
	input.SetWidth(50)
	m := &dashboardModel{ctx: ctx, home: home, width: 100, height: 30, timeNow: time.Now(), connecting: true, diskSelection: ioSelection{All: true}, networkSelection: ioSelection{All: true}, viewport: viewport.New(viewport.WithWidth(65), viewport.WithHeight(20)), input: input}
	m.viewport.SoftWrap = true
	p, err := readPreferences(home)
	m.hideInactive = p.HideInactive
	if err != nil {
		m.notice = "Client settings not loaded: " + safe(err.Error())
	}
	m.resize()
	m.syncViewport()
	return m
}
func (m *dashboardModel) Init() tea.Cmd { return tea.Batch(connect(m.ctx, m.home), clockTick()) }
func (m *dashboardModel) acceptMonitor(page model.MonitorPage) {
	m.history = mergeMonitor(m.history, page, m.page.Epoch)
	m.page = page
	m.timeMonitor = time.Now()
	m.dirty = true
}
func (m *dashboardModel) addEvent(event model.Event) {
	for _, old := range m.events {
		if old.Sequence == event.Sequence {
			return
		}
	}
	id, previous := "", m.selected
	if m.feedFocus {
		id = m.selectionID()
	}
	m.events = append(m.events, event)
	if len(m.events) > 200 {
		m.events = m.events[len(m.events)-200:]
	}
	if m.feedFocus {
		m.restoreSelection(id, previous)
	}
	m.dirty = true
}
func (m *dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer m.syncViewport()
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, min(500, msg.Width))
		m.height = max(1, min(200, msg.Height))
		m.resize()
	case connectedMsg:
		m.connecting = false
		if msg.err != nil {
			m.notice = msg.err.Error()
			m.retry++
			return m, tea.Tick(time.Duration(min(15, 1<<min(m.retry, 4)))*time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
		m.session = msg.session
		// Reconnection is an authoritative journal snapshot. The database may
		// have been replaced and sequence values may restart at this endpoint.
		m.events = nil
		m.applySnapshot(msg.data)
		m.connected = true
		m.retry = 0
		m.lastRefresh = time.Now()
		m.notice = ""
		m.acceptMonitor(msg.page)
		for _, event := range msg.events {
			m.addEvent(event)
		}
		return m, waitStream(m.ctx, m.session)
	case reconnectMsg:
		if !m.connected && !m.connecting {
			m.connecting = true
			return m, connect(m.ctx, m.home)
		}
	case refreshedMsg:
		if msg.session != m.session {
			return m, nil
		}
		m.refreshing = false
		if msg.err == nil {
			m.applySnapshot(msg.data)
			m.lastRefresh = time.Now()
		} else {
			m.notice = msg.err.Error()
		}
	case streamMsg:
		if msg.session != m.session {
			return m, nil
		}
		if msg.err != nil {
			m.connected = false
			m.detailGeneration++
			m.refreshing = false
			m.review = nil
			if m.poolUI != nil {
				m.poolUI.generation++
				m.poolUI.loading = false
				m.poolUI.review = nil
				m.poolUI.form = nil
				if strings.HasPrefix(m.inputMode, "pool:") {
					m.inputMode = ""
					m.input.Blur()
				}
			}
			m.notice = "Disconnected; observations are stale. Reconnecting…"
			m.retry++
			return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
		}
		if msg.event != nil {
			m.addEvent(*msg.event)
		}
		if msg.page != nil {
			m.acceptMonitor(*msg.page)
		}
		return m, waitStream(m.ctx, m.session)
	case tickMsg:
		m.timeNow = time.Time(msg)
		if m.connected && m.dirty && !m.refreshing && m.timeNow.Sub(m.lastRefresh) >= time.Second {
			m.refreshing = true
			m.dirty = false
			return m, tea.Batch(clockTick(), refresh(m.ctx, m.session))
		}
		return m, clockTick()
	case poolReadMsg:
		return m, m.acceptPoolRead(msg)
	case poolResultMsg:
		return m, m.acceptPoolResult(msg)
	case detailMsg:
		return m, m.acceptDetail(msg)
	case leaseResultMsg:
		return m, m.acceptLeaseResult(msg)
	case messageResultMsg:
		return m, m.acceptMessageResult(msg)
	case lookupResultMsg:
		return m, m.acceptLookup(msg)
	case dashboardMouseMsg:
		return m, m.acceptMouse(msg)
	case tea.MouseMsg:
		return m, nil
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	if m.inputMode != "" {
		m.input, cmd = m.input.Update(msg)
	} else {
		m.viewport, cmd = m.viewport.Update(msg)
	}
	return m, cmd
}

func (m *dashboardModel) key(key tea.KeyPressMsg) tea.Cmd {
	k := key.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	if k == "q" && m.inputMode == "" {
		return tea.Quit
	}
	if m.inputMode != "" {
		if k == "esc" {
			if strings.HasPrefix(m.inputMode, "pool:") {
				return m.poolKey("esc")
			}
			m.inputMode = ""
			m.input.Blur()
			return nil
		}
		if k == "enter" {
			return m.acceptInput()
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return cmd
	}
	if m.ioPicker != nil {
		return m.pickerKey(k)
	}
	if m.review != nil {
		return m.reviewKey(k)
	}
	if m.composer != nil {
		return m.composeKey(k)
	}
	if m.poolUI != nil && m.record == nil && m.detail == nil {
		if !m.poolModal() && k >= "1" && k <= "6" {
			m.poolUI.generation++
			m.poolUI = nil
			m.resourceID = ""
		} else {
			return m.poolKey(k)
		}
	}
	if k == "j" || k == "k" || k == "up" || k == "down" {
		defer m.keepSelectionVisible()
	}
	if m.record != nil {
		switch k {
		case "esc":
			m.record = nil
			m.viewport.GotoTop()
		case "j", "down":
			m.record.selected = min(len(m.record.lines)-1, m.record.selected+1)
			m.viewport.SetYOffset(max(0, m.record.selected-m.viewport.Height()+2))
		case "k", "up":
			m.record.selected = max(0, m.record.selected-1)
			m.viewport.SetYOffset(min(m.viewport.YOffset(), m.record.selected))
		case "enter", " ":
			m.record.toggle()
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(key)
			return cmd
		}
		return nil
	}
	if m.detail != nil {
		switch k {
		case "esc":
			if !m.pending {
				m.detailGeneration++
				m.detail = nil
				m.viewport.GotoTop()
			}
		case "e":
			return m.startReview("renew")
		case "x":
			return m.startReview("release")
		case "c":
			return m.startReview("cancel")
		case "r":
			return m.loadDetail(m.detail.ID)
		case "i":
			m.record, _ = newRecord(m.detail, "Lease")
			m.viewport.GotoTop()
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(key)
			return cmd
		}
		return nil
	}
	if m.resourceID != "" {
		resource := m.selectedResource()
		switch k {
		case "esc":
			m.detailGeneration++
			m.resourceID = ""
			m.selected = 0
			m.viewport.GotoTop()
		case "p":
			return m.openPools()
		case "i":
			m.record, _ = newRecord(resource, "Resource")
			m.viewport.GotoTop()
		case "enter":
			if resource != nil && m.selected < len(resource.Users) {
				return m.loadDetail(resource.Users[m.selected].Lease)
			}
		case "j", "down":
			if resource != nil {
				m.selected = min(max(0, len(resource.Users)-1), m.selected+1)
				m.viewport.ScrollDown(2)
			}
		case "k", "up":
			m.selected = max(0, m.selected-1)
			m.viewport.ScrollUp(2)
		default:
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(key)
			return cmd
		}
		return nil
	}
	if k >= "1" && k <= "6" {
		m.detailGeneration++
		m.view = int(k[0] - '1')
		m.selected = 0
		m.filter = ""
		m.feedFocus = false
		m.feedOpen = false
		m.viewport.GotoTop()
		return nil
	}
	switch k {
	case "p":
		if m.view == 1 && !m.feedFocus {
			return m.openPools()
		}
	case "h":
		if m.view == 2 && !m.feedFocus {
			id, previous := m.selectionID(), m.selected
			m.hideInactive = !m.hideInactive
			m.restoreSelection(id, previous)
			m.viewport.GotoTop()
			if err := writePreferences(m.home, preferences{Version: 1, HideInactive: m.hideInactive}); err != nil {
				m.notice = "Filter changed for this session; saving failed: " + safe(err.Error())
			} else {
				m.notice = "Lease visibility preference saved"
			}
		}
	case "q":
		return tea.Quit
	case "tab":
		m.feedFocus = !m.feedFocus
		m.selected = 0
		m.viewport.GotoTop()
	case "f":
		m.feedOpen = !m.feedOpen
		if m.width < 110 {
			m.feedFocus = m.feedOpen
			m.selected = 0
			m.viewport.GotoTop()
		}
	case "?":
		m.record, _ = newRecord(map[string]any{"Keys": "1–6 navigate · ↑↓/jk select · Enter inspect · Tab live feed · f toggle compact feed · / filter · i record · r refresh · q exit", "Observations": "Monitor shows reported host activity. Reservations are in Resources; capacity is not a performance guarantee.", "Memory": "Available memory changes with host pressure. Existing reservations can exceed a falling budget; new grants wait.", "Storage": "APFS volumes share container capacity. Clones and snapshots can share blocks; allocated size does not prove exclusive or reclaimable space.", "Disk and network": "Charts sum selected reported counters. Layered disks and virtual interfaces can overlap, so the sum is not exclusive physical traffic. Open [d]/[n] and [?] for selection, missing-counter and topology-gap details.", "Cleanup": "Release of managed storage can stop attached ARM-supervised work and reclaim managed files after safety checks.", "Authority": "Local native operator connection. Closing the dashboard leaves leases running. This dashboard shows the local host; peer views are not available here."}, "Dashboard help")
		m.viewport.GotoTop()
	case "/":
		m.inputMode = "filter"
		m.input.SetValue(m.filter)
		return m.input.Focus()
	case "r":
		if m.connected && !m.refreshing {
			m.refreshing = true
			return refresh(m.ctx, m.session)
		}
	case "n":
		if m.view == 0 {
			return m.openIOPicker(false)
		}
		if m.view == 3 {
			return m.beginCompose(false)
		}
	case "b":
		if m.view == 3 {
			return m.beginCompose(true)
		}
	case "d":
		if m.view == 0 {
			return m.openIOPicker(true)
		}
	case "y":
		if m.view == 3 {
			return m.replyMessage()
		}
	case "i":
		m.record, _ = newRecord(m.currentRecord(), viewNames[m.view])
		m.viewport.GotoTop()
	case "j", "down":
		if !m.feedFocus && (m.view == 0 || m.view == 5) {
			m.viewport.ScrollDown(1)
			return nil
		}
		m.selected = min(max(0, m.itemCount()-1), m.selected+1)
		m.viewport.SetYOffset(max(0, m.selected*3-m.viewport.Height()+3))
	case "k", "up":
		if !m.feedFocus && (m.view == 0 || m.view == 5) {
			m.viewport.ScrollUp(1)
			return nil
		}
		m.selected = max(0, m.selected-1)
		m.viewport.SetYOffset(min(m.viewport.YOffset(), m.selected*3))
	case "enter":
		return m.openSelected()
	case "esc":
		m.detailGeneration++
		m.filter = ""
		m.feedOpen = false
		m.feedFocus = false
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(key)
		return cmd
	}
	return nil
}
func (m *dashboardModel) acceptInput() tea.Cmd {
	value := m.input.Value()
	mode := m.inputMode
	m.inputMode = ""
	m.input.Blur()
	if strings.HasPrefix(mode, "pool:") {
		return m.poolAcceptInput(value)
	}
	switch mode {
	case "filter":
		m.filter = strings.TrimSpace(value)
		m.selected = 0
		m.viewport.GotoTop()
	case "ttl":
		return m.reviewTTL(value)
	case "recipient":
		return m.composeRecipient(value)
	case "body":
		return m.composeBody(value)
	}
	return nil
}
