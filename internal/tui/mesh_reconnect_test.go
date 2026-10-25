package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/meshops"
)

func TestMeshReconnectDraftExactHostFreshDefaultsAndClosedWire(t *testing.T) {
	form := newMeshForm()
	form.reconnectHost = strings.Repeat("a", 32)
	form.fields = form.fields[:18]
	form.fields[1].value = "new explicitly limited enrollment"
	q, e := form.request()
	if e != nil {
		t.Fatal(e)
	}
	var value meshPrepareRequest
	if e = json.Unmarshal(q.Params, &value); e != nil {
		t.Fatal(e)
	}
	if value.Kind != "reconnect" || value.Reconnect == nil || value.Reconnect.Host != form.reconnectHost || value.Connect != nil || value.Disconnect != nil {
		t.Fatal(value)
	}
	if strings.Join(value.Reconnect.Forward.Permissions, ",") != "observe-host" || len(value.Reconnect.Reverse.Permissions) != 0 || value.Reconnect.Destination != "" {
		t.Fatal("fresh defaults inherited old privileges", value.Reconnect)
	}
	for _, field := range []string{"remote_binary", "remote_home", "binding"} {
		if strings.Contains(string(q.Params), field) {
			t.Fatal("native authority field exposed in terminal draft", field)
		}
	}
	if e = validateMeshOutcome(q, meshops.Operation{Key: q.Key, Kind: "connect", Revision: 1, Phase: "preparing"}); e == nil {
		t.Fatal("different outcome kind settled reconnect")
	}
	if e = validateMeshOutcome(q, meshops.Operation{Key: q.Key, Kind: "reconnect", Revision: 1, Phase: "preparing"}); e != nil {
		t.Fatal(e)
	}
	if _, e = newMeshIntent("mesh_operator_prepare", "bad", meshPrepareRequest{Key: "bad", Kind: "reconnect", Reconnect: &meshReconnectDraft{Host: "name"}}); e == nil {
		t.Fatal("nonstable reconnect host retained")
	}
}
func TestMeshReconnectKeyOnlyForRevokedNode(t *testing.T) {
	m := meshVisualDashboard(t, "mesh-node-detail")
	m.meshUI.node.Trust.State = "revoked"
	host := m.meshUI.node.Host
	m.meshKey("e")
	if m.meshUI.form == nil || m.meshUI.form.reconnectHost != host || len(m.meshUI.form.fields) != 18 {
		t.Fatal("exact reconnect form unavailable")
	}
	m = meshVisualDashboard(t, "mesh-node-detail")
	m.meshUI.node.Trust.State = "active"
	m.meshKey("e")
	if m.meshUI.form != nil || !strings.Contains(m.meshUI.err, "revoked") {
		t.Fatal("active node implicitly replaced")
	}
}

func TestMeshReconnectPendingContextBlocksAndOpensExactRecovery(t *testing.T) {
	m := meshVisualDashboard(t, "mesh-node-detail")
	transport := newTestTransport()
	m.session = transport
	m.connected = true
	transport.reply = func(operation string, params, result any) error {
		if operation == "mesh_operator_get" {
			*result.(*meshops.Operation) = meshops.Operation{Key: params.(map[string]string)["key"], Kind: "disconnect", Phase: "incomplete", Revision: 2}
		}
		return nil
	}
	m.meshUI.node.Trust.State = "revoked"
	m.meshKey("e")
	ui := m.meshUI
	m.meshKey("p")
	if ui.prompt != nil {
		t.Fatal("unread context permitted prepare")
	}
	ctx := &meshReconnectContext{Host: ui.node.Host, Blockers: []meshops.ReconnectBlocker{{Key: "retained-disconnect", Kind: "disconnect", Phase: "incomplete"}}}
	m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "reconnect", reconnect: ctx})
	m.meshKey("p")
	if ui.prompt != nil || !strings.Contains(m.meshText(), "retained-disconnect") {
		t.Fatal("pending obligation hidden or prepared")
	}
	// A stale read for another host cannot unblock this form.
	m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "reconnect", reconnect: &meshReconnectContext{Host: strings.Repeat("f", 32), Blockers: []meshops.ReconnectBlocker{}}})
	if len(ui.form.blockers) != 1 {
		t.Fatal("stale context replaced exact blocker")
	}
	cmd := m.meshKey("l")
	if cmd == nil {
		t.Fatal("exact operation read unavailable")
	}
	msg, ok := cmd().(meshReadMsg)
	if !ok || msg.err != nil || msg.operation == nil || msg.operation.Key != "retained-disconnect" {
		t.Fatal("inspection did not read exact blocker", msg)
	}
	if len(transport.operations) != 1 || transport.operations[0] != "mesh_operator_get" {
		t.Fatal("inspection invoked mutation", transport.operations)
	}
	if ui.form != nil {
		t.Fatal("pending recovery remained trapped in editor")
	}
	// The existing operation reader owns review/resume/handoff; no mutation was queued.
	if ui.prompt != nil || ui.unknown != nil {
		t.Fatal("inspection created mutation")
	}
}

