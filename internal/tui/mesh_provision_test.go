package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/buildinfo"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/meshssh"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

// Typed synthetic display/decision controls, not native installation evidence.
func provisionTUIReview(t *testing.T) (meshops.ProvisionPlan, meshops.Operation) {
	t.Helper()
	connection := "connect-ui"
	key := meshops.ProvisionKey(connection)
	transfer := meshssh.Transfer{Root: "/private/fixture-install", Key: key, SHA256: strings.Repeat("a", 64), Size: 33554432, ExpectedUID: "501", OS: "linux", Arch: "arm64", Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "Install for resource coordination"}
	p := meshops.ProvisionPlan{Operation: "install-and-start-arm-node", Key: key, Request: meshops.ProvisionRequest{ConnectionKey: connection, Destination: "fixture", Transfer: transfer, SSH: meshssh.Identity{Account: "operator", HostKey: "SHA256:fixture"}, Home: "/private/fixture-state", Startup: mesh.DefaultStartup()}, ArtifactSource: "native trusted local catalog / SHA256 " + transfer.SHA256, Binary: mesh.UninstallInstalledPath(transfer), Effect: "Install this exact verified software and preserve existing state; no trust is granted."}
	raw, _ := json.Marshal(p)
	hash, e := mesh.ReviewDigest(p)
	if e != nil || p.Validate() != nil {
		t.Fatal(p, e)
	}
	return p, meshops.Operation{Key: key, Kind: "provision", Revision: 3, Targets: []string{"ssh:fixture"}, Phase: "awaiting_review", WorkerLive: true, Review: raw, ReviewHash: hash, ReviewRevision: 3, ReviewExpires: time.Now().Add(time.Minute)}
}
func provisionTUIComplete(t *testing.T) (meshops.ProvisionPlan, meshops.Operation) {
	t.Helper()
	p, op := provisionTUIReview(t)
	q := p.Request
	result := meshops.ProvisionResult{Provisioned: p.Binary, Key: p.Key, ConnectionKey: q.ConnectionKey, VerifiedAt: time.Now(), Receipt: mesh.ProvisionReceipt{Schema: 1, Request: q.Transfer, Phase: "started_verified", Binary: p.Binary, Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, StartedHost: strings.Repeat("b", 32), StartHome: q.Home, Startup: &q.Startup, Runtime: &mesh.RuntimeProof{Host: strings.Repeat("b", 32), UID: q.Transfer.ExpectedUID, SHA256: q.Transfer.SHA256, Size: q.Transfer.Size, Version: q.Transfer.Version, Protocol: q.Transfer.Protocol, Method: "synthetic display test", Captured: time.Now()}}}
	op.Result, _ = json.Marshal(result)
	op.Approved = true
	op.WorkerLive = false
	op.LookupOnly = true
	op.Phase = "completed"
	op.ProvisionConnectionReady = true
	return p, op
}
func TestMeshProvisionUsesRealDashboardDraftRoutingAndClosedKeys(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "nodes"}
	if cmd := m.key(tea.KeyPressMsg{Code: 'p'}); cmd != nil || m.meshUI.form == nil || !m.meshUI.form.provisionInstall {
		t.Fatal("displayed installation key did not open draft")
	}
	f := m.meshUI.form
	f.fields[0].value = "@reviewed-node"
	f.fields[1].value = "Install ARM for coordinated work"
	q, e := f.request()
	if e != nil || !q.LookupOnly || q.Operation != "mesh_operator_provision_prepare" {
		t.Fatal(q, e)
	}
	var request meshProvisionPrepareRequest
	if e = json.Unmarshal(q.Params, &request); e != nil || request.Key != meshops.ProvisionKey(request.Provision.ConnectionKey) || request.Provision.Destination != "" || request.Provision.Connector != "reviewed-node" {
		t.Fatal(request, e)
	}
	m.meshKey("p")
	if m.meshUI.prompt == nil || m.meshUI.prompt.title != "PREPARE INSTALLATION REVIEW" {
		t.Fatal("no explicit preparation prompt")
	}
	m.meshKey("n")
	if m.meshUI.prompt != nil || m.meshUI.form != f || m.meshUI.intent != nil {
		t.Fatal("default no dispatched or lost draft")
	}
}
func TestMeshProvisionFinalConsentKeepsImmutableSnapshotAndDefaultNo(t *testing.T) {
	m, s := fixtureDashboard(t)
	_, op := provisionTUIReview(t)
	m.view = 6
	m.width = 100
	m.height = 36
	m.meshUI = &meshDashboard{mode: "operation", operation: &op}
	m.meshDecision(true)
	if m.meshUI.prompt == nil || !m.meshUI.prompt.request.LookupOnly || len(s.operations) != 0 {
		t.Fatal("installation approval sent without explicit confirmation")
	}
	captured := string(m.meshUI.prompt.provisionReview)
	request := string(m.meshUI.prompt.request.Params)
	m.meshUI.operation.Review[0] = 'X'
	for _, width := range []int{100, 56, 40} {
		m.width = width
		screen := ansi.Strip(m.meshText())
		for _, want := range []string{"operator", "501", "TRUSTED INSTALLATION", "trust", "WHY"} {
			if !strings.Contains(screen, want) {
				t.Fatal("review lost important fact", width, want, screen)
			}
		}
	}
	m.meshKey("i")
	if m.record == nil {
		t.Fatal("immutable installation record unavailable")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.record != nil || m.meshUI.prompt == nil || string(m.meshUI.prompt.provisionReview) != captured || string(m.meshUI.prompt.request.Params) != request {
		t.Fatal("record inspection changed exact pending decision")
	}
	m.meshKey("n")
	if m.meshUI.prompt != nil || len(s.operations) != 0 {
		t.Fatal("default no sent a decision")
	}
}
func TestMeshProvisionRejectsBadRevisionsAndOnlyAdvancesProvisionalMetadata(t *testing.T) {
	_, ready := provisionTUIReview(t)
	before := ready
	before.Phase = "preparing"
	before.Revision = 2
	before.ReviewRevision = 2
	if !meshProvisionSameReview(before, ready) {
		t.Fatal("real preparing-to-ready revision rejected")
	}
	for name, change := range map[string]func(*meshops.Operation){"zero-operation": func(op *meshops.Operation) { op.Revision = 0 }, "zero-review": func(op *meshops.Operation) { op.ReviewRevision = 0 }, "future-review": func(op *meshops.Operation) { op.ReviewRevision = op.Revision + 1 }, "foreign-host": func(op *meshops.Operation) { op.Targets = []string{"ssh:foreign"} }, "foreign-hash": func(op *meshops.Operation) { op.ReviewHash = strings.Repeat("0", 64) }} {
		t.Run(name, func(t *testing.T) {
			bad := ready
			change(&bad)
			if meshProvisionOperationBound(bad) || meshProvisionSameReview(before, bad) {
				t.Fatal("altered review accepted")
			}
		})
	}
	changed := ready
	changed.Revision++
	changed.ReviewRevision++
	if meshProvisionSameReview(ready, changed) {
		t.Fatal("committed review revision changed")
	}
	before.LookupOnly = true
	if meshProvisionSameReview(before, ready) {
		t.Fatal("lookup-only provisional reference advanced review revision")
	}
}
func TestMeshProvisionFollowupNeedsVerifiedSourceAndKeepsDestinationReadOnly(t *testing.T) {
	m, _ := fixtureDashboard(t)
	p, op := provisionTUIComplete(t)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "operation", operation: &op}
	m.meshKey("g")
	f := m.meshUI.form
	if f == nil || f.provisionSourceKey != op.Key || f.provisionConnectionKey != p.Request.ConnectionKey {
		t.Fatal("separate verified connection form missing")
	}
	m.meshKey("enter")
	if m.inputMode != "" {
		t.Fatal("original destination became editable")
	}
	f.fields[0].value = "attacker"
	f.fields[1].value = "Review fresh mesh grants"
	q, e := f.request()
	var request meshPrepareRequest
	if e != nil || json.Unmarshal(q.Params, &request) != nil || q.Key != p.Request.ConnectionKey || request.Connect == nil || request.Connect.ProvisionKey != op.Key || request.Connect.Destination != "" || request.Connect.Connector != "" {
		t.Fatal("followup retargeted executable/destination", q, e, request)
	}
	for _, bad := range []meshops.Operation{func() meshops.Operation { x := op; x.ProvisionConnectionReady = false; return x }(), func() meshops.Operation { x := op; x.Approved = false; return x }(), func() meshops.Operation { x := op; x.Key = "foreign"; return x }()} {
		m.meshUI = &meshDashboard{mode: "operation", operation: &bad}
		m.meshKey("g")
		if m.meshUI.form != nil || meshProvisionConfirmed(bad) {
			t.Fatal("unverified installation opened connection review")
		}
	}
}

