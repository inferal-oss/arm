package tui

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
)

type meshDashboard struct {
	stopLeaseIndex     int
	stopLeaseSelection uint64
	stopLease          *meshops.StopLeaseDetail
	mode               string
	registry           model.NodeRegistry
	operations         []meshops.Operation
	node               *model.NodeEntry
	operation          *meshops.Operation
	selected           int
	sequence           uint64
	loading            bool
	err                string
	form               *meshForm
	prompt             *meshPrompt
	unknown            *meshIntent
	intent             *pendingRecord
	busy               bool
	watching           bool
	dirty              bool
}

type meshReconnectContext struct {
	Host     string                     `json:"host"`
	Blockers []meshops.ReconnectBlocker `json:"blockers"`
}

type meshReadMsg struct {
	stopLease          *meshops.StopLeaseDetail
	stopLeaseRequest   *meshops.StopLeaseRequest
	stopLeaseSelection uint64
	ui                 *meshDashboard
	session            transport
	sequence           uint64
	mode               string
	registry           model.NodeRegistry
	operations         []meshops.Operation
	node               *model.NodeEntry
	operation          *meshops.Operation
	reconnect          *meshReconnectContext
	err                error
}

type meshSendMsg struct {
	ui      *meshDashboard
	session transport
	request meshIntent
	intent  *pendingRecord
	result  meshops.Operation
	err     error
}

type workflowTransport interface {
	WorkflowChanges() <-chan client.WorkflowChange
}

type meshIntent struct {
	LookupOnly bool            `json:"lookup_only,omitempty"`
	Key        string          `json:"key"`
	Operation  string          `json:"operation"`
	Params     json.RawMessage `json:"params"`
}

func newMeshIntent(operation, key string, params any) (meshIntent, error) {
	raw, err := json.Marshal(params)
	q := meshIntent{Key: key, Operation: operation, Params: raw}
	if draft, ok := params.(meshPrepareRequest); ok && draft.Kind == "stop" {
		q.LookupOnly = true
	}
	if err == nil {
		err = q.validate()
	}
	return q, err
}

func (q meshIntent) validate() error {
	if q.LookupOnly && q.Operation != "mesh_operator_prepare" && q.Operation != "mesh_operator_decide" && q.Operation != "mesh_operator_cancel" {
		return fmt.Errorf("Stop recovery permits lookup only, no resume or handoff")
	}
	if !mesh.ValidKey(q.Key) || len(q.Params) > pendingRequestLimit {
		return fmt.Errorf("bounded exact coordinator key and request required")
	}
	var nested struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(q.Params, &nested) != nil || nested.Key != q.Key {
		return fmt.Errorf("coordinator request key differs from retained envelope")
	}
	switch q.Operation {
	case "mesh_operator_prepare":
		var p meshPrepareRequest
		if err := strictPendingJSON(q.Params, &p); err != nil {
			return err
		}
		count := 0
		for _, present := range []bool{p.Connect != nil, p.Reconnect != nil, p.Disconnect != nil, p.Stop != nil} {
			if present {
				count++
			}
		}
		if count != 1 || (p.Kind == "connect" && p.Connect == nil) || (p.Kind == "reconnect" && p.Reconnect == nil) || (p.Kind == "disconnect" && p.Disconnect == nil) || (p.Kind == "stop" && (p.Stop == nil || !q.LookupOnly)) || (p.Kind != "connect" && p.Kind != "reconnect" && p.Kind != "disconnect" && p.Kind != "stop") {
			return fmt.Errorf("exact connect, reconnect, disconnect or lookup-only Stop draft required")
		}
		if q.LookupOnly && p.Kind != "stop" {
			return fmt.Errorf("lookup-only Stop envelope changed operation kind")
		}
		if p.Stop != nil && !validMeshStopHost(p.Stop.Host) {
			return fmt.Errorf("stop requires an exact stable host")
		}

		if p.Reconnect != nil {
			raw, e := hex.DecodeString(p.Reconnect.Host)
			if e != nil || len(raw) != 16 || hex.EncodeToString(raw) != p.Reconnect.Host {
				return fmt.Errorf("reconnect requires an exact stable host")
			}
		}
	case "mesh_operator_decide":
		var p meshops.Decision
		if err := strictPendingJSON(q.Params, &p); err != nil {
			return err
		}
		if p.ReviewRevision <= 0 || len(p.ReviewHash) != 64 {
			return fmt.Errorf("exact reviewed revision/hash required")
		}
	case "mesh_operator_cancel", "mesh_operator_resume", "mesh_operator_handoff":
		var p meshRevisionRequest
		if err := strictPendingJSON(q.Params, &p); err != nil {
			return err
		}
		if p.Revision <= 0 {
			return fmt.Errorf("exact operation revision required")
		}
		if q.Operation == "mesh_operator_handoff" && (len(p.Hash) != 64 || p.Reason == "") {
			return fmt.Errorf("reviewed recovery hash and reason required")
		}
	default:
		return fmt.Errorf("unsupported retained Mesh operation")
	}
	return nil
}

