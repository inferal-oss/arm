package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"getarm.org/internal/meshops"
)

// Handoff's recovery fingerprint differs from the original trust review hash.
// The existing terminal prompt must show and submit the exact current snapshot.
func TestMeshHandoffReviewDisplaysExactRevisionAndRecoveryFingerprint(t *testing.T) {
	m := meshVisualDashboard(t, "mesh-node-detail")
	transport := newTestTransport()
	m.session = transport
	m.connected = true
	ui := m.meshUI
	ui.mode = "operation"
	ui.operation = &meshops.Operation{Key: "lost-disconnect", Kind: "disconnect", Phase: "incomplete", Revision: 8,
		ReviewHash: strings.Repeat("a", 64), RecoveryHash: strings.Repeat("b", 64), RecoveryEffect: "Workflow exclusion only; old obligations remain."}
	m.meshKey("h") // The returned cursor-focus command is not a network mutation.
	if m.inputMode != "mesh:handoff_reason" || ui.prompt != nil || len(transport.operations) != 0 {
		t.Fatal("handoff sent before entering a reason")
	}
	m.input.SetValue("Preserve exact obligations for manual recovery")
	if command := m.acceptMeshInput(); command != nil || ui.prompt == nil {
		t.Fatal("handoff failed to prepare a local review")
	}
	for _, value := range []string{"Exact operation: lost-disconnect", "Revision: 8", "Recovery SHA256: " + strings.Repeat("b", 64), "old obligations remain"} {
		if !strings.Contains(ui.prompt.text, value) {
			t.Fatalf("review omitted %q", value)
		}
	}
	if strings.Contains(ui.prompt.text, strings.Repeat("a", 64)) {
		t.Fatal("handoff displayed original trust hash instead of recovery fingerprint")
	}
	var request meshRevisionRequest
	if err := json.Unmarshal(ui.prompt.request.Params, &request); err != nil {
		t.Fatal(err)
	}
	if request.Key != "lost-disconnect" || request.Revision != 8 || request.Hash != strings.Repeat("b", 64) || request.Reason != m.input.Value() {
		t.Fatal("review differs from exact guarded request", request)
	}
	if command := m.meshKey("n"); command != nil || ui.prompt != nil {
		t.Fatal("default no retained or sent the handoff")
	}
	if len(transport.operations) != 0 || ui.operation.Revision != 8 || ui.operation.Phase != "incomplete" {
		t.Fatal("declining local review changed workflow or issued a call")
	}
}
