package tui

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
)

type meshDashboard struct {
	kube                     *kubeInventoryUI
	uninstallCheckedKey      string
	uninstallCheckedRevision int64
	uninstallCheckedHash     string
	stopLeaseIndex           int
	stopLeaseSelection       uint64
	stopLease                *meshops.StopLeaseDetail
	mode                     string
	registry                 model.NodeRegistry
	operations               []meshops.Operation
	node                     *model.NodeEntry
	operation                *meshops.Operation
	selected                 int
	sequence                 uint64
	loading                  bool
	err                      string
	form                     *meshForm
	prompt                   *meshPrompt
	unknown                  *meshIntent
	intent                   *pendingRecord
	busy                     bool
	watching                 bool
	dirty                    bool
}

type meshReconnectContext struct {
	Host     string                     `json:"host"`
	Blockers []meshops.ReconnectBlocker `json:"blockers"`
}

type meshReadMsg struct {
	provisionSource    string
	uninstall          *meshops.UninstallContext
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
	if draft, ok := params.(meshPrepareRequest); ok && (draft.Kind == "stop" || draft.Kind == "uninstall") {
		q.LookupOnly = true
	}
	if operation == "mesh_operator_uninstall_finish" {
		q.LookupOnly = true
	}
	if operation == "mesh_operator_provision_prepare" {
		q.LookupOnly = true
	}
	if err == nil {
		err = q.validate()
	}
	return q, err
}

