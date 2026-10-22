package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/meshops"
)

func (m *dashboardModel) meshModal() bool {
	return m.nodeUI == nil && m.meshUI != nil && m.view == 6 && (m.meshUI.form != nil || m.meshUI.prompt != nil) && m.record == nil
}
func (m *dashboardModel) meshKey(key string) tea.Cmd {
	if key == "up" || key == "down" || key == "j" || key == "k" {
		defer m.keepSelectionVisible()
	}
	ui := m.meshUI
	if ui == nil {
		return m.openMesh()
	}
	if ui.busy {
		if key == "i" && ui.unknown != nil {
			m.record, _ = newRecord(ui.unknown, "Exact coordinator request")
		}
		return nil
	}
	if ui.prompt != nil {
		switch key {
		case "i":
			var record any = ui.prompt.request
			if ui.operation != nil {
				record = struct {
					Operation *meshops.Operation `json:"operation"`
					Request   meshIntent         `json:"exact_request"`
				}{ui.operation, ui.prompt.request}
			}
			m.record, _ = newRecord(record, "Full immutable Mesh review and exact request")
			m.viewport.GotoTop()
		case "y":
			return m.sendMesh(ui.prompt.request, ui.prompt.retry)
		case "n", "esc":
			ui.prompt = nil
		default:
			scrollReview(&m.viewport, key)
		}
		return nil
	}
	if ui.form != nil {
		form := ui.form
		switch key {
		case "esc":
			ui.form = nil
		case "up", "k":
			form.selected = max(0, form.selected-1)
		case "down", "j", "tab":
			form.selected = min(len(form.fields)-1, form.selected+1)
		case " ", "space":
			field := &form.fields[form.selected]
			for i, value := range field.choices {
				if value == field.value {
					field.value = field.choices[(i+1)%len(field.choices)]
					break
				}
			}
		case "enter":
			m.inputMode = "mesh:field"
			m.input.SetValue(form.fields[form.selected].value)
			return m.input.Focus()
		case "p":
			q, err := form.request()
			if err != nil {
				ui.err = safe(err.Error())
				return nil
			}
			ui.prompt = &meshPrompt{title: "PREPARE CONNECTION · CONSEQUENCES", text: meshops.PreparationNotice + "\n\nEXACT DRAFT\n" + meshDraftSummary(q.Params), request: q}
			m.viewport.GotoTop()
		}
		return nil
	}
	if ui.unknown != nil {
		switch key {
		case "l":
			return m.readMesh("operation", ui.unknown.Key)
		case "t":
			q := *ui.unknown
			if q.Operation == "mesh_operator_resume" {
				ui.err = "Resume outcome is uncertain. Resend is disabled because it can create another child attempt. [l] reads the exact parent; [o] reads durable attempts. Inspect the recorded child before separately requesting any new recovery. The exact request remains retained."
				return nil
			}
			warning := "This explicitly resends the same request/key. Inspect its durable receipt first. No automatic retry occurs."
			ui.prompt = &meshPrompt{title: "RETRY EXACT UNCERTAIN REQUEST", text: warning + "\n\n" + meshExactJSON(q), request: q, retry: true}
			m.viewport.GotoTop()
			// This prompt is tagged by the retained intent, not a new draft.
		case "i":
			m.record, _ = newRecord(ui.unknown, "Exact retained coordinator request")
			m.viewport.GotoTop()
			return nil
		}
		if key == "l" || key == "t" {
			return nil
		}
		if strings.Contains("ancshd", key) && len(key) == 1 {
			ui.err = "Resolve the exact retained uncertain request before preparing another Mesh mutation."
			return nil
		}
	}
	switch key {
	case "esc":
		if ui.mode == "node" {
			ui.mode = "nodes"
		} else if ui.mode == "operation" {
			ui.mode = "operations"
		} else {
			ui.mode = "nodes"
		}
		ui.node = nil
		ui.operation = nil
		ui.selected = 0
		m.viewport.GotoTop()
	case "o":
		if ui.mode == "operations" {
			ui.mode = "nodes"
		} else {
			ui.mode = "operations"
		}
		ui.node = nil
		ui.operation = nil
		ui.selected = 0
		m.viewport.GotoTop()
		return m.readMesh("list", "")
	case "r":
		if ui.mode == "node" && ui.node != nil {
			return m.readMesh("node", ui.node.Host)
		}
		if ui.mode == "operation" && ui.operation != nil {
			return m.readMesh("operation", ui.operation.Key)
		}
		return m.readMesh("list", "")
	case "n":
		if ui.mode == "operation" {
			return m.meshDecision(false)
		}
		if ui.unknown != nil {
			ui.err = "Inspect the retained uncertain request before preparing another change."
			return nil
		}
		ui.form = newMeshForm()
		ui.err = ""
		m.viewport.GotoTop()
	case "d":
		if ui.node == nil || ui.node.Local {
			ui.err = "Select an exact direct peer to disconnect."
			return nil
		}
		m.inputMode = "mesh:disconnect_reason"
		m.input.SetValue("")
		return m.input.Focus()
	case "v":
		return m.openSelectedNode()
	case "a":
		return m.meshDecision(true)
	case "c", "s", "h":
		if ui.operation == nil {
			return nil
		}
		op := ui.operation
		operation := map[string]string{"c": "mesh_operator_cancel", "s": "mesh_operator_resume", "h": "mesh_operator_handoff"}[key]
		if key == "h" {
			if op.RecoveryHash == "" {
				ui.err = "No exact inactive recovery handoff is currently available."
				return nil
			}
			m.inputMode = "mesh:handoff_reason"
			m.input.SetValue("")
			return m.input.Focus()
		}
		q, err := newMeshIntent(operation, op.Key, meshRevisionRequest{Key: op.Key, Revision: op.Revision})
		if err != nil {
			ui.err = err.Error()
			return nil
		}
		text := "Cancellation is a request, not proof that trust or prior effects were removed. Existing leases keep their lifetimes."
		if key == "s" {
			text = "Explicit recovery creates a bounded new attempt of the immutable draft. Fresh review is required; prior approval does not authorize changed facts."
		}
		ui.prompt = &meshPrompt{title: "REVIEW " + strings.ToUpper(operation), text: text + fmt.Sprintf("\n\nExact operation: %s\nRevision: %d\nCurrent state: %s\n\n[i] Complete collapsible receipt and exact request", safe(op.Key), op.Revision, meshPhaseLabel(*op)), request: q}
		m.viewport.GotoTop()
	case "enter":
		if ui.mode == "nodes" && ui.selected < len(ui.registry.Nodes) {
			return m.readMesh("node", ui.registry.Nodes[ui.selected].Host)
		}
		if ui.mode == "operations" && ui.selected < len(ui.operations) {
			return m.readMesh("operation", ui.operations[ui.selected].Key)
		}
	case "i":
		var record any = ui.registry
		if ui.node != nil {
			record = ui.node
		}
		if ui.operation != nil {
			record = ui.operation
		}
		m.record, _ = newRecord(record, "Mesh · exact local registry or durable operation")
		m.viewport.GotoTop()
	case "p":
		if ui.operation != nil && ui.operation.ParentKey != "" {
			return m.readMesh("operation", ui.operation.ParentKey)
		}
	case "f":
		if ui.operation != nil && ui.operation.ResolvedBy != "" {
			return m.readMesh("operation", ui.operation.ResolvedBy)
		}
	case "down", "j":
		if ui.mode == "nodes" || ui.mode == "operations" {
			ui.selected = min(max(0, m.meshItemCount()-1), ui.selected+1)
		} else {
			m.viewport.ScrollDown(1)
		}
	case "up", "k":
		if ui.mode == "nodes" || ui.mode == "operations" {
			ui.selected = max(0, ui.selected-1)
		} else {
			m.viewport.ScrollUp(1)
		}
	default:
		scrollReview(&m.viewport, key)
	}
	return nil
}