func (m *dashboardModel) openMesh() tea.Cmd {
	if m.meshUI == nil {
		m.meshUI = &meshDashboard{mode: "nodes"}
	}
	m.view = 6
	m.feedFocus = false
	m.viewport.GotoTop()
	return m.readMesh("list", "")
}

func (m *dashboardModel) readMesh(mode, id string) tea.Cmd {
	ui, session := m.meshUI, m.session
	if ui == nil || !m.connected || ui.loading || ui.busy {
		return nil
	}
	var stopRevision int64
	expectedStopTarget := ""
	if ui.operation != nil && ui.operation.Kind == "stop" && ui.operation.Key == id && len(ui.operation.Targets) == 1 {
		expectedStopTarget = ui.operation.Targets[0]
	}
	if ui.unknown != nil && ui.unknown.LookupOnly && ui.unknown.Key == id && ui.unknown.Operation == "mesh_operator_prepare" {
		var draft meshPrepareRequest
		if json.Unmarshal(ui.unknown.Params, &draft) == nil && draft.Stop != nil {
			expectedStopTarget = "host:" + draft.Stop.Host
		}
	}
	if mode == "stop-lookup" {
		if ui.operation == nil || ui.operation.Key != id || ui.operation.Kind != "stop" || (!ui.operation.LookupOnly && !ui.operation.Approved) {
			ui.err = "Inspect the exact admitted Stop receipt before checking its original process."
			return nil
		}
		stopRevision = ui.operation.Revision
	}
	var leaseRequest *meshops.StopLeaseRequest
	var leaseOperation *meshops.Operation
	leaseSelection := ui.stopLeaseSelection
	if mode == "stop-lease" {
		q, err := meshStopLeaseRequest(ui)
		if err != nil {
			ui.err = safe(err.Error())
			return nil
		}
		leaseRequest = &q
		copy := *ui.operation
		copy.Review = append(json.RawMessage(nil), copy.Review...)
		leaseOperation = &copy
		ui.stopLease = nil
	}
	ui.loading = true
	ui.dirty = false
	ui.sequence++
	sequence := ui.sequence
	ctx := m.ctx
	return func() tea.Msg {
		msg := meshReadMsg{ui: ui, session: session, sequence: sequence, mode: mode}
		switch mode {
		case "list":
			msg.err = session.Call(ctx, "node_list", nil, &msg.registry)
			if msg.err == nil {
				msg.err = session.Call(ctx, "mesh_operator_list", nil, &msg.operations)
			}
		case "reconnect":
			msg.reconnect = &meshReconnectContext{}
			msg.err = session.Call(ctx, "mesh_operator_reconnect_context", map[string]string{"host": id}, msg.reconnect)
			if msg.err == nil && (msg.reconnect.Host != id || msg.reconnect.Blockers == nil) {
				msg.err = fmt.Errorf("exact reconnect context unavailable")
			}
		case "node", "stop":
			msg.node = &model.NodeEntry{}
			msg.err = session.Call(ctx, "node_inspect", map[string]string{"host": id}, msg.node)
			if mode == "stop" && msg.err == nil && msg.node.Host != id {
				msg.err = fmt.Errorf("exact Stop host changed during refresh")
			}
		case "stop-lease":
			msg.stopLease = &meshops.StopLeaseDetail{}
			msg.stopLeaseRequest = leaseRequest
			msg.stopLeaseSelection = leaseSelection
			msg.err = session.Call(ctx, "mesh_operator_stop_lease", leaseRequest, msg.stopLease)
			if msg.err == nil && !meshStopLeaseMatches(leaseOperation, *leaseRequest, *msg.stopLease) {
				msg.err = fmt.Errorf("Exact reviewed destination lease differs; no details displayed")
			}
		case "stop-lookup":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_stop_lookup", map[string]any{"key": id, "revision": stopRevision}, msg.operation)
			if msg.err == nil && (msg.operation.Key != id || msg.operation.Kind != "stop") {
				msg.err = fmt.Errorf("original Stop lookup returned a different receipt")
			}
		case "operation":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_get", map[string]string{"key": id}, msg.operation)
		}
		if msg.err == nil && msg.operation != nil && (msg.operation.Kind == "stop" || expectedStopTarget != "") {
			if msg.operation.Key != id || !meshStopOperationBound(*msg.operation) || (expectedStopTarget != "" && msg.operation.Targets[0] != expectedStopTarget) {
				msg.err = fmt.Errorf("Stop lookup returned a different exact host/review")
			}
		}
		return msg
	}
}

