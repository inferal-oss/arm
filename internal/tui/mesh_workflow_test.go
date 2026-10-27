package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestMeshLateReadCannotPopulateReopenedSurface(t *testing.T) {
	m, s := fixtureDashboard(t)
	old := &meshDashboard{sequence: 1, loading: true}
	m.meshUI = &meshDashboard{sequence: 1, loading: true}
	m.acceptMeshRead(meshReadMsg{ui: old, session: s, sequence: 1, mode: "node", node: &model.NodeEntry{Host: "wrong"}})
	if m.meshUI.node != nil || !m.meshUI.loading {
		t.Fatal("old UI generation populated replacement")
	}
}

func TestMeshDeclinePromptSendsNothingAndUnknownPersistsExact(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	review := json.RawMessage(`{"operation":"connect-direct-peers","local_prepared":{"identity":{"host":"source","certificate_sha256":"pin-source"}},"remote_prepared":{"identity":{"host":"target","certificate_sha256":"pin-target"}}}`)
	m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: local.ID(), Phase: "awaiting_review", WorkerLive: true, Review: review, ReviewRevision: 1, ReviewHash: strings.Repeat("a", 64), ReviewExpires: m.timeNow.Add(time.Minute)}}
	m.meshDecision(true)
	if m.meshUI.prompt == nil {
		t.Fatal("concrete review prompt missing")
	}
	press(m, "n")
	if len(s.operations) != 0 || m.meshUI.prompt != nil {
		t.Fatal("decline performed network operation")
	}
	key := local.ID()
	q, err := newMeshIntent("mesh_operator_cancel", key, meshRevisionRequest{Key: key, Revision: 3})
	if err != nil {
		t.Fatal(err)
	}
	s.reply = func(string, any, any) error { return errors.New("response lost") }
	cmd := m.sendMesh(q, false)
	if cmd == nil {
		t.Fatal("send unavailable")
	}
	m.Update(cmd())
	if m.meshUI.unknown == nil || m.meshUI.unknown.Key != key {
		t.Fatal("unknown request lost key")
	}
	records, err := loadPending(m.home)
	if err != nil || len(records) != 1 || records[0].Kind != "mesh" || records[0].key() != key {
		t.Fatal(records, err)
	}
	restored := newDashboard(t.Context(), m.home)
	if len(restored.pendingRecords) != 1 {
		t.Fatal("new process lost exact coordinator request")
	}
	restored.session = s
	restored.connected = true
	restored.data.status.Host = "another-authority"
	restored.openRecovery()
	restored.recoveryKey("enter")
	if restored.meshUI == nil || restored.meshUI.intent == nil {
		t.Fatal("Mesh recovery not routed")
	}
	before := len(s.operations)
	if retry := restored.sendMesh(*restored.meshUI.unknown, true); retry != nil || len(s.operations) != before {
		t.Fatal("restored request submitted to another authority")
	}
}

func TestMeshPrimaryReviewShowsHumanTrustFactsNotRawJSON(t *testing.T) {
	review := json.RawMessage(`{"operation":"connect-direct-peers","ssh_identity":{"account":"build-user","host_key":"SHA256:ssh-pin"},"request":{"ssh_destination":"fixture.example","reason":"coordination fixture","source_permissions_on_target":{"permissions":["observe-host"]},"target_permissions_on_source":{"permissions":[]}},"local_prepared":{"identity":{"name":"source-name","host":"source-id","owner_uid":"42","owner_username":"operator","address":"127.0.0.1:10001","certificate_sha256":"source-pin","public_key_sha256":"source-key"},"listen":"127.0.0.1:10001"},"remote_prepared":{"identity":{"name":"target-name","host":"target-id","owner_uid":"43","owner_username":"worker","address":"127.0.0.1:10002","certificate_sha256":"target-pin","public_key_sha256":"target-key"},"listen":"127.0.0.1:10002"},"warning":"No new trust until approval"}`)
	text, ok := meshReviewSummary(review)
	if !ok {
		t.Fatal("current known review not supported")
	}
	for _, want := range []string{"source-name", "target-name", "operator (UID 42)", "worker (UID 43)", "source-pin", "target-pin", "source-key", "target-key", "build-user", "SHA256:ssh-pin", "WE MAY DO THERE", "PEER MAY DO HERE", "Host summary", "No access", "Default: keep unchanged"} {
		if !strings.Contains(text, want) {
			t.Fatal("concrete trust fact hidden", want, text)
		}
	}
	if strings.Contains(text, `"local_prepared"`) || strings.Contains(text, `{"`) {
		t.Fatal("raw JSON remained primary review", text)
	}
	if _, ok := meshReviewSummary(json.RawMessage(`{"operation":"future-review"}`)); ok {
		t.Fatal("unsupported review implicitly approved")
	}
}