func TestMeshReconnectRefreshedBlockersKeepExactSelection(t *testing.T) {
	m := meshVisualDashboard(t, "mesh-node-detail")
	transport := newTestTransport()
	m.session = transport
	m.connected = true
	transport.reply = func(op string, params, result any) error {
		if op == "mesh_operator_get" {
			*result.(*meshops.Operation) = meshops.Operation{Key: params.(map[string]string)["key"], Kind: "disconnect", Phase: "incomplete", Revision: 2}
		}
		return nil
	}
	m.meshUI.node.Trust.State = "revoked"
	m.meshKey("e")
	ui := m.meshUI
	receive := func(keys ...string) {
		blockers := []meshops.ReconnectBlocker{}
		for _, key := range keys {
			blockers = append(blockers, meshops.ReconnectBlocker{Key: key, Kind: "disconnect", Phase: "incomplete"})
		}
		m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "reconnect", reconnect: &meshReconnectContext{Host: ui.node.Host, Blockers: blockers}})
	}
	receive("first", "second")
	m.meshKey("]")
	if ui.form.blockerIndex != 1 {
		t.Fatal("fixture did not select second")
	}
	receive("second", "first")
	if ui.form.blockerIndex != 0 || ui.form.blockers[ui.form.blockerIndex].Key != "second" {
		t.Fatal("reorder retargeted selection")
	}
	receive("remaining")
	if ui.form.blockerIndex != -1 {
		t.Fatal("removed selection silently retargeted")
	}
	if cmd := m.meshKey("l"); cmd != nil || ui.form == nil || !strings.Contains(ui.err, "Select an exact") {
		t.Fatal("removed key opened unrelated operation")
	}
	// Empty then repopulated rosters do not revive a stale positional selection.
	receive()
	receive("new-first", "new-second")
	if ui.form.blockerIndex != -1 {
		t.Fatal("repopulation silently selected a different key")
	}
	// A malformed/stale index is independently guarded before any slice access.
	ui.form.blockerIndex = 99
	if cmd := m.meshKey("l"); cmd != nil || ui.form == nil {
		t.Fatal("invalid index inspected a different workflow")
	}
	ui.form.blockerIndex = -1
	m.meshKey("]")
	if ui.form.blockerIndex != 0 {
		t.Fatal("explicit selection unavailable")
	}
	cmd := m.meshKey("l")
	if cmd == nil {
		t.Fatal("explicit exact read unavailable")
	}
	msg := cmd().(meshReadMsg)
	if msg.err != nil || msg.operation == nil || msg.operation.Key != "new-first" {
		t.Fatal("explicit selection did not read exact key", msg)
	}
	if len(transport.operations) != 1 {
		t.Fatal("refresh manufactured native operations", transport.operations)
	}
}

func TestMeshReconnectInspectionWaitsWithoutDroppingEditor(t *testing.T) {
	m := meshVisualDashboard(t, "mesh-node-detail")
	m.session = newTestTransport()
	m.connected = true
	m.meshUI.node.Trust.State = "revoked"
	m.meshKey("e")
	ui := m.meshUI
	receive := func() {
		m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "reconnect", reconnect: &meshReconnectContext{Host: ui.node.Host, Blockers: []meshops.ReconnectBlocker{{Key: "exact-blocker", Kind: "disconnect", Phase: "incomplete"}}}})
	}
	receive()
	form := ui.form
	if m.meshKey("r") == nil || !ui.loading {
		t.Fatal("fixture context refresh did not start")
	}
	if cmd := m.meshKey("l"); cmd != nil || ui.form != form || ui.form.blockers[ui.form.blockerIndex].Key != "exact-blocker" || !strings.Contains(ui.err, "retained") {
		t.Fatal("pending refresh dropped editor/selection")
	}
	receive()
	m.connected = false
	if cmd := m.meshKey("l"); cmd != nil || ui.form != form {
		t.Fatal("disconnected inspection dropped editor")
	}
	m.connected = true
	if cmd := m.meshKey("l"); cmd == nil || ui.form != nil {
		t.Fatal("completed refresh did not allow explicit exact inspection")
	}
}