func (m *dashboardModel) acceptMeshRead(msg meshReadMsg) tea.Cmd {
	ui := m.meshUI
	if ui == nil || msg.ui != ui || msg.session != m.session || msg.sequence != ui.sequence {
		return nil
	}
	ui.loading = false
	// Retire the original call only when its actual result arrives. Selection
	// uses its own epoch; it never abandons loading or the Service read owner.
	if msg.mode == "stop-lease" {
		current, err := meshStopLeaseRequest(ui)
		if err != nil || msg.stopLeaseRequest == nil || msg.stopLeaseSelection != ui.stopLeaseSelection || current != *msg.stopLeaseRequest {
			return nil
		}
	}
	if msg.err != nil {
		ui.err = safe(msg.err.Error())
		return nil
	}
	ui.err = ""
	switch msg.mode {
	case "stop-lease":
		if msg.stopLeaseRequest == nil || msg.stopLease == nil || !meshStopLeaseMatches(ui.operation, *msg.stopLeaseRequest, *msg.stopLease) {
			ui.err = "Stop review changed while reading lease details. Refresh its exact receipt."
			return nil
		}
		ui.stopLease = msg.stopLease
		m.viewport.GotoTop()
	case "reconnect":
		if ui.form != nil && msg.reconnect != nil && ui.form.reconnectHost == msg.reconnect.Host {
			form := ui.form
			selectedKey := ""
			if form.blockerIndex >= 0 && form.blockerIndex < len(form.blockers) {
				selectedKey = form.blockers[form.blockerIndex].Key
			}
			form.blockerIndex = -1
			for i, blocker := range msg.reconnect.Blockers {
				if selectedKey != "" && blocker.Key == selectedKey {
					form.blockerIndex = i
					break
				}
			}
			// The first observation may select its first item. Later changed lists
			// preserve only an exact selected key; removal never retargets inspection.
			if !form.contextSeen && len(msg.reconnect.Blockers) > 0 {
				form.blockerIndex = 0
			}
			form.contextSeen = true
			form.contextReady = true
			form.blockers = msg.reconnect.Blockers
		}
	case "list":
		ui.registry, ui.operations = msg.registry, msg.operations
		if ui.mode == "operation" && ui.operation != nil || ui.mode == "node" && ui.node != nil {
			ui.dirty = true
		}
		ui.selected = min(ui.selected, max(0, m.meshItemCount()-1))
		if !ui.watching {
			ui.watching = true
			// Stream installation is read-only. Failure is surfaced; refresh never
			// retries a pending coordinator mutation.
			session, ctx := m.session, m.ctx
			return func() tea.Msg {
				err := session.Call(ctx, "mesh_operator_watch", map[string]any{}, nil)
				return meshWatchMsg{ui: ui, session: session, err: err}
			}
		}
	case "stop":
		if msg.node == nil {
			ui.err = "Exact stop node unavailable"
			return nil
		}
		allowed, why := meshStopAvailability(msg.node)
		if !allowed {
			ui.err = why
			return nil
		}
		ui.node = msg.node
		ui.mode = "node"
		ui.operation = nil
		ui.form = newMeshStopForm(msg.node.Host)
		m.viewport.GotoTop()
	case "node":
		changed := ui.node == nil || msg.node == nil || ui.node.Host != msg.node.Host
		ui.node = msg.node
		ui.mode = "node"
		if changed {
			m.viewport.GotoTop()
		}
	case "operation", "stop-lookup":
		changed := ui.operation == nil || msg.operation == nil || ui.operation.Key != msg.operation.Key
		if changed || ui.operation.ReviewRevision != msg.operation.ReviewRevision || ui.operation.ReviewHash != msg.operation.ReviewHash {
			ui.stopLeaseIndex = 0
			ui.stopLease = nil
		}
		ui.operation = msg.operation
		if ui.unknown != nil && ui.unknown.LookupOnly && msg.operation != nil && msg.operation.Kind == "stop" && msg.operation.Key == ui.unknown.Key {
			known := msg.operation.LookupOnly || msg.operation.CanPrepareFresh || ui.unknown.Operation == "mesh_operator_prepare"
			if known && validateMeshOutcome(*ui.unknown, *msg.operation) == nil && m.finishDurable(ui.intent, false) {
				ui.unknown = nil
				ui.intent = nil
			}
		}
		ui.mode = "operation"
		if changed {
			m.viewport.GotoTop()
		}
	}
	return nil
}

