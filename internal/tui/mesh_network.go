package tui

import (
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
	mode       string
	registry   model.NodeRegistry
	operations []meshops.Operation
	node       *model.NodeEntry
	operation  *meshops.Operation
	selected   int
	sequence   uint64
	loading    bool
	err        string
	form       *meshForm
	prompt     *meshPrompt
	unknown    *meshIntent
	intent     *pendingRecord
	busy       bool
	watching   bool
	dirty      bool
}

type meshReadMsg struct {
	ui         *meshDashboard
	session    transport
	sequence   uint64
	mode       string
	registry   model.NodeRegistry
	operations []meshops.Operation
	node       *model.NodeEntry
	operation  *meshops.Operation
	err        error
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
	Key       string          `json:"key"`
	Operation string          `json:"operation"`
	Params    json.RawMessage `json:"params"`
}

func newMeshIntent(operation, key string, params any) (meshIntent, error) {
	raw, err := json.Marshal(params)
	q := meshIntent{Key: key, Operation: operation, Params: raw}
	if err == nil {
		err = q.validate()
	}
	return q, err
}

func (q meshIntent) validate() error {
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
		if (p.Kind == "connect" && (p.Connect == nil || p.Disconnect != nil)) || (p.Kind == "disconnect" && (p.Disconnect == nil || p.Connect != nil)) || (p.Kind != "connect" && p.Kind != "disconnect") {
			return fmt.Errorf("exact connect or disconnect draft required")
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
		case "node":
			msg.node = &model.NodeEntry{}
			msg.err = session.Call(ctx, "node_inspect", map[string]string{"host": id}, msg.node)
		case "operation":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_get", map[string]string{"key": id}, msg.operation)
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
	if msg.err != nil {
		ui.err = safe(msg.err.Error())
		return nil
	}
	ui.err = ""
	switch msg.mode {
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
	case "node":
		changed := ui.node == nil || msg.node == nil || ui.node.Host != msg.node.Host
		ui.node = msg.node
		ui.mode = "node"
		if changed {
			m.viewport.GotoTop()
		}
	case "operation":
		changed := ui.operation == nil || msg.operation == nil || ui.operation.Key != msg.operation.Key
		ui.operation = msg.operation
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
	if !mesh.ValidKey(op.Key) || op.Revision <= 0 || (op.Kind != "connect" && op.Kind != "disconnect") || op.Phase == "" {
		return fmt.Errorf("invalid coordinator receipt; retain exact request/key and inspect its durable outcome")
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