func TestRemoteWorkspaceNeverRendersOrRoutesLocalState(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 140
	m.height = 35
	m.view = 2
	m.data.status.Leases = []model.Lease{activeLease("LOCAL-LEASE-SECRET")}
	m.events = []model.Event{{Type: "LOCAL-EVENT-SECRET", Entity: "LOCAL-LEASE-SECRET"}}
	m.nodeUI = &nodeDashboard{selection: model.NodeSelection{Host: strings.Repeat("a", 32), Source: strings.Repeat("b", 32)}, summary: nodeSummary{Host: strings.Repeat("a", 32), Leases: []model.Lease{}}}
	m.syncViewport()
	text := ansi.Strip(m.View().Content)
	for _, secret := range []string{"LOCAL-LEASE-SECRET", "LOCAL-EVENT-SECRET"} {
		if strings.Contains(text, secret) {
			t.Fatal("remote view leaked local state", secret)
		}
	}
	for _, key := range []string{"e", "x", "c", "n", "b", "y", "p", "enter", "r"} {
		if cmd := press(m, key); cmd != nil {
			t.Fatal("unsupported remote action produced local command", key)
		}
	}
	if len(s.operations) != 0 {
		t.Fatal("remote action called local control transport", s.operations)
	}
	for _, region := range m.mouseRegions() {
		if region.row >= 0 && region.key == "" {
			t.Fatal("local lease/feed row was interactive in remote context", region)
		}
	}
}

func TestMeshFormExactCountsAndSafeDirectionalDefaults(t *testing.T) {
	f := newMeshForm()
	f.fields[0].value = "example.test"
	f.fields[1].value = "review fixture"
	q, err := f.request()
	if err != nil {
		t.Fatal(err)
	}
	var p meshPrepareRequest
	json.Unmarshal(q.Params, &p)
	if len(p.Connect.Forward.Permissions) != 1 || p.Connect.Forward.Permissions[0] != "observe-host" || len(p.Connect.Reverse.Permissions) != 0 {
		t.Fatal("default permissions widened", p.Connect)
	}
	for _, value := range []string{"1e999999999", "1/2", "NaN", "-1", "9223372036854775807"} {
		if _, err := meshGiB(value); err == nil {
			t.Fatal("unsafe/out-of-range quantity accepted", value)
		}
	}
	if n, err := meshGiB("0.5"); err != nil || n != 1<<29 {
		t.Fatal("decimal byte conversion rounded", n, err)
	}
	f.fields[2].value = "unknown-mode"
	if _, err := f.request(); err == nil {
		t.Fatal("unknown mode acquired implicit rights")
	}
	var summary nodeSummary
	if err := json.Unmarshal([]byte(`{"host":"a","resources":[{"budget_total":9223372036854775807,"reserved":0,"available":9223372036854775807}]}`), &summary); err != nil || string(summary.Resources[0].Total) != "9223372036854775807" {
		t.Fatal("remote summary rounded count", summary, err)
	}
}

func TestMeshEmptyOrForeignReceiptCannotSettleUnknownRequest(t *testing.T) {
	key := local.ID()
	q, _ := newMeshIntent("mesh_operator_cancel", key, meshRevisionRequest{Key: key, Revision: 1})
	for _, op := range []meshops.Operation{{}, {Key: local.ID(), Revision: 1, Kind: "connect", Phase: "completed"}, {Key: key, Revision: 0, Kind: "connect", Phase: "completed"}} {
		if validateMeshOutcome(q, op) == nil {
			t.Fatal("unbound acknowledgement could clear exact intent", op)
		}
	}
	if err := validateMeshOutcome(q, meshops.Operation{Key: key, Revision: 2, Kind: "connect", Phase: "cancelled"}); err != nil {
		t.Fatal(err)
	}
}

func TestMeshLateOriginalResponseCannotClearNewRetryAttempt(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "operations"}
	key := local.ID()
	q, _ := newMeshIntent("mesh_operator_cancel", key, meshRevisionRequest{Key: key, Revision: 3})
	if cmd := m.sendMesh(q, false); cmd == nil {
		t.Fatal("initial intent not reserved")
	}
	old := m.meshUI.intent
	m.Update(streamMsg{session: s, err: errors.New("old connection lost")})
	newSession := newTestTransport()
	m.session = newSession
	m.connected = true
	if cmd := m.sendMesh(q, true); cmd == nil {
		t.Fatal("explicit same request retry unavailable")
	}
	if m.meshUI.intent.Attempts != 2 || !m.meshUI.busy {
		t.Fatal("retry did not reserve second attempt")
	}
	m.acceptMeshSend(meshSendMsg{ui: m.meshUI, session: s, request: q, intent: old, result: meshops.Operation{Key: key, Revision: 4, Kind: "connect", Phase: "cancelled"}})
	if !m.meshUI.busy || m.meshUI.unknown == nil || m.meshUI.intent.Attempts != 2 {
		t.Fatal("late old success cleared current attempt")
	}
	records, err := loadPending(m.home)
	if err != nil || len(records) != 1 || records[0].Attempts != 2 {
		t.Fatal("late response cleared durable newer retry", records, err)
	}
}