type meshWatchMsg struct {
	ui      *meshDashboard
	session transport
	err     error
}

func (m *dashboardModel) sendMesh(q meshIntent, retry bool) tea.Cmd {
	ui := m.meshUI
	if ui == nil || !m.connected || ui.busy {
		return nil
	}
	if ui.unknown != nil && (ui.unknown.LookupOnly || q.LookupOnly) {
		ui.err = "An unresolved Stop request is retained. Read its exact receipt; no Stop request or decision is resent."
		return nil
	}
	if retry && q.LookupOnly {
		ui.err = "Stop recovery is lookup-only. Read the exact receipt; no Stop request is resent."
		return nil
	}
	if retry && q.Operation == "mesh_operator_resume" {
		ui.err = "Uncertain recovery resumes cannot be resent. Read the exact parent and recorded child attempts; a separately keyed recovery API is required for safe resend."
		return nil
	}
	if retry && (ui.intent == nil || ui.intent.Authority != m.data.status.Host) || ui.registry.Source != "" && ui.registry.Source != m.data.status.Host {
		ui.err = "LOCAL authority changed. Retained requests are not submitted to another source; inspect the original ledger."
		return nil
	}
	if err := q.validate(); err != nil {
		ui.err = err.Error()
		return nil
	}
	intent, err := m.prepareDurable("mesh", m.data.status.Host, q, retry)
	if err != nil {
		ui.err = err.Error()
		return nil
	}
	ui.busy = true
	ui.unknown = &q
	ui.intent = intent
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		msg := meshSendMsg{ui: ui, session: session, request: q, intent: intent}
		msg.err = session.Call(ctx, q.Operation, q.Params, &msg.result)
		if msg.err == nil {
			msg.err = validateMeshOutcome(q, msg.result)
		}
		return msg
	}
}