func (q meshIntent) validate() error {
	if q.LookupOnly && q.Operation != "mesh_operator_prepare" && q.Operation != "mesh_operator_provision_prepare" && q.Operation != "mesh_operator_decide" && q.Operation != "mesh_operator_cancel" && q.Operation != "mesh_operator_uninstall_finish" {
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
	case "mesh_operator_provision_prepare":
		var p meshProvisionPrepareRequest
		if e := strictPendingJSON(q.Params, &p); e != nil {
			return e
		}
		if !q.LookupOnly {
			return fmt.Errorf("installation preparation must retain lookup-only recovery")
		}
		if e := p.Provision.Validate(q.Key); e != nil {
			return e
		}
	case "mesh_operator_prepare":
		var p meshPrepareRequest
		if err := strictPendingJSON(q.Params, &p); err != nil {
			return err
		}
		count := 0
		for _, present := range []bool{p.Connect != nil, p.Reconnect != nil, p.Disconnect != nil, p.Stop != nil, p.Uninstall != nil} {
			if present {
				count++
			}
		}
		if count != 1 || (p.Kind == "connect" && p.Connect == nil) || (p.Kind == "reconnect" && p.Reconnect == nil) || (p.Kind == "disconnect" && p.Disconnect == nil) || (p.Kind == "stop" && (p.Stop == nil || !q.LookupOnly)) || (p.Kind == "uninstall" && (p.Uninstall == nil || !q.LookupOnly)) || (p.Kind != "connect" && p.Kind != "reconnect" && p.Kind != "disconnect" && p.Kind != "stop" && p.Kind != "uninstall") {
			return fmt.Errorf("exact connect, reconnect, disconnect or lookup-only Stop draft required")
		}
		if q.LookupOnly && p.Kind != "stop" && p.Kind != "uninstall" {
			return fmt.Errorf("lookup-only Stop envelope changed operation kind")
		}
		if p.Uninstall != nil && (!validMeshStopHost(p.Uninstall.Host) || !mesh.ValidKey(p.Uninstall.ProvisionKey)) {
			return fmt.Errorf("exact uninstall host and source receipt required")
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
	case "mesh_operator_uninstall_finish":
		var p meshUninstallFinishRequest
		if e := strictPendingJSON(q.Params, &p); e != nil {
			return e
		}
		hash, e := hex.DecodeString(p.Hash)
		if e != nil || len(hash) != 32 || hex.EncodeToString(hash) != p.Hash || p.Revision <= 0 || !q.LookupOnly {
			return fmt.Errorf("exact original uninstall finish revision/hash required")
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
	var expectedProvision *meshops.Operation
	var provisionLink *meshops.ProvisionPlan
	if mode == "provision-connection" {
		if ui.operation == nil || !meshProvisionConfirmed(*ui.operation) {
			return nil
		}
		p, ok := parseMeshProvisionReview(ui.operation.Review)
		if !ok || p.Request.ConnectionKey != id {
			return nil
		}
		provisionLink = &p
	}
	if ui.operation != nil && ui.operation.Kind == "provision" && ui.operation.Key == id {
		copy := *ui.operation
		copy.Review = append(json.RawMessage(nil), ui.operation.Review...)
		copy.Targets = append([]string(nil), ui.operation.Targets...)
		expectedProvision = &copy
	}
	if mode == "provision-lookup" {
		if expectedProvision == nil || !expectedProvision.Approved || !meshProvisionOperationBound(*expectedProvision) {
			ui.err = "Read the exact approved original installation before inspecting it."
			return nil
		}
		stopRevision = expectedProvision.Revision
	}
	expectedStopTarget := ""
	if ui.operation != nil && (ui.operation.Kind == "stop" || ui.operation.Kind == "uninstall") && ui.operation.Key == id && len(ui.operation.Targets) == 1 {
		expectedStopTarget = ui.operation.Targets[0]
	}
	if ui.unknown != nil && ui.unknown.LookupOnly && ui.unknown.Key == id && ui.unknown.Operation == "mesh_operator_prepare" {
		var draft meshPrepareRequest
		if json.Unmarshal(ui.unknown.Params, &draft) == nil {
			if draft.Stop != nil {
				expectedStopTarget = "host:" + draft.Stop.Host
			}
			if draft.Uninstall != nil {
				expectedStopTarget = "host:" + draft.Uninstall.Host
			}
		}
	}
	if mode == "uninstall-lookup" {
		if ui.operation == nil || ui.operation.Key != id || ui.operation.Kind != "uninstall" || !ui.operation.Approved || !meshUninstallOperationBound(*ui.operation) {
			ui.err = "Read the exact approved original uninstall before checking it."
			return nil
		}
		stopRevision = ui.operation.Revision
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
		case "uninstall-context":
			msg.node = &model.NodeEntry{}
			msg.err = session.Call(ctx, "node_inspect", map[string]string{"host": id}, msg.node)
			if msg.err == nil {
				allowed, why := meshUninstallAvailability(msg.node)
				if msg.node.Host != id || !allowed {
					msg.err = fmt.Errorf("%s", why)
				}
			}
			if msg.err == nil {
				msg.uninstall = &meshops.UninstallContext{}
				msg.err = session.Call(ctx, "mesh_operator_uninstall_context", map[string]string{"host": id}, msg.uninstall)
				if msg.err == nil && (msg.uninstall.Host != id || msg.uninstall.Candidates == nil) {
					msg.err = fmt.Errorf("exact managed-installation context unavailable")
				}
			}
		case "uninstall-lookup":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_uninstall_lookup", map[string]any{"key": id, "revision": stopRevision}, msg.operation)
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
		case "provision-lookup":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_provision_lookup", map[string]any{"key": id, "revision": stopRevision}, msg.operation)
		case "operation", "provision-connection":
			msg.operation = &meshops.Operation{}
			msg.err = session.Call(ctx, "mesh_operator_get", map[string]string{"key": id}, msg.operation)
		}
		if provisionLink != nil {
			msg.provisionSource = provisionLink.Key
			if msg.err == nil && (msg.operation == nil || msg.operation.Key != id || msg.operation.Kind != "connect" || len(msg.operation.Targets) != 1 || msg.operation.Targets[0] != "ssh:"+provisionLink.Request.Destination) {
				msg.err = fmt.Errorf("linked connection receipt differs from original installation")
			}
		}
		if msg.err == nil && msg.operation != nil && (msg.operation.Kind == "provision" || expectedProvision != nil) {
			if msg.operation.Key != id || !meshProvisionOperationBound(*msg.operation) || expectedProvision != nil && !meshProvisionSameReview(*expectedProvision, *msg.operation) {
				msg.err = fmt.Errorf("original installation lookup returned a different key, destination or committed review")
			}
		}
		if msg.err == nil && msg.operation != nil && (msg.operation.Kind == "stop" || msg.operation.Kind == "uninstall" || expectedStopTarget != "") {
			if msg.operation.Key != id || !((msg.operation.Kind == "stop" && meshStopOperationBound(*msg.operation)) || (msg.operation.Kind == "uninstall" && meshUninstallOperationBound(*msg.operation))) || (expectedStopTarget != "" && msg.operation.Targets[0] != expectedStopTarget) {
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
	if msg.mode == "provision-connection" && (ui.operation == nil || ui.operation.Kind != "provision" || ui.operation.Key != msg.provisionSource) {
		return nil
	}
	// Retire the original call only when its actual result arrives. Selection
	// uses its own epoch; it never abandons loading or the Service read owner.
	if msg.mode == "stop-lease" {
		current, err := meshStopLeaseRequest(ui)
		if err != nil || msg.stopLeaseRequest == nil || msg.stopLeaseSelection != ui.stopLeaseSelection || current != *msg.stopLeaseRequest {
			return nil
		}
	}
	if msg.mode == "uninstall-context" && (ui.form == nil || ui.form.uninstallHost == "" || msg.uninstall != nil && ui.form.uninstallHost != msg.uninstall.Host) {
		return nil
	}
	if msg.err != nil {
		ui.err = safe(msg.err.Error())
		return nil
	}
	ui.err = ""
	switch msg.mode {
	case "uninstall-context":
		f := ui.form
		previous := f.provisionKey
		f.provisions = append([]meshops.UninstallCandidate(nil), msg.uninstall.Candidates...)
		f.provisionKey = ""
		for _, p := range f.provisions {
			if p.ProvisionKey == previous {
				f.provisionKey = previous
			}
		}
		if !f.contextSeen && len(f.provisions) == 1 {
			f.provisionKey = f.provisions[0].ProvisionKey
		}
		f.contextSeen = true
		f.contextReady = true
		f.provenanceReason = msg.uninstall.Reason
		ui.node = msg.node
		m.viewport.GotoTop()
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
	case "operation", "stop-lookup", "uninstall-lookup", "provision-lookup", "provision-connection":
		changed := ui.operation == nil || msg.operation == nil || ui.operation.Key != msg.operation.Key
		if changed || ui.operation.ReviewRevision != msg.operation.ReviewRevision || ui.operation.ReviewHash != msg.operation.ReviewHash {
			ui.stopLeaseIndex = 0
			ui.stopLease = nil
		}
		ui.operation = msg.operation
		if msg.operation != nil && msg.mode == "uninstall-lookup" {
			ui.uninstallCheckedKey = msg.operation.Key
			ui.uninstallCheckedRevision = msg.operation.Revision
			ui.uninstallCheckedHash = msg.operation.ReviewHash
		} else if msg.operation != nil && (ui.uninstallCheckedKey != msg.operation.Key || ui.uninstallCheckedRevision != msg.operation.Revision || ui.uninstallCheckedHash != msg.operation.ReviewHash) {
			ui.uninstallCheckedKey = ""
		}
		if ui.unknown != nil && ui.unknown.LookupOnly && msg.operation != nil && (msg.operation.Kind == "stop" || msg.operation.Kind == "uninstall" || msg.operation.Kind == "provision") && msg.operation.Key == ui.unknown.Key {
			known := msg.operation.LookupOnly || msg.operation.CanPrepareFresh || ui.unknown.Operation == "mesh_operator_prepare"
			known = known || ui.unknown.Operation == "mesh_operator_provision_prepare"
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
	if !mesh.ValidKey(op.Key) || op.Revision <= 0 || (op.Kind != "connect" && op.Kind != "reconnect" && op.Kind != "disconnect" && op.Kind != "stop" && op.Kind != "uninstall" && op.Kind != "provision") || op.Phase == "" {
		return fmt.Errorf("invalid coordinator receipt; retain exact request/key and inspect its durable outcome")
	}
	if q.LookupOnly && op.Kind != "stop" && op.Kind != "uninstall" && op.Kind != "provision" {
		return fmt.Errorf("lookup-only Stop receipt changed operation kind")
	}
	if op.Kind == "provision" && !meshProvisionOperationBound(op) {
		return fmt.Errorf("installation receipt differs from original destination or review")
	}
	if q.Operation == "mesh_operator_provision_prepare" {
		var p meshProvisionPrepareRequest
		if strictPendingJSON(q.Params, &p) != nil || op.Kind != "provision" || p.Provision.Validate(q.Key) != nil {
			return fmt.Errorf("original installation preparation changed kind or key")
		}
		if p.Provision.Destination != "" && (len(op.Targets) != 1 || op.Targets[0] != "ssh:"+p.Provision.Destination) {
			return fmt.Errorf("installation preparation changed requested destination")
		}
		if len(op.Review) > 0 {
			review, _ := parseMeshProvisionReview(op.Review)
			if review.Request.ConnectionKey != p.Provision.ConnectionKey || review.Request.Transfer.Reason != p.Provision.Reason {
				return fmt.Errorf("installation preparation changed linked key or purpose")
			}
		}
	}
	if op.Kind == "uninstall" && !meshUninstallOperationBound(op) {
		return fmt.Errorf("uninstall receipt differs from exact host/review")
	}
	if q.Operation == "mesh_operator_uninstall_finish" {
		var p meshUninstallFinishRequest
		if strictPendingJSON(q.Params, &p) != nil || op.ReviewHash != p.Hash || op.Revision <= p.Revision {
			return fmt.Errorf("original uninstall finish response changed review")
		}
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
		if p.Uninstall != nil && (op.Kind != "uninstall" || len(op.Targets) != 1 || op.Targets[0] != "host:"+p.Uninstall.Host) {
			return fmt.Errorf("uninstall preparation changed selected host")
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
			ui.err = "Lifecycle outcome may be unknown: " + safe(msg.err.Error()) + ". Read this exact key; resend is disabled."
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