func (m *dashboardModel) meshDecision(approve bool) tea.Cmd {
	ui := m.meshUI
	if ui == nil || ui.operation == nil {
		return nil
	}
	op := *ui.operation
	now := m.timeNow
	if now.IsZero() {
		now = time.Now()
	}
	if !meshCanReview(op, now) {
		ui.err = "Review is not current/live. Read the exact operation and review its current revision before deciding."
		return nil
	}
	summary, supported := meshReviewSummary(op.Review)
	if approve && !supported {
		ui.err = "This review cannot be safely summarized. Inspect the immutable record; approval is unavailable."
		return nil
	}
	q, err := newMeshIntent("mesh_operator_decide", op.Key, meshops.Decision{Key: op.Key, ReviewRevision: op.ReviewRevision, ReviewHash: op.ReviewHash, Approve: approve})
	if err != nil {
		ui.err = err.Error()
		return nil
	}
	action := "DECLINE"
	if approve {
		action = "APPROVE"
	}
	ui.prompt = &meshPrompt{title: "REVIEW " + action + " · EXACT ENROLLMENT", text: fmt.Sprintf("%s this immutable review only. Revision %d\nHash: %s\nExpires: %s\n\n%s\n\n%s", action, op.ReviewRevision, safe(op.ReviewHash), op.ReviewExpires.Format(time.RFC3339), summary, safe(op.Notice)), request: q}
	m.viewport.GotoTop()
	return nil
}

