package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/local"
	"getarm.org/internal/meshops"
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
	if ui.mode == "kubernetes" {
		return m.kubeKey(key)
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
			if len(ui.prompt.stopReview) > 0 {
				// The record and framed final dialog inspect the same captured
				// review, independently of live operation refreshes.
				record = struct {
					Review  json.RawMessage `json:"reviewed_stop_snapshot"`
					Request meshIntent      `json:"exact_request"`
				}{ui.prompt.stopReview, ui.prompt.request}
			} else if len(ui.prompt.uninstallReview) > 0 {
				record = struct {
					Review  json.RawMessage `json:"reviewed_uninstall_snapshot"`
					Request meshIntent      `json:"exact_request"`
				}{ui.prompt.uninstallReview, ui.prompt.request}
			} else if len(ui.prompt.provisionReview) > 0 {
				record = struct {
					Review  json.RawMessage `json:"reviewed_installation_snapshot"`
					Request meshIntent      `json:"exact_request"`
				}{ui.prompt.provisionReview, ui.prompt.request}
			} else if ui.operation != nil {
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
					if form.stopHost != "" && form.selected == 0 && field.value != "wait" {
						form.fields[1].value = ""
					}
					break
				}
			}
		case "enter":
			if form.provisionSourceKey != "" && form.selected == 0 {
				ui.err = "Original installed destination is fixed by the completed source receipt."
				return nil
			}
			if form.stopHost != "" && form.selected == 1 && form.fields[0].value != "wait" {
				ui.err = "Choose Wait for existing work before adding a reclaim timeout."
				return nil
			}
			m.inputMode = "mesh:field"
			m.input.SetValue(form.fields[form.selected].value)
			return m.input.Focus()
		case "[":
			if form.uninstallHost != "" {
				cycleMeshProvenance(form, -1)
				return nil
			}
			if len(form.blockers) > 0 {
				form.blockerIndex = min(len(form.blockers)-1, max(0, form.blockerIndex-1))
			}
		case "]":
			if form.uninstallHost != "" {
				cycleMeshProvenance(form, 1)
				return nil
			}
			if len(form.blockers) > 0 {
				form.blockerIndex = min(len(form.blockers)-1, max(0, form.blockerIndex+1))
			}
		case "l":
			if len(form.blockers) > 0 && (form.blockerIndex < 0 || form.blockerIndex >= len(form.blockers)) {
				ui.err = "The previously selected workflow is no longer in this list. Select an exact pending key with [ or ] before inspecting it."
				return nil
			}
			if len(form.blockers) > 0 {
				key := form.blockers[form.blockerIndex].Key
				cmd := m.readMesh("operation", key)
				if cmd == nil {
					ui.err = "Exact workflow inspection is waiting for the current read or connection. The editor and selected key are retained; press l after the read finishes."
					return nil
				}
				ui.form = nil
				return cmd
			}
		case "r":
			if form.uninstallHost != "" {
				form.contextReady = false
				return m.readMesh("uninstall-context", form.uninstallHost)
			}
			if form.reconnectHost != "" {
				form.contextReady = false
				return m.readMesh("reconnect", form.reconnectHost)
			}
		case "p":
			if form.reconnectHost != "" && (!form.contextReady || len(form.blockers) > 0) {
				ui.err = "Read the exact pending workflow and settle it or review its explicit handoff before preparing a fresh reconnect. [r] refreshes context; [l] inspects the selected blocker."
				return nil
			}
			q, err := form.request()
			if err != nil {
				ui.err = safe(err.Error())
				return nil
			}
			ui.prompt = &meshPrompt{title: "PREPARE CONNECTION · CONSEQUENCES", text: meshops.PreparationNotice + "\n\nEXACT DRAFT\n" + meshDraftSummary(q.Params), request: q}
			if form.stopHost != "" {
				ui.prompt = &meshPrompt{title: "PREPARE STOP REVIEW", text: "Requests a current plan for this exact ARM backend. No stop is approved before the separate concrete review.\n\n" + meshStopDraftSummary(q.Params), request: q}
			}
			if form.uninstallHost != "" {
				ui.prompt = &meshPrompt{title: "PREPARE UNINSTALL REVIEW", text: "Read a current plan for the selected source-managed installation. No Stop or removal is approved yet.\n\nExact host " + safe(form.uninstallHost) + "\nOriginal source receipt " + safe(form.provisionKey) + "\nThe database, identity, history, configuration and backups remain.", request: q}
			}
			if form.provisionInstall {
				var p meshProvisionPrepareRequest
				_ = json.Unmarshal(q.Params, &p)
				ui.prompt = &meshPrompt{title: "PREPARE INSTALLATION REVIEW", text: "Perform a read-only verified SSH account/platform probe and select trusted ARM payloads. No upload, startup or mesh trust is approved yet.\n\nDestination " + safe(p.Provision.Destination) + "\nReviewed connector " + safe(p.Provision.Connector) + "\nWhy " + safe(p.Provision.Reason), request: q}
			}
			m.viewport.GotoTop()
		}
		return nil
	}
	// These actions are reads only, including for an unresolved Stop outcome.
	if ui.mode == "operation" && ui.operation != nil && ui.operation.Kind == "stop" {
		switch key {
		case "[", "]":
			before := ui.stopLeaseIndex
			count := len(meshStopLeases(ui.operation))
			if count > 0 {
				if key == "[" {
					ui.stopLeaseIndex = max(0, ui.stopLeaseIndex-1)
				} else {
					ui.stopLeaseIndex = min(count-1, ui.stopLeaseIndex+1)
				}
			}
			if ui.stopLeaseIndex != before {
				ui.stopLeaseSelection++
				ui.err = ""
			}
			ui.stopLease = nil
			m.viewport.GotoTop()
			return nil
		case "d":
			return m.readMesh("stop-lease", ui.operation.Key)
		case "esc":
			if ui.stopLease != nil {
				ui.stopLease = nil
				m.viewport.GotoTop()
				return nil
			}
		}
	}
	if ui.unknown != nil {
		switch key {
		case "l":
			if ui.unknown.Operation == "mesh_operator_uninstall_finish" && ui.operation != nil && ui.operation.Kind == "uninstall" && ui.operation.Approved {
				return m.readMesh("uninstall-lookup", ui.unknown.Key)
			}
			return m.readMesh("operation", ui.unknown.Key)
		case "t":
			q := *ui.unknown
			if q.LookupOnly {
				ui.err = "Stop outcome is uncertain. Read this exact receipt; no request, decision, resume or handoff is resent."
				return nil
			}
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
		if (strings.Contains("anceshdgu", key) || (key == "f" && ui.operation != nil && ui.operation.Kind == "uninstall")) && len(key) == 1 {
			ui.err = "Resolve the exact retained uncertain request before preparing another Mesh mutation."
			return nil
		}
	}
	switch key {
	case "K":
		ui.mode = "kubernetes"
		ui.kube = &kubeInventoryUI{}
		return m.readKubeContexts(ui.kube)
	case "esc":
		if ui.mode == "node" {
			ui.mode = "nodes"
		} else if ui.mode == "operation" {
			ui.mode = "operations"
		} else {
			ui.mode = "nodes"
		}
		ui.nodeSelection++
		ui.node = nil
		ui.availabilityReport, ui.availabilityInspection = nil, nil
		ui.operation = nil
		ui.selected = 0
		m.viewport.GotoTop()
	case "o":
		if ui.mode == "operations" {
			ui.mode = "nodes"
		} else {
			ui.mode = "operations"
		}
		ui.nodeSelection++
		ui.node = nil
		ui.availabilityReport, ui.availabilityInspection = nil, nil
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
	case "e":
		if ui.node == nil || ui.node.Local || ui.node.Trust.State != "revoked" {
			ui.err = "Select an exact revoked direct peer to reconnect."
			return nil
		}
		ui.form = newMeshForm()
		ui.form.reconnectHost = ui.node.Host
		ui.form.fields = ui.form.fields[:18]
		ui.form.fields[0].label = "Replacement SSH locator or @connector (blank: retained)"
		ui.err = ""
		m.viewport.GotoTop()
		return m.readMesh("reconnect", ui.node.Host)
	case "d":
		if ui.node == nil || ui.node.Local {
			ui.err = "Select an exact direct peer to disconnect."
			return nil
		}
		m.inputMode = "mesh:disconnect_reason"
		m.input.SetValue("")
		return m.input.Focus()
	case "u":
		if allowed, why := meshUninstallAvailability(ui.node); !allowed {
			ui.err = why
			return nil
		}
		ui.form = newMeshStopForm(ui.node.Host)
		ui.form.uninstallHost = ui.node.Host
		ui.form.contextReady = false
		ui.err = ""
		m.viewport.GotoTop()
		return m.readMesh("uninstall-context", ui.node.Host)
	case "v":
		return m.openSelectedNode()
	case "a":
		return m.meshDecision(true)
	case "l":
		if ui.operation != nil && ui.operation.Kind == "provision" && ui.operation.Approved {
			return m.readMesh("provision-lookup", ui.operation.Key)
		}
		if ui.operation != nil && ui.operation.Kind == "uninstall" && ui.operation.Approved {
			return m.readMesh("uninstall-lookup", ui.operation.Key)
		}
		if ui.operation != nil && ui.operation.Kind == "stop" && (ui.operation.LookupOnly || ui.operation.Approved) {
			return m.readMesh("stop-lookup", ui.operation.Key)
		}
	case "g":
		if ui.operation != nil && ui.operation.Kind == "provision" {
			return m.meshConnectProvisioned()
		}
		if ui.operation != nil && ui.operation.Kind == "stop" && ui.operation.CanPrepareFresh && !ui.operation.LookupOnly && !ui.operation.Approved {
			var review meshStopReview
			if json.Unmarshal(ui.operation.Review, &review) == nil && validMeshStopHost(review.Plan.Target.Host) {
				return m.readMesh("stop", review.Plan.Target.Host)
			}
		}
	case "c", "s", "h":
		if key == "s" && ui.mode == "node" && ui.node != nil && !ui.node.Local {
			return m.readMesh("stop", ui.node.Host)
		}
		if ui.operation == nil {
			return nil
		}
		op := ui.operation
		if op.Kind == "provision" {
			ui.err = "Installation has no resend, resume or handoff. Inspect the original key; a connection review is separate."
			return nil
		}
		if op.Kind == "uninstall" {
			ui.err = "Uninstall has no resend, resume or handoff. Read the original key; finish is separately reviewed."
			return nil
		}
		if op.Kind == "stop" && (key != "c" || op.LookupOnly || op.Approved) {
			ui.err = "Stop recovery is lookup-only; no resume or handoff is available."
			return nil
		}
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
		if op.Kind == "stop" {
			q.LookupOnly = true
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
		if ui.mode == "nodes" {
			if ui.unknown != nil {
				ui.err = "Inspect the retained original request before preparing another installation."
				return nil
			}
			ui.form = newMeshProvisionForm()
			ui.err = ""
			m.viewport.GotoTop()
			return nil
		}
		if ui.operation != nil && ui.operation.ParentKey != "" {
			return m.readMesh("operation", ui.operation.ParentKey)
		}
	case "f":
		if ui.operation != nil && ui.operation.Kind == "uninstall" {
			return m.meshFinishUninstall()
		}
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
	if !meshCanReview(op, now) && !(!approve && (meshStopCanDecline(op, now) || meshUninstallCanDecline(op, now))) {
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
	if op.Kind == "stop" || op.Kind == "uninstall" || op.Kind == "provision" {
		q.LookupOnly = true
	}
	action := "DECLINE"
	if approve {
		action = "APPROVE"
	}
	ui.prompt = &meshPrompt{title: "REVIEW " + action + " · EXACT ENROLLMENT", text: fmt.Sprintf("%s this immutable review only. Revision %d\nHash: %s\nExpires: %s\n\n%s\n\n%s", action, op.ReviewRevision, safe(op.ReviewHash), op.ReviewExpires.Format(time.RFC3339), summary, safe(op.Notice)), request: q}
	if op.Kind == "stop" {
		title := "KEEP ARM RUNNING"
		if approve {
			title = "STOP ARM · FINAL REVIEW"
		}
		ui.prompt = &meshPrompt{title: title, text: fmt.Sprintf("Exact review revision %d · expires %s\n[i] Complete immutable review. Default: keep running.", op.ReviewRevision, op.ReviewExpires.Format(time.RFC3339)), request: q, stopReview: append(json.RawMessage(nil), op.Review...)}
	}
	if op.Kind == "uninstall" {
		title := "KEEP INSTALLATION UNCHANGED"
		if approve {
			title = "UNINSTALL ARM · FINAL REVIEW"
		}
		ui.prompt = &meshPrompt{title: title, text: fmt.Sprintf("Exact review revision %d · expires %s\nDefault: keep this installation unchanged.", op.ReviewRevision, op.ReviewExpires.Format(time.RFC3339)), request: q, uninstallReview: append(json.RawMessage(nil), op.Review...)}
	}
	if op.Kind == "provision" {
		title := "KEEP HOST UNCHANGED"
		if approve {
			title = "INSTALL ARM · FINAL REVIEW"
		}
		ui.prompt = &meshPrompt{title: title, text: fmt.Sprintf("Exact review revision %d · expires %s\nDefault: keep this host unchanged. Installation grants no mesh trust.", op.ReviewRevision, op.ReviewExpires.Format(time.RFC3339)), request: q, provisionReview: append(json.RawMessage(nil), op.Review...)}
	}
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
		if ui.prompt.request.LookupOnly {
			yes := "Confirm"
			if ui.prompt.request.Operation == "mesh_operator_prepare" {
				yes = "Prepare review"
			}
			if ui.prompt.request.Operation == "mesh_operator_uninstall_finish" {
				yes = "Finish original uninstall"
			}
			if ui.prompt.request.Operation == "mesh_operator_decide" {
				var q meshops.Decision
				if json.Unmarshal(ui.prompt.request.Params, &q) == nil {
					yes = "Keep running"
					if q.Approve {
						yes = "Stop ARM"
					}
				}
			}
			if len(ui.prompt.uninstallReview) > 0 && ui.prompt.request.Operation == "mesh_operator_decide" {
				var d meshops.Decision
				if json.Unmarshal(ui.prompt.request.Params, &d) == nil {
					yes = "Keep installation"
					if d.Approve {
						yes = "Uninstall ARM"
					}
				}
			}
			if ui.prompt.request.Operation == "mesh_operator_provision_prepare" {
				yes = "Prepare installation review"
			}
			if len(ui.prompt.provisionReview) > 0 && ui.prompt.request.Operation == "mesh_operator_decide" {
				var d meshops.Decision
				if json.Unmarshal(ui.prompt.request.Params, &d) == nil {
					yes = "Keep host unchanged"
					if d.Approve {
						yes = "Install ARM"
					}
				}
			}
			return []mouseControl{{"n", "Keep unchanged"}, {"y", yes}, {"i", "Record"}}
		}
		return []mouseControl{{"n", "Keep"}, {"y", "Confirm"}, {"i", "Record"}}
	}
	if ui.form != nil {
		if ui.form.uninstallHost != "" {
			controls := []mouseControl{{"[", "Previous receipt"}, {"]", "Next receipt"}, {"r", "Refresh provenance"}, {"enter", "Edit field"}, {"space", "Policy"}, {"esc", "Keep installed"}}
			if ui.form.contextReady && ui.form.provisionKey != "" {
				controls = append([]mouseControl{{"p", "Prepare review"}}, controls...)
			}
			return controls
		}
		return []mouseControl{{"enter", "Edit field"}, {"space", "Cycle"}, {"p", "Prepare"}, {"esc", "Back"}}
	}
	if ui.unknown != nil && ui.operation != nil && ui.operation.Kind == "stop" && ui.mode == "operation" {
		return []mouseControl{{"[", "Previous lease"}, {"]", "Next lease"}, {"d", "Lease details"}, {"l", "Read receipt"}, {"i", "Request"}, {"esc", "Back"}}
	}
	if ui.unknown != nil {
		if ui.unknown.LookupOnly {
			return []mouseControl{{"l", "Read receipt"}, {"o", "Operations"}, {"i", "Request"}, {"esc", "Back"}}
		}
		if ui.unknown.Operation == "mesh_operator_resume" {
			return []mouseControl{{"l", "Read parent"}, {"o", "Attempts"}, {"i", "Request"}, {"esc", "Back"}}
		}
		return []mouseControl{{"l", "Lookup"}, {"t", "Retry exact"}, {"i", "Request"}, {"esc", "Back"}}
	}
	switch ui.mode {
	case "kubernetes":
		return []mouseControl{{"enter", "Select Node"}, {"[", "Previous context"}, {"]", "Next context"}, {"ctrl+r", "Refresh"}, {"esc", "Mesh"}}
	case "node":
		controls := []mouseControl{{"v", "Open node"}, {"d", "Disconnect"}, {"r", "Refresh"}, {"i", "Record"}, {"esc", "Nodes"}}
		if allowed, _ := meshStopAvailability(ui.node); allowed {
			controls = append([]mouseControl{{"s", "Stop ARM"}}, controls...)
		}
		if allowed, _ := meshUninstallAvailability(ui.node); allowed {
			controls = append([]mouseControl{{"u", "Uninstall ARM"}}, controls...)
		}
		return controls

	case "operation":
		if ui.operation != nil && ui.operation.Kind == "provision" {
			controls := []mouseControl{{"r", "Read receipt"}, {"i", "Record"}, {"esc", "Back"}}
			if meshCanReview(*ui.operation, time.Now()) {
				controls = append([]mouseControl{{"n", "Keep host unchanged"}, {"a", "Review installation"}}, controls...)
			}
			if ui.operation.Approved {
				controls = append([]mouseControl{{"l", "Inspect original installation"}}, controls...)
			}
			if meshProvisionConfirmed(*ui.operation) {
				controls = append([]mouseControl{{"g", "Review mesh connection"}}, controls...)
			}
			return controls
		}
		if ui.operation != nil && ui.operation.Kind == "uninstall" {
			controls := []mouseControl{{"r", "Read receipt"}, {"i", "Record"}, {"esc", "Back"}}
			if meshUninstallCanDecline(*ui.operation, time.Now()) {
				controls = append([]mouseControl{{"n", "Keep installation"}}, controls...)
			}
			if meshCanReview(*ui.operation, time.Now()) {
				controls = append([]mouseControl{{"a", "Review uninstall"}}, controls...)
			}
			if ui.operation.Approved {
				controls = append([]mouseControl{{"l", "Check original uninstall"}}, controls...)
				if meshUninstallFinishReady(ui) {
					controls = append([]mouseControl{{"f", "Review finish"}}, controls...)
				}
			}
			return controls
		}
		if ui.operation != nil && ui.operation.Kind == "stop" {
			controls := []mouseControl{{"[", "Previous lease"}, {"]", "Next lease"}, {"d", "Lease details"}, {"r", "Lookup"}, {"i", "Record"}, {"esc", "Back"}}
			if meshStopCanDecline(*ui.operation, time.Now()) {
				controls = append([]mouseControl{{"n", "Keep running"}}, controls...)
			}
			if meshCanReview(*ui.operation, time.Now()) {
				controls = append([]mouseControl{{"a", "Review stop"}}, controls...)
			}
			if ui.operation.WorkerLive && !ui.operation.Approved && !ui.operation.LookupOnly {
				controls = append([]mouseControl{{"c", "Cancel review"}}, controls...)
			}
			if ui.operation.LookupOnly || ui.operation.Approved {
				controls = append([]mouseControl{{"l", "Check original stop"}}, controls...)
			}
			if ui.operation.CanPrepareFresh && !ui.operation.LookupOnly && !ui.operation.Approved {
				controls = append([]mouseControl{{"g", "Fresh review"}}, controls...)
			}
			return controls
		}
		return []mouseControl{{"n", "Decline"}, {"a", "Approve"}, {"c", "Cancel"}, {"s", "Recover"}, {"r", "Refresh"}, {"i", "Record"}, {"esc", "Operations"}}
	case "operations":
		return []mouseControl{{"enter", "Inspect"}, {"o", "Nodes"}, {"r", "Refresh"}}
	default:
		return []mouseControl{{"enter", "Inspect"}, {"K", "Kubernetes Nodes"}, {"p", "Install ARM"}, {"n", "Connect"}, {"o", "Operations"}, {"r", "Refresh"}}
	}
}