func validateMeshOutcome(q meshIntent, op meshops.Operation) error {
	if !mesh.ValidKey(op.Key) || op.Revision <= 0 || (op.Kind != "connect" && op.Kind != "reconnect" && op.Kind != "disconnect" && op.Kind != "stop") || op.Phase == "" {
		return fmt.Errorf("invalid coordinator receipt; retain exact request/key and inspect its durable outcome")
	}
	if q.LookupOnly && op.Kind != "stop" {
		return fmt.Errorf("lookup-only Stop receipt changed operation kind")
	}
	if op.Kind == "stop" && !meshStopOperationBound(op) {
		return fmt.Errorf("Stop receipt differs from its exact host target or review")
	}
	if q.LookupOnly && q.Operation == "mesh_operator_decide" {
		var decision meshops.Decision
		if strictPendingJSON(q.Params, &decision) != nil || decision.ReviewRevision != op.ReviewRevision || decision.ReviewHash != op.ReviewHash || ((op.Phase == "preparing" || op.Phase == "awaiting_review") && !op.LookupOnly && !op.CanPrepareFresh) {
			return fmt.Errorf("Stop decision response changed exact reviewed revision/hash")
		}
	}
	if q.Operation == "mesh_operator_prepare" {
		var p meshPrepareRequest
		if strictPendingJSON(q.Params, &p) != nil || p.Kind != op.Kind {
			return fmt.Errorf("coordinator result kind differs from exact prepared intent")
		}
		if p.Stop != nil && (op.Kind != "stop" || len(op.Targets) != 1 || op.Targets[0] != "host:"+p.Stop.Host) {
			return fmt.Errorf("Stop preparation changed selected host")
		}
	}
	if q.Operation == "mesh_operator_resume" {
		if op.ParentKey != q.Key {
			return fmt.Errorf("recovery receipt is not bound to the original intent")
		}
	} else if op.Key != q.Key {
		return fmt.Errorf("coordinator receipt differs from the exact request key")
	}
	return nil
}

func (m *dashboardModel) acceptMeshSend(msg meshSendMsg) tea.Cmd {
	// Durable settlement belongs to the exact request even if a newer UI is
	// visible. An uncertain response never settles an unrelated operation.
	known := msg.err == nil
	initialRejected := false
	var api *model.Error
	if msg.intent != nil && msg.intent.Attempts == 1 && errors.As(msg.err, &api) {
		switch api.Code {
		case "invalid_request", "forbidden", "not_found", "review_changed", "wrong_host", "setup_changed":
			initialRejected = true
		}
	}
	settled := false
	if known || initialRejected {
		settled = m.finishDurable(msg.intent, initialRejected)
	}
	if m.meshUI != msg.ui {
		return nil
	}
	ui := m.meshUI
	if msg.intent == nil || ui.intent == nil || ui.intent.Attempts != msg.intent.Attempts || !samePending(*ui.intent, *msg.intent) || m.data.status.Host != msg.intent.Authority {
		return nil
	}
	ui.busy = false
	ui.prompt = nil
	if msg.err != nil {
		if initialRejected && settled {
			ui.unknown = nil
			ui.intent = nil
			ui.err = "Request rejected before application: " + safe(msg.err.Error())
			return nil
		}
		ui.err = "Outcome may be unknown: " + safe(msg.err.Error()) + ". The exact key/request is retained; lookup or same-request retry is explicit."
		if msg.request.LookupOnly {
			ui.err = "Stop outcome may be unknown: " + safe(msg.err.Error()) + ". Read this exact key; resend is disabled."
		}
		return nil
	}
	if settled {
		ui.unknown = nil
		ui.intent = nil
	} else {
		ui.err = "The backend response is known, but private recovery storage still needs attention. The exact request remains retained."
	}
	if settled {
		ui.err = ""
	}
	ui.form = nil
	ui.operation = &msg.result
	ui.mode = "operation"
	m.viewport.GotoTop()
	return nil
}

func workflowChannel(session transport) <-chan client.WorkflowChange {
	if stream, ok := session.(workflowTransport); ok {
		return stream.WorkflowChanges()
	}
	return nil
}