func TestMeshProvisionLabelsAreHistoricalAndSummariesRemainNeutral(t *testing.T) {
	_, full := provisionTUIComplete(t)
	if meshPhaseLabel(full) != "ARM installation completed" || strings.Contains(meshOperationAdvice(full), "is available") || !strings.Contains(meshOperationAdvice(full), "at that time") {
		t.Fatal("historical proof was presented as current node availability", meshPhaseLabel(full), meshOperationAdvice(full))
	}
	summary := full
	summary.Review = nil
	summary.Result = nil
	summary.HasResult = true
	if meshPhaseLabel(summary) != "Installation result recorded" || meshProvisionConfirmed(summary) {
		t.Fatal("summary-only row borrowed full proof", summary)
	}
	summary.Error = &model.Error{Code: "unknown", Message: "retained duty"}
	if meshPhaseLabel(summary) == "Installation result recorded" {
		t.Fatal("summary hid a retained error")
	}
	applying := full
	applying.Phase = "applying"
	applying.WorkerLive = true
	if meshPhaseLabel(applying) != "Installing reviewed ARM node" {
		t.Fatal(meshPhaseLabel(applying))
	}
	applying.Phase = "review_approved"
	if meshPhaseLabel(applying) != "Installing reviewed ARM node" {
		t.Fatal("live approved decision mislabeled")
	}
	applying.Error = &model.Error{Code: "unknown", Message: "retained live duty"}
	if meshPhaseLabel(applying) == "Installing reviewed ARM node" {
		t.Fatal("mixed error/live installation looked normal")
	}
	liveDecline := applying
	liveDecline.Approved = false
	liveDecline.Phase = "review_declined"
	if meshPhaseLabel(liveDecline) == "Keeping host unchanged" {
		t.Fatal("mixed error/live decline looked normal")
	}
	declined := full
	declined.Approved = false
	declined.Phase = "rejected"
	declined.Error = &model.Error{Code: "confirmation_declined", Message: "internal API wording"}
	if meshPhaseLabel(declined) != "Installation declined · unchanged" || !strings.Contains(meshOperationAdvice(declined), "No installation was approved") {
		t.Fatal("ordinary decline shown as unknown failure")
	}
	m, _ := fixtureDashboard(t)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "operation", operation: &declined}
	if strings.Contains(ansi.Strip(m.meshText()), "confirmation_declined") || strings.Contains(ansi.Strip(m.meshText()), "internal API wording") {
		t.Fatal("normal decline leaked raw API banner")
	}
}