func (m *dashboardModel) acceptMeshInput() tea.Cmd {
	ui := m.meshUI
	if ui == nil {
		return nil
	}
	value := m.input.Value()
	mode := m.inputMode
	m.inputMode = ""
	m.input.Blur()
	switch mode {
	case "mesh:field":
		if ui.form != nil {
			field := &ui.form.fields[ui.form.selected]
			if len(field.choices) > 0 {
				valid := false
				for _, choice := range field.choices {
					valid = valid || choice == value
				}
				if !valid {
					ui.err = "Choose " + strings.Join(field.choices, ", ")
					return nil
				}
			}
			field.value = value
		}
	case "mesh:disconnect_reason":
		if ui.node == nil || ui.node.Local {
			return nil
		}
		if strings.TrimSpace(value) == "" {
			ui.err = "Reason is required."
			return nil
		}
		key := local.ID()
		q, err := newMeshIntent("mesh_operator_prepare", key, meshPrepareRequest{Key: key, Kind: "disconnect", Disconnect: &meshops.DisconnectDraft{Host: ui.node.Host, Pair: ui.node.Trust.Pair, Reason: value}})
		if err != nil {
			ui.err = err.Error()
			return nil
		}
		ui.prompt = &meshPrompt{title: "PREPARE EXACT DISCONNECT", text: "Preparation reads this exact enrollment. Trust is revoked only after reviewing and confirming the concrete disconnect plan.\n\n" + meshDraftSummary(q.Params), request: q}
		m.viewport.GotoTop()
	case "mesh:handoff_reason":
		if ui.operation == nil {
			return nil
		}
		op := ui.operation
		q, err := newMeshIntent("mesh_operator_handoff", op.Key, meshRevisionRequest{Key: op.Key, Revision: op.Revision, Hash: op.RecoveryHash, Reason: value})
		if err != nil {
			ui.err = err.Error()
			return nil
		}
		ui.prompt = &meshPrompt{title: "REVIEW RECOVERY HANDOFF", text: safe(op.RecoveryEffect) + fmt.Sprintf("\n\nExact operation: %s\nRevision: %d\nRecovery SHA256: %s\nReason: %s\n\n[i] Complete collapsible receipt and exact request", safe(op.Key), op.Revision, safe(op.RecoveryHash), safe(value)), request: q}
		m.viewport.GotoTop()
	}
	return nil
}

func (m *dashboardModel) meshControls() []mouseControl {
	ui := m.meshUI
	if ui == nil {
		return []mouseControl{{"r", "Read Mesh"}}
	}
	if ui.busy {
		return []mouseControl{{"i", "Exact request"}}
	}
	if ui.prompt != nil {
		return []mouseControl{{"n", "Keep"}, {"y", "Confirm"}, {"i", "Record"}}
	}
	if ui.form != nil {
		return []mouseControl{{"enter", "Edit field"}, {"space", "Cycle"}, {"p", "Prepare"}, {"esc", "Back"}}
	}
	if ui.unknown != nil {
		if ui.unknown.Operation == "mesh_operator_resume" {
			return []mouseControl{{"l", "Read parent"}, {"o", "Attempts"}, {"i", "Request"}, {"esc", "Back"}}
		}
		return []mouseControl{{"l", "Lookup"}, {"t", "Retry exact"}, {"i", "Request"}, {"esc", "Back"}}
	}
	switch ui.mode {
	case "node":
		return []mouseControl{{"v", "Open node"}, {"d", "Disconnect"}, {"r", "Refresh"}, {"i", "Record"}, {"esc", "Nodes"}}
	case "operation":
		return []mouseControl{{"n", "Decline"}, {"a", "Approve"}, {"c", "Cancel"}, {"s", "Recover"}, {"r", "Refresh"}, {"i", "Record"}, {"esc", "Operations"}}
	case "operations":
		return []mouseControl{{"enter", "Inspect"}, {"o", "Nodes"}, {"r", "Refresh"}}
	default:
		return []mouseControl{{"enter", "Inspect"}, {"n", "Connect"}, {"o", "Operations"}, {"r", "Refresh"}}
	}
}
