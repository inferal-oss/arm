package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

type poolDashboard struct {
	mode         string
	views        []model.PoolView
	current      *model.PoolView
	selected     int
	loading      bool
	generation   uint64
	history      model.PoolHistory
	historyStart int64
	previous     []int64
	form         *poolForm
	review       *poolPlan
	returnOffset int
}
type poolForm struct {
	draft  poolDraft
	fields []string
	step   int
	values map[string]string
}
type poolOperation struct {
	request            model.PoolMutation
	state, busy, error string
	change             *model.PoolChange
	sequence           uint64
}
type poolReadMsg struct {
	ui         *poolDashboard
	generation uint64
	session    transport
	kind       string
	views      []model.PoolView
	view       model.PoolView
	history    model.PoolHistory
	start      int64
	draft      *poolDraft
	err        error
}
type poolResultMsg struct {
	sequence     uint64
	request      model.PoolMutation
	change       model.PoolChange
	lookup       *manager.PoolMutationLookup
	err          error
	priorUnknown bool
}

func (m *dashboardModel) openPools() tea.Cmd {
	if m.poolUI == nil {
		m.poolUI = &poolDashboard{mode: "list", returnOffset: m.viewport.YOffset()}
		m.feedFocus = false
	}
	return m.readPools("list", "", 0, nil)
}
func (m *dashboardModel) readPools(kind, id string, after int64, draft *poolDraft) tea.Cmd {
	if !m.connected || m.poolUI == nil {
		return nil
	}
	ui := m.poolUI
	ui.generation++
	generation := ui.generation
	ui.loading = true
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		result := poolReadMsg{ui: ui, generation: generation, session: session, kind: kind, start: after, draft: draft}
		switch kind {
		case "list":
			result.err = session.Call(ctx, "pool_list", map[string]bool{"all": true}, &result.views)
		case "detail", "prepare":
			result.err = session.Call(ctx, "pool_inspect", map[string]string{"id": id}, &result.view)
		case "history":
			result.err = session.Call(ctx, "pool_history", map[string]any{"id": id, "after": after}, &result.history)
		}
		return result
	}
}
func (m *dashboardModel) acceptPoolRead(result poolReadMsg) tea.Cmd {
	ui := m.poolUI
	if ui == nil || result.ui != ui || result.generation != ui.generation || result.session != m.session {
		return nil
	}
	ui.loading = false
	if result.err != nil {
		m.notice = safe(result.err.Error())
		return nil
	}
	switch result.kind {
	case "list":
		id := ""
		if ui.selected < len(ui.views) {
			id = ui.views[ui.selected].Pool.ID
		}
		ui.views = result.views
		ui.mode = "list"
		ui.selected = 0
		for i, view := range ui.views {
			if view.Pool.ID == id {
				ui.selected = i
			}
		}
		m.viewport.GotoTop()
	case "detail":
		view := result.view
		ui.current = &view
		ui.mode = "detail"
		ui.selected = 0
		m.viewport.GotoTop()
	case "history":
		ui.history = result.history
		ui.historyStart = result.start
		ui.mode = "history"
		ui.selected = 0
		m.viewport.GotoTop()
	case "prepare":
		if result.draft == nil {
			return nil
		}
		view := result.view
		ui.current = &view
		plan, err := preparePoolPlan(*result.draft, &view, m.data.status.Host)
		if err != nil {
			m.notice = err.Error()
			ui.form = nil
			return nil
		}
		ui.form = nil
		ui.review = plan
		m.viewport.GotoTop()
	}
	return nil
}
func (m *dashboardModel) poolKey(key string) tea.Cmd {
	ui := m.poolUI
	if ui == nil {
		return nil
	}
	if ui.review != nil {
		if scrollReview(&m.viewport, key) {
			return nil
		}
		if key == "n" || key == "esc" {
			ui.review = nil
			m.viewport.GotoTop()
			return nil
		}
		if key == "y" && m.connected && ui.review.request.Reviewed.Authority == m.data.status.Host {
			return m.sendPoolRequest(ui.review.request, false)
		}
		return nil
	}
	if ui.form != nil {
		if key == "esc" {
			ui.generation++
			ui.loading = false
			ui.form = nil
			if ui.current == nil {
				ui.mode = "list"
			}
			m.inputMode = ""
			m.input.Blur()
			m.viewport.GotoTop()
		}
		return nil
	}
	if key == "esc" {
		switch ui.mode {
		case "detail", "actions", "operation":
			ui.mode = "list"
			ui.selected = 0
			if ui.current != nil {
				for i, view := range ui.views {
					if view.Pool.ID == ui.current.Pool.ID {
						ui.selected = i
						break
					}
				}
			}
		case "history":
			ui.mode = "detail"
		default:
			offset := ui.returnOffset
			m.poolUI = nil
			m.syncViewport()
			m.viewport.SetYOffset(offset)
			return nil
		}
		m.viewport.GotoTop()
		return nil
	}
	if key == "o" {
		ui.mode = "operation"
		m.viewport.GotoTop()
		return nil
	}
	if ui.mode == "operation" {
		if m.poolPending != nil && m.poolPending.state == "unknown" && m.poolPending.busy == "" {
			if key == "i" {
				return m.lookupPoolRequest()
			}
			if key == "y" {
				return m.sendPoolRequest(m.poolPending.request, true)
			}
		}
		if key == "i" && m.poolPending != nil {
			m.record, _ = newRecord(map[string]any{"request": m.poolPending.request, "state": m.poolPending.state, "error": m.poolPending.error, "change": m.poolPending.change}, "Pool operation")
			m.viewport.GotoTop()
		}
		return nil
	}
	if ui.loading {
		return nil
	}
	if ui.mode == "actions" {
		options := m.poolActionOptions()
		switch key {
		case "up", "k":
			ui.selected = max(0, ui.selected-1)
		case "down", "j":
			ui.selected = min(max(0, len(options)-1), ui.selected+1)
		case "enter":
			if ui.selected < len(options) {
				return m.beginPoolForm(options[ui.selected])
			}
		}
		return nil
	}
	if key == "i" {
		var record any = ui.current
		if ui.mode == "history" {
			record = ui.history
		} else if ui.mode == "list" {
			record = ui.views
		}
		m.record, _ = newRecord(record, "Pool record")
		m.viewport.GotoTop()
		return nil
	}
	if ui.mode == "history" {
		switch key {
		case "n":
			if ui.history.More {
				ui.previous = append(ui.previous, ui.historyStart)
				if len(ui.previous) > 256 {
					ui.previous = ui.previous[1:]
				}
				return m.readPools("history", ui.current.Pool.ID, ui.history.Cursor, nil)
			}
		case "p":
			if len(ui.previous) > 0 {
				after := ui.previous[len(ui.previous)-1]
				ui.previous = ui.previous[:len(ui.previous)-1]
				return m.readPools("history", ui.current.Pool.ID, after, nil)
			}
		case "f":
			ui.previous = nil
			return m.readPools("history", ui.current.Pool.ID, 0, nil)
		default:
			scrollReview(&m.viewport, key)
		}
		return nil
	}
	if ui.mode == "detail" {
		users := append(append([]model.ResourceUse{}, ui.current.Users...), ui.current.Queued...)
		switch key {
		case "down", "j":
			ui.selected = min(max(0, len(users)-1), ui.selected+1)
			m.keepSelectionVisible()
		case "up", "k":
			ui.selected = max(0, ui.selected-1)
			m.keepSelectionVisible()
		case "enter":
			if ui.selected < len(users) {
				return m.loadDetail(users[ui.selected].Lease)
			}
		case "a":
			ui.mode = "actions"
			ui.selected = 0
			m.viewport.GotoTop()
		case "h":
			ui.previous = nil
			return m.readPools("history", ui.current.Pool.ID, 0, nil)
		case "r":
			return m.readPools("detail", ui.current.Pool.ID, 0, nil)
		default:
			scrollReview(&m.viewport, key)
		}
		return nil
	}
	switch key {
	case "n":
		return m.beginPoolForm("create")
	case "r":
		return m.readPools("list", "", 0, nil)
	case "down", "j":
		ui.selected = min(max(0, len(ui.views)-1), ui.selected+1)
		m.keepPoolRowVisible()
	case "up", "k":
		ui.selected = max(0, ui.selected-1)
		m.keepPoolRowVisible()
	case "enter":
		if ui.selected < len(ui.views) {
			return m.readPools("detail", ui.views[ui.selected].Pool.ID, 0, nil)
		}
	default:
		scrollReview(&m.viewport, key)
	}
	return nil
}
func (m *dashboardModel) poolActionOptions() []string {
	ui := m.poolUI
	if ui == nil || ui.current == nil || ui.current.Pool.State == "deleted" {
		return nil
	}
	transition := "drain"
	if ui.current.Pool.State == "draining" {
		transition = "resume"
	}
	return []string{"set", "adjust", transition, "delete"}
}
func (m *dashboardModel) beginPoolForm(operation string) tea.Cmd {
	if m.poolPending != nil && (m.poolPending.busy != "" || m.poolPending.state == "unknown") {
		m.poolUI.mode = "operation"
		m.notice = "Resolve the retained pool request before another change."
		return nil
	}
	if !m.connected {
		return nil
	}
	if m.blockPendingDraft("pool") {
		return nil
	}
	fields := []string{"reason"}
	values := map[string]string{}
	draft := poolDraft{operation: operation}
	if operation == "create" {
		fields = []string{"name", "scope", "unit", "capacity", "description", "reason"}
		values["scope"] = "host"
		values["unit"] = "count"
		values["capacity"] = "0"
	} else {
		if m.poolUI.current == nil {
			return nil
		}
		draft.id = m.poolUI.current.Pool.ID
		if operation == "set" {
			fields = []string{"capacity", "reason"}
			values["capacity"] = fmt.Sprint(m.poolUI.current.Pool.Capacity)
		}
		if operation == "adjust" {
			fields = []string{"delta", "reason"}
			values["delta"] = "1"
		}
	}
	m.poolUI.form = &poolForm{draft: draft, fields: fields, values: values}
	m.poolUI.mode = "detail"
	return m.poolNextInput()
}
func (m *dashboardModel) poolNextInput() tea.Cmd {
	form := m.poolUI.form
	if form.step >= len(form.fields) {
		return m.finishPoolForm()
	}
	field := form.fields[form.step]
	m.inputMode = "pool:" + field
	m.input.SetValue(form.values[field])
	m.viewport.GotoTop()
	return m.input.Focus()
}
func (m *dashboardModel) poolAcceptInput(value string) tea.Cmd {
	if m.poolUI == nil || m.poolUI.form == nil {
		return nil
	}
	form := m.poolUI.form
	form.values[form.fields[form.step]] = value
	form.step++
	return m.poolNextInput()
}
func (m *dashboardModel) finishPoolForm() tea.Cmd {
	ui := m.poolUI
	form := ui.form
	draft := form.draft
	draft.name = form.values["name"]
	draft.scope = strings.TrimSpace(form.values["scope"])
	draft.unit = form.values["unit"]
	draft.description = form.values["description"]
	draft.reason = form.values["reason"]
	draft.value = form.values["capacity"]
	if draft.operation == "adjust" {
		draft.value = form.values["delta"]
	}
	if draft.operation != "create" {
		return m.readPools("prepare", draft.id, 0, &draft)
	}
	plan, err := preparePoolPlan(draft, nil, m.data.status.Host)
	ui.form = nil
	if err != nil {
		m.notice = err.Error()
		ui.mode = "list"
		return nil
	}
	ui.review = plan
	m.viewport.GotoTop()
	return nil
}
func (m *dashboardModel) sendPoolRequest(request model.PoolMutation, priorUnknown bool) tea.Cmd {
	if !m.connected || request.Reviewed == nil || request.Reviewed.Authority != m.data.status.Host {
		m.notice = "Connect to the saved pool authority before retrying this exact request/key."
		return nil
	}
	if m.poolPending != nil && m.poolPending.busy != "" {
		return nil
	}
	intent, err := m.prepareDurable("pool", request.Reviewed.Authority, request, priorUnknown)
	if err != nil {
		m.notice = safe(err.Error())
		return nil
	}
	m.poolIntent = intent
	m.poolSequence++
	sequence := m.poolSequence
	op := &poolOperation{request: copyPoolMutation(request), state: "unknown", busy: "send", sequence: sequence}
	m.poolPending = op
	m.poolUI.review = nil
	m.poolUI.mode = "operation"
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		var change model.PoolChange
		err := session.Call(ctx, "pool_mutate", request, &change)
		return poolResultMsg{sequence: sequence, request: request, change: change, err: err, priorUnknown: priorUnknown}
	}
}
func (m *dashboardModel) lookupPoolRequest() tea.Cmd {
	op := m.poolPending
	if op == nil || op.busy != "" {
		return nil
	}
	if !m.connected || op.request.Reviewed == nil || op.request.Reviewed.Authority != m.data.status.Host {
		m.notice = "Connect to the saved pool authority before looking up this exact key."
		return nil
	}
	m.poolSequence++
	op.sequence = m.poolSequence
	op.busy = "lookup"
	sequence, request, session, ctx := op.sequence, copyPoolMutation(op.request), m.session, m.ctx
	return func() tea.Msg {
		var lookup manager.PoolMutationLookup
		err := session.Call(ctx, "pool_lookup", map[string]string{"idempotency_key": request.Key}, &lookup)
		return poolResultMsg{sequence: sequence, request: request, lookup: &lookup, err: err, priorUnknown: true}
	}
}
func (m *dashboardModel) acceptPoolResult(result poolResultMsg) tea.Cmd {
	op := m.poolPending
	if op == nil || op.sequence != result.sequence || op.busy == "" || !samePoolRequest(op.request, result.request) {
		return nil
	}
	op.busy = ""
	if result.err != nil {
		op.error = safe(result.err.Error())
		if !result.priorUnknown && poolDefiniteError(result.err) {
			if !m.finishDurable(m.poolIntent, true) {
				op.error = m.notice
				return nil
			}
			m.poolIntent = nil
			op.state = "rejected"
		}
		return nil
	}
	if result.lookup != nil {
		lookup := result.lookup
		if !lookup.Found {
			op.error = "No stored result yet. The original request may still commit. Retain this exact request/key."
			return nil
		}
		if lookup.Request == nil || lookup.Change == nil || !samePoolRequest(*lookup.Request, op.request) {
			op.error = "Stored request does not match this exact request/key; outcome remains uncertain."
			return nil
		}
		result.change = *lookup.Change
	}
	if !matchingPoolChange(op.request, result.change) {
		op.error = "Response did not match the reviewed operation; inspect the retained key."
		return nil
	}
	change := result.change
	if !m.finishDurable(m.poolIntent) {
		op.error = m.notice
		op.change = &change
		return nil
	}
	m.poolIntent = nil
	op.change = &change
	op.state = "recorded"
	op.error = ""
	m.dirty = true
	m.notice = "Pool change recorded; inspect the journal for the durable result."
	return nil
}
func poolDefiniteError(err error) bool {
	api, ok := err.(*model.Error)
	if !ok {
		return false
	}
	switch api.Code {
	case "invalid_request", "invalid_contact", "pool_not_found", "pool_deleted", "pool_exists", "pool_in_use", "revision_conflict", "review_conflict", "revision_exhausted", "authority_unavailable", "host_unavailable":
		return true
	}
	return false
}
func poolRequestJSON(request model.PoolMutation) string {
	raw, _ := json.Marshal(request)
	return string(raw)
}