func TestMeshProvisionKnownConnectionInspectsWithoutRedraftingAndRejectsLateReply(t *testing.T) {
	for _, mode := range []string{"matching", "foreign-response", "closed-view", "foreign-cache"} {
		t.Run(mode, func(t *testing.T) {
			m, s := fixtureDashboard(t)
			p, original := provisionTUIComplete(t)
			m.view = 6
			linked := meshops.Operation{Key: p.Request.ConnectionKey, Kind: "connect", Revision: 4, Phase: "completed", Approved: true, Targets: []string{"ssh:" + p.Request.Destination}}
			cached := linked
			if mode == "foreign-cache" {
				cached.Targets = []string{"ssh:foreign"}
			}
			m.meshUI = &meshDashboard{mode: "operation", operation: &original, operations: []meshops.Operation{cached}}
			calls := 0
			s.reply = func(operation string, request any, out any) error {
				calls++
				if operation != "mesh_operator_get" {
					t.Fatal("known connection attempted a mutation", operation)
				}
				raw, _ := json.Marshal(request)
				if !strings.Contains(string(raw), p.Request.ConnectionKey) {
					t.Fatal("read changed original connection key")
				}
				result := linked
				if mode == "foreign-response" {
					result.Targets = []string{"ssh:foreign"}
				}
				body, _ := json.Marshal(result)
				return json.Unmarshal(body, out)
			}
			cmd := m.meshKey("g")
			if mode == "foreign-cache" {
				if cmd != nil || m.meshUI.form != nil || m.meshUI.err == "" || calls != 0 {
					t.Fatal("foreign cache opened a new connection draft")
				}
				return
			}
			if cmd == nil || m.meshUI.form != nil || !m.meshUI.loading || m.meshUI.intent != nil {
				t.Fatal("known exact connection was redrafted")
			}
			if mode == "closed-view" {
				m.meshKey("esc")
			}
			m.Update(cmd())
			if calls != 1 || m.meshUI.loading || m.meshUI.form != nil {
				t.Fatal("read owner not retired or a draft appeared")
			}
			switch mode {
			case "matching":
				if m.meshUI.operation == nil || m.meshUI.operation.Key != linked.Key {
					t.Fatal("original linked receipt not inspected")
				}
			case "foreign-response":
				if m.meshUI.operation == nil || m.meshUI.operation.Key != original.Key || m.meshUI.err == "" {
					t.Fatal("foreign target response replaced source review")
				}
			case "closed-view":
				if m.meshUI.operation != nil || m.meshUI.mode != "operations" {
					t.Fatal("late linked read reopened closed review")
				}
			}
		})
	}
}