func TestUncertainResumeCannotBeResentAsIdempotentRetry(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	key := local.ID()
	q, _ := newMeshIntent("mesh_operator_resume", key, meshRevisionRequest{Key: key, Revision: 3})
	r, err := newPendingRecord("mesh", m.data.status.Host, q)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistPending(m.home, r, false); err != nil {
		t.Fatal(err)
	}
	m.meshUI = &meshDashboard{mode: "operations", unknown: &q, intent: &r}
	if cmd := m.meshKey("t"); cmd != nil || m.meshUI.prompt != nil {
		t.Fatal("uncertain resume exposed resend confirmation")
	}
	if cmd := m.sendMesh(q, true); cmd != nil || len(s.operations) != 0 {
		t.Fatal("uncertain Resume dispatched another child attempt")
	}
	records, err := loadPending(m.home)
	if err != nil || len(records) != 1 || records[0].Attempts != 1 {
		t.Fatal("blocked resend changed retained uncertainty", records, err)
	}
}

func TestDisconnectReviewUsesActualIdentityDTO(t *testing.T) {
	raw := json.RawMessage(`{"operation":"disconnect-direct-peer","host":{"name":"build-node","host":"exact-host","owner":"1000","owner_username":"builder","address":"192.0.2.20:48520","fingerprint":"actual-certificate-pin","spki_sha256":"actual-public-key","expires_at":"2027-01-01T00:00:00Z","certificate":"PUBLIC-PEM-MUST-STAY-IN-RECORD"},"pair":"exact-pair","local_revision":2,"remote_revision":4,"ssh_account":"verified-ssh-user","ssh_host_key":"SHA256:verified","reason":"operator cleanup","effect":"Local revocation first; existing obligations remain."}`)
	text, ok := meshReviewSummary(raw)
	if !ok {
		t.Fatal(text)
	}
	for _, want := range []string{"builder (UID 1000)", "actual-certificate-pin", "actual-public-key", "exact-host", "exact-pair", "revision: 2", "revision: 4", "verified-ssh-user", "operator cleanup", "existing obligations remain"} {
		if !strings.Contains(text, want) {
			t.Fatal("disconnect review omitted actual DTO fact", want, text)
		}
	}
	if strings.Contains(text, "PUBLIC-PEM-MUST-STAY-IN-RECORD") {
		t.Fatal("raw certificate flooded primary review")
	}
}

func TestRemoteBudgetObservationCannotRemainFreshIndefinitely(t *testing.T) {
	now := time.Now()
	ui := &nodeDashboard{lastRead: now, summary: nodeSummary{Memory: json.RawMessage(`{"estimated_available_bytes":1024,"stale":false,"age_ns":1000000000,"stale_after_ns":4000000000}`)}}
	if nodeBudgetUnavailable(ui, "memory", now) {
		t.Fatal("fresh known memory unavailable")
	}
	if !nodeBudgetUnavailable(ui, "memory", now.Add(5*time.Second)) {
		t.Fatal("old snapshot looked like current headroom")
	}
	ui.summary.Memory = json.RawMessage(`{"estimated_available_bytes":null,"stale":false}`)
	if !nodeBudgetUnavailable(ui, "memory", now) {
		t.Fatal("unknown memory looked like zero/current availability")
	}
}

func TestWorkflowInvalidationSurvivesBusyReadOrMutation(t *testing.T) {
	for _, busy := range []bool{false, true} {
		m, s := fixtureDashboard(t)
		m.view = 6
		m.meshUI = &meshDashboard{mode: "operations", loading: !busy, busy: busy}
		m.Update(streamMsg{session: s, workflow: &client.WorkflowChange{Key: local.ID(), Revision: 2}})
		if !m.meshUI.dirty {
			t.Fatal("workflow invalidation lost behind in-flight work", busy)
		}
		m.meshUI.loading = false
		m.meshUI.busy = false
		_, cmd := m.Update(tickMsg(m.timeNow.Add(time.Second)))
		if cmd == nil || !m.meshUI.loading || m.meshUI.dirty {
			t.Fatal("deferred invalidation was not consumed by a read-only refresh", busy)
		}
		if len(s.operations) != 0 {
			t.Fatal("tick replayed mutation instead of preparing read", s.operations)
		}
	}
}
