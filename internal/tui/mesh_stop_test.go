package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"github.com/charmbracelet/x/ansi"
)

// Typed contract values below are synthetic display controls, never runtime closure evidence.
func stopUIReview(t *testing.T) meshops.StopReview {
	t.Helper()
	target := nodestop.Target{Host: strings.Repeat("b", 32), Certificate: strings.Repeat("d", 64), SPKI: strings.Repeat("e", 64), Owner: "501", Home: "/home/operator/arm", Directory: model.FileIdentity{Device: 7, Inode: 42, Owner: 501}, Generation: strings.Repeat("c", 32), Startup: nodestop.StartupContract}
	boundary, err := nodestop.NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	actor := nodestop.Actor{Host: strings.Repeat("a", 32), Certificate: strings.Repeat("1", 64), SPKI: strings.Repeat("2", 64), Pair: strings.Repeat("f", 64), GrantRevision: 7}
	impact := nodestop.Impact{Complete: true, SHA256: strings.Repeat("3", 64), Outstanding: 1, Obligations: []nodestop.Obligation{{ID: "compile-lease", Kind: "lease", State: "active", Revision: 4}}, Blockers: []nodestop.Blocker{}, Peers: []string{actor.Host}}
	plan, err := boundary.Prepare("stop-ui", actor, 9, model.ShutdownRequest{Wait: true}, "Finish maintenance", impact, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return meshops.StopReview{Operation: "stop-arm-node", Key: plan.Key, Plan: plan, SHA256: hash, OwnerUsername: "operator", HistoryUsed: 4, HistoryCapacity: 128, CompletionCheck: meshops.StopCompletionCheck{Available: true, Detail: "Checks only the original ARM backend."}}
}
func stopUIRaw(t *testing.T, review meshops.StopReview) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func stopUINode() *model.NodeEntry {
	return &model.NodeEntry{Host: strings.Repeat("b", 32), Trust: model.NodeTrust{State: "active"}, ScopeFreshness: model.NodeReadFreshness{Known: true}, Outbound: &model.NodeAccessScope{Target: strings.Repeat("b", 32), Grant: model.NodeGrant{Permissions: []string{"observe-host", "node-stop"}}, NodeStop: &model.NodeStopAvailability{Supported: true, Reason: "Original backend completion is available."}}}
}
func TestMeshStopAvailabilityNeedsFreshGrantAndUsableBackend(t *testing.T) {
	if ok, _ := meshStopAvailability(stopUINode()); !ok {
		t.Fatal("usable observed support hidden")
	}
	for _, kind := range []string{"local", "revoked", "stale", "unknown", "grant", "wrong-target", "missing-capability", "disabled-capability"} {
		t.Run(kind, func(t *testing.T) {
			n := stopUINode()
			switch kind {
			case "local":
				n.Local = true
			case "revoked":
				n.Trust.State = "revoked"
			case "stale":
				n.ScopeFreshness.Stale = true
			case "unknown":
				n.ScopeFreshness.Known = false
			case "grant":
				n.Outbound.Grant.Permissions = []string{"run"}
			case "wrong-target":
				n.Outbound.Target = strings.Repeat("e", 32)
			case "missing-capability":
				n.Outbound.NodeStop = nil
			case "disabled-capability":
				n.Outbound.NodeStop.Supported = false
				n.Outbound.NodeStop.Reason = "Current Serve cannot confirm this backend stopped."
			}
			if ok, why := meshStopAvailability(n); ok || why == "" {
				t.Fatal("unusable Stop advertised", ok, why)
			}
		})
	}
}
func TestMeshStopDraftHasSeparateDefaultPolicyAndExactSeconds(t *testing.T) {
	f := newMeshStopForm(strings.Repeat("b", 32))
	f.fields[2].value = "Finish maintenance"
	q, err := f.request()
	if err != nil {
		t.Fatal(err)
	}
	var p meshPrepareRequest
	if json.Unmarshal(q.Params, &p) != nil || p.Kind != "stop" || p.Stop == nil || p.Stop.Host != f.stopHost || p.Stop.Policy.Force || p.Stop.Policy.Wait || p.Stop.Policy.Timeout != 0 || !q.LookupOnly {
		t.Fatal("default stop widened or changed target", string(q.Params), q)
	}
	for _, forbidden := range []string{"ssh_destination", "remote_home", "remote_binary", "source_permissions_on_target"} {
		if strings.Contains(string(q.Params), forbidden) {
			t.Fatal("privileged transport field in Stop draft", forbidden)
		}
	}
	f.fields[0].value = "wait"
	f.fields[1].value = "120"
	q, err = f.request()
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(q.Params, &p)
	if !p.Stop.Policy.Wait || p.Stop.Policy.Force || p.Stop.Policy.Timeout != 2*time.Minute {
		t.Fatal(p.Stop.Policy)
	}
	for _, bad := range []string{"-1", "1.5", "1e2", "01", "9223372037"} {
		f.fields[1].value = bad
		if _, err = f.request(); err == nil {
			t.Fatal("invalid timeout accepted", bad)
		}
	}
	f.fields[1].value = "1"
	f.fields[0].value = "force"
	if _, err = f.request(); err == nil {
		t.Fatal("timeout silently became Force")
	}
	f.fields[1].value = ""
	f.fields[2].value = " "
	if _, err = f.request(); err == nil {
		t.Fatal("blank reason admitted")
	}
}
func TestMeshStopReviewUsesExactAccountHumanPolicyAndFramedTables(t *testing.T) {
	review := stopUIReview(t)
	review.Plan.Policy = model.ShutdownRequest{Wait: true, Timeout: 2 * time.Minute}
	review.SHA256, _ = review.Plan.Digest()
	raw := stopUIRaw(t, review)
	text, valid := meshReviewSummary(raw)
	if !valid {
		t.Fatal(text)
	}
	for _, want := range []string{"operator (UID 501)", review.Plan.Target.Generation, review.Plan.Actor.Pair, "grant revision: 7", "after 2m", "Finish maintenance"} {
		if !strings.Contains(text, want) {
			t.Fatal("required reviewed fact hidden", want, text)
		}
	}
	frame := ansi.Strip(meshStopReviewWindows(raw, 126))
	for _, want := range []string{"EXACT BACKEND", "ACCESS USED", "WHAT WILL HAPPEN", "AFFECTED WORK AND CONNECTIONS", "compile-lease", "active", "COMPLETION CHECK", "OPERATOR REASON", "128"} {
		if !strings.Contains(frame, want) {
			t.Fatal("framed Stop review omitted fact", want, frame)
		}
	}
	if strings.Contains(frame, `"target"`) || strings.Contains(frame, "120000000000") {
		t.Fatal("raw JSON or duration replaced human review", frame)
	}
}
func TestMeshStopReviewRefusesMissingProofAndClockChangedExpiry(t *testing.T) {
	for _, kind := range []string{"incomplete", "blocker", "observer", "hash", "key", "refuse-active"} {
		t.Run(kind, func(t *testing.T) {
			r := stopUIReview(t)
			switch kind {
			case "refuse-active":
				r.Plan.Policy = model.ShutdownRequest{}
				r.SHA256, _ = r.Plan.Digest()
			case "incomplete":
				r.Plan.Impact.Complete = false
				r.SHA256, _ = r.Plan.Digest()
			case "blocker":
				r.Plan.Impact.Blockers = []nodestop.Blocker{{Class: "unknown", ID: "retained-work", Detail: "Keep unresolved evidence."}}
				r.SHA256, _ = r.Plan.Digest()
			case "observer":
				r.CompletionCheck.Available = false
			case "hash":
				r.SHA256 = strings.Repeat("0", 64)
			case "key":
				r.Key = "another-key"
			}
			raw := stopUIRaw(t, r)
			if _, valid := meshReviewSummary(raw); valid {
				t.Fatal("unusable Stop review allowed", kind)
			}
		})
	}
	r := stopUIReview(t)
	op := meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Phase: "awaiting_review", WorkerLive: true, Review: stopUIRaw(t, r), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: r.Plan.Expires.Add(time.Minute)}
	if !meshCanReview(op, r.Plan.Observed.Add(time.Second)) || meshCanReview(op, r.Plan.Observed.Add(-time.Nanosecond)) || meshCanReview(op, r.Plan.Expires) {
		t.Fatal("nested exact plan expiry or backward clock ignored")
	}
	op.LookupOnly = true
	if meshCanReview(op, r.Plan.Observed.Add(time.Second)) {
		t.Fatal("lookup-only receipt offered another decision")
	}
}
func TestMeshStopDefaultNoPromptDoesNotSendAndKeepsStructuredRecord(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 132
	m.height = 40
	m.view = 6
	r := stopUIReview(t)
	m.timeNow = r.Plan.Observed.Add(time.Second)
	m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Phase: "awaiting_review", WorkerLive: true, Review: stopUIRaw(t, r), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: r.Plan.Expires}}
	m.meshDecision(true)
	if m.meshUI.prompt == nil || !m.meshUI.prompt.request.LookupOnly || len(s.operations) != 0 {
		t.Fatal("Stop review dispatched without confirmation")
	}
	if !strings.Contains(ansi.Strip(m.meshText()), "EXACT BACKEND") {
		t.Fatal("confirmation lost framed review")
	}
	m.meshKey("n")
	if m.meshUI.prompt != nil || len(s.operations) != 0 {
		t.Fatal("default Keep sent Stop")
	}
	m.meshDecision(true)
	m.meshKey("i")
	if m.record == nil || len(s.operations) != 0 {
		t.Fatal("full exact record unavailable or sent work")
	}
}
func TestMeshStopUnknownPrivateRecoveryCannotResendAndKeepsExactKey(t *testing.T) {
	home := pendingHome(t)
	f := newMeshStopForm(strings.Repeat("b", 32))
	f.fields[2].value = "Finish maintenance"
	q, err := f.request()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := newPendingRecord("mesh", "localhost", q)
	if err != nil {
		t.Fatal(err)
	}
	if err = persistPending(home, pending, false); err != nil {
		t.Fatal(err)
	}
	m := newDashboard(t.Context(), home)
	s := newTestTransport()
	m.session = s
	m.connected = true
	m.connecting = false
	m.data.status.Host = "localhost"
	press(m, "u")
	press(m, "enter")
	if m.meshUI == nil || m.meshUI.unknown == nil || !m.meshUI.unknown.LookupOnly || m.meshUI.unknown.Key != q.Key || len(s.operations) != 0 {
		t.Fatal("startup changed or acted on Stop intent")
	}
	m.meshKey("t")
	if m.meshUI.prompt != nil || len(s.operations) != 0 {
		t.Fatal("Stop unknown offered resend")
	}
	if cmd := m.sendMesh(q, false); cmd != nil {
		t.Fatal("false retry argument bypassed retained Stop fence")
	}
	if cmd := m.sendMesh(q, true); cmd != nil {
		t.Fatal("send path bypassed Stop retry fence")
	}
	for _, control := range m.meshControls() {
		if control.key == "t" {
			t.Fatal("mouse offered resend")
		}
	}
	records, err := loadPending(home)
	if err != nil || len(records) != 1 || !samePending(records[0], pending) {
		t.Fatal("read-only recovery lost pending evidence", records, err)
	}
}
func TestMeshStopKnownReceiptUsesExplicitRevisionBoundLookupOnly(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	op := meshops.Operation{Key: "stop-ui", Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Phase: "incomplete", Revision: 8, Approved: true, LookupOnly: true}
	m.meshUI = &meshDashboard{mode: "operation", operation: &op}
	s.reply = func(operation string, params, result any) error {
		if operation != "mesh_operator_stop_lookup" {
			t.Fatal("unexpected operation", operation)
		}
		q := params.(map[string]any)
		if q["key"] != "stop-ui" || q["revision"] != int64(8) {
			t.Fatal("lookup changed exact key/revision", q)
		}
		*result.(*meshops.Operation) = op
		return nil
	}
	if len(s.operations) != 0 {
		t.Fatal("render triggered lookup")
	}
	m.meshText()
	m.meshControls()
	if len(s.operations) != 0 {
		t.Fatal("presentation triggered lookup")
	}
	cmd := m.meshKey("l")
	if cmd == nil {
		t.Fatal("explicit original check unavailable")
	}
	msg := cmd().(meshReadMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if len(s.operations) != 1 {
		t.Fatal(s.operations)
	}
	// A later selection cannot be overwritten by this delayed original lookup.
	replacement := &meshDashboard{mode: "nodes"}
	m.meshUI = replacement
	m.acceptMeshRead(msg)
	if m.meshUI != replacement || m.meshUI.operation != nil {
		t.Fatal("late lookup retargeted another view")
	}
}
func TestMeshStopUnknownDecisionStaysReadOnlyUntilBackendDeclaresKnownState(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	r := stopUIReview(t)
	q, err := newMeshIntent("mesh_operator_decide", r.Key, meshops.Decision{Key: r.Key, ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	q.LookupOnly = true
	pending, err := m.prepareDurable("mesh", "localhost", q, false)
	if err != nil {
		t.Fatal(err)
	}
	ui := &meshDashboard{mode: "operation", unknown: &q, intent: pending}
	m.meshUI = ui
	op := &meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Revision: 3, Phase: "awaiting_review", WorkerLive: true, Review: stopUIRaw(t, r), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: r.Plan.Expires}
	m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "operation", operation: op})
	if ui.unknown == nil {
		t.Fatal("transient awaiting-review cleared uncertain decision")
	}
	m.meshKey("a")
	if ui.prompt != nil || len(s.operations) != 0 {
		t.Fatal("uncertain decision was resent")
	}
	op.Phase = "rejected"
	op.WorkerLive = false
	op.CanPrepareFresh = true
	m.acceptMeshRead(meshReadMsg{ui: ui, session: m.session, sequence: ui.sequence, mode: "operation", operation: op})
	if ui.unknown != nil {
		t.Fatal("backend-declared safe state did not settle UI pending envelope")
	}
}
func TestMeshStopFreshReviewRequiresBackendNoEffectFlagAndNeverResumeHandoff(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	r := stopUIReview(t)
	op := &meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Revision: 3, Phase: "incomplete", Approved: true, LookupOnly: true, Review: stopUIRaw(t, r)}
	m.meshUI = &meshDashboard{mode: "operation", operation: op}
	for _, key := range []string{"s", "h", "g"} {
		if cmd := m.meshKey(key); cmd != nil || m.meshUI.prompt != nil {
			t.Fatal("admitted Stop offered recovery mutation", key)
		}
	}
	op.Approved = false
	op.LookupOnly = false
	op.Phase = "rejected"
	op.CanPrepareFresh = true
	s.reply = func(operation string, params, result any) error {
		if operation != "node_inspect" {
			return errors.New("mutation not permitted")
		}
		*result.(*model.NodeEntry) = *stopUINode()
		return nil
	}
	cmd := m.meshKey("g")
	if cmd == nil {
		t.Fatal("safe fresh review unavailable")
	}
	m.acceptMeshRead(cmd().(meshReadMsg))
	if m.meshUI.form == nil || m.meshUI.form.stopHost != r.Plan.Target.Host {
		t.Fatal("fresh review changed target")
	}
	m.meshUI.form.fields[2].value = "New reviewed maintenance"
	q, err := m.meshUI.form.request()
	if err != nil || q.Key == r.Key {
		t.Fatal("fresh review reused old key", q, err)
	}
	if len(s.operations) != 1 || s.operations[0] != "node_inspect" {
		t.Fatal("fresh editor sent mutation", s.operations)
	}
}
func TestMeshStopRecordedStoppedLabelNeedsOriginalAuthorityClosure(t *testing.T) {
	r := stopUIReview(t)
	record := nodestop.Record{Schema: nodestop.RecordSchema, Plan: r.Plan, Digest: r.SHA256, Shutdown: strings.Repeat("4", 32), DesiredRevision: r.Plan.DesiredRevision + 1, Phase: "drain-ready", Created: r.Plan.Observed, Updated: r.Plan.Observed}
	state := nodestop.State{Schema: nodestop.StateSchema, Revision: record.DesiredRevision, Running: false, Target: r.Plan.Target, Key: r.Key, Digest: r.SHA256}
	result := meshops.StopResult{Operation: "stop-arm-node", Key: r.Key, Status: "stopped", Observation: &nodestop.Observation{Record: record, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}}
	render := func(value meshops.StopResult) string {
		raw, _ := json.Marshal(value)
		return meshStopResultText(raw, r.Key)
	}
	if !strings.HasPrefix(render(result), "Original ARM backend stopped\n") {
		t.Fatal("positive synthetic display fixture refused", render(result))
	}
	restarted := result
	obs := *result.Observation
	restarted.Observation = &obs
	restarted.Observation.Desired.Running = true
	restarted.Observation.Desired.Revision++
	if !strings.Contains(render(restarted), "restart requested separately") {
		t.Fatal("authorized restart intent erased original closure", render(restarted))
	}
	for _, kind := range []string{"no-observation", "not-joined", "not-absent", "other-generation", "wrong-key", "desired-running", "dual-record"} {
		t.Run(kind, func(t *testing.T) {
			bad := result
			o := *result.Observation
			bad.Observation = &o
			switch kind {
			case "dual-record":
				copy := bad.Observation.Record
				bad.Record = &copy
			case "no-observation":
				bad.Observation = nil
			case "not-joined":
				bad.Observation.ServerJoined = false
			case "not-absent":
				bad.Observation.OriginalGenerationAbsent = false
			case "other-generation":
				bad.Observation.Desired.Target.Generation = strings.Repeat("5", 32)
			case "wrong-key":
				bad.Key = "another-key"
			case "desired-running":
				bad.Observation.Desired.Running = true
			}
			if strings.HasPrefix(render(bad), "Original ARM backend stopped\n") {
				t.Fatal("incomplete evidence fabricated stopped", kind)
			}
		})
	}
}

func TestMeshStopOutstandingDefaultCanDeclineButCannotApprove(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 6
	r := stopUIReview(t)
	r.Plan.Policy = model.ShutdownRequest{}
	r.SHA256, _ = r.Plan.Digest()
	m.timeNow = r.Plan.Observed.Add(time.Second)
	m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Phase: "awaiting_review", WorkerLive: true, Review: stopUIRaw(t, r), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: r.Plan.Expires}}
	m.meshDecision(true)
	if m.meshUI.prompt != nil || len(s.operations) != 0 {
		t.Fatal("default outstanding stop was offered for approval")
	}
	m.meshDecision(false)
	if m.meshUI.prompt == nil || len(s.operations) != 0 {
		t.Fatal("keep-running decline unavailable")
	}
	var decision meshops.Decision
	if json.Unmarshal(m.meshUI.prompt.request.Params, &decision) != nil || decision.Approve {
		t.Fatal("decline became approval")
	}
}

func TestMeshStopReviewEscapesOperatorTextAndFitsCompactDashboard(t *testing.T) {
	r := stopUIReview(t)
	r.Plan.Reason = "unsafe\x1b]52;c;copied\a reason"
	r.OwnerUsername = "operator\x1b[31m"
	r.SHA256, _ = r.Plan.Digest()
	raw := stopUIRaw(t, r)
	frame := ansi.Strip(meshStopReviewWindows(raw, 86))
	if strings.Contains(frame, "\x1b") || !strings.Contains(frame, `\u001b`) {
		t.Fatal("operator text escaped its presentation boundary", frame)
	}
	m, _ := fixtureDashboard(t)
	m.width = 132
	m.height = 40
	m.view = 6
	m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: r.Key, Kind: "stop", Targets: []string{"host:" + strings.Repeat("b", 32)}, Phase: "awaiting_review", WorkerLive: true, Review: raw, ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: r.Plan.Expires}}
	m.resize()
	m.syncViewport()
	text := ansi.Strip(m.View().Content)
	lines := strings.Split(text, "\n")
	if len(lines) != 40 {
		t.Fatal("compact dashboard escaped height", len(lines))
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 132 {
			t.Fatal("compact dashboard escaped width", line)
		}
	}
}

func TestMeshStopOutcomeCannotSubstituteTargetOrReviewedDecision(t *testing.T) {
	f := newMeshStopForm(strings.Repeat("b", 32))
	f.fields[0].value = "wait"
	f.fields[2].value = "Exact host"
	q, err := f.request()
	if err != nil {
		t.Fatal(err)
	}
	op := meshops.Operation{Key: q.Key, Kind: "stop", Targets: []string{"host:" + f.stopHost}, Revision: 2, Phase: "preparing"}
	if err = validateMeshOutcome(q, op); err != nil {
		t.Fatal(err)
	}
	op.Targets = []string{"host:" + strings.Repeat("e", 32)}
	if validateMeshOutcome(q, op) == nil {
		t.Fatal("prepare changed selected host")
	}
	decision, err := newMeshIntent("mesh_operator_decide", q.Key, meshops.Decision{Key: q.Key, ReviewRevision: 7, ReviewHash: strings.Repeat("a", 64), Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	decision.LookupOnly = true
	op.Targets = []string{"host:" + f.stopHost}
	op.Phase = "review_approved"
	op.Approved = true
	op.LookupOnly = true
	op.ReviewRevision = 7
	op.ReviewHash = strings.Repeat("a", 64)
	if err = validateMeshOutcome(decision, op); err != nil {
		t.Fatal(err)
	}
	op.ReviewHash = strings.Repeat("f", 64)
	if validateMeshOutcome(decision, op) == nil {
		t.Fatal("decision changed reviewed digest")
	}
}
func TestMeshStopResultCannotBorrowAnotherValidGeneration(t *testing.T) {
	reviewed := stopUIReview(t)
	other := reviewed
	other.Plan.Target.Generation = strings.Repeat("9", 32)
	other.SHA256, _ = other.Plan.Digest()
	record := nodestop.Record{Schema: nodestop.RecordSchema, Plan: other.Plan, Digest: other.SHA256, Shutdown: strings.Repeat("4", 32), DesiredRevision: other.Plan.DesiredRevision + 1, Phase: "drain-ready", Created: other.Plan.Observed, Updated: other.Plan.Observed}
	state := nodestop.State{Schema: nodestop.StateSchema, Revision: record.DesiredRevision, Target: other.Plan.Target, Key: other.Key, Digest: other.SHA256}
	result := meshops.StopResult{Operation: "stop-arm-node", Key: other.Key, Status: "stopped", Observation: &nodestop.Observation{Record: record, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}}
	if err := result.Validate(other); err != nil {
		t.Fatal("synthetic other-generation display control malformed", err)
	}
	raw, _ := json.Marshal(result)
	op := meshops.Operation{Key: reviewed.Key, Kind: "stop", Targets: []string{"host:" + reviewed.Plan.Target.Host}, Review: stopUIRaw(t, reviewed), Result: raw}
	if !strings.HasPrefix(meshStopOperationResult(op), "Stop outcome unknown") {
		t.Fatal("valid other-generation result borrowed old review", meshStopOperationResult(op))
	}
}

func TestMeshStopLeaseDetailsRetainReviewAndRejectLateBinding(t *testing.T) {
	review := stopUIReview(t)
	item := &review.Plan.Impact.Obligations[0]
	item.ID = "lease:compile"
	item.LeaseDetails = "permitted"
	item.Lease = &nodestop.LeaseSummary{Owner: "501", Username: "recorded-user", Reason: "Compile useful binary", Truncated: true}
	review.SHA256, _ = review.Plan.Digest()
	op := meshops.Operation{Key: review.Key, Kind: "stop", Targets: []string{"host:" + review.Plan.Target.Host}, Review: stopUIRaw(t, review), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64)}
	ui := &meshDashboard{mode: "operation", operation: &op}
	q, err := meshStopLeaseRequest(ui)
	if err != nil || q.Obligation != "lease:compile" || q.ReviewHash != op.ReviewHash {
		t.Fatal(q, err)
	}
	detail := meshops.StopLeaseDetail{Key: q.Key, ReviewRevision: q.ReviewRevision, ReviewHash: q.ReviewHash, Obligation: q.Obligation, ID: "compile", Host: review.Plan.Target.Host, Owner: "501", Username: "recorded-user", Reason: "Full useful purpose", Revision: 5, ReviewedRevision: 4, Changed: true}
	if !meshStopLeaseMatches(&op, q, detail) {
		t.Fatal("exact binding refused")
	}
	before := string(op.Review)
	text := strings.Join(strings.Fields(strings.ReplaceAll(ansi.Strip(meshStopLeaseWindow(detail, 56)), "│", "")), " ")
	for _, want := range []string{"READ ONLY", "recorded-user (UID 501)", "Full useful purpose", "Changed since review", "Keep running"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing human lease fact", want, text)
		}
	}
	long := detail
	long.Reason = strings.Repeat("meaningful segment ", 120) + "PURPOSE_SUFFIX_SENTINEL"
	for _, width := range []int{56, 32} {
		visible := strings.Join(strings.Fields(strings.ReplaceAll(ansi.Strip(meshStopLeaseWindow(long, width)), "│", "")), " ")
		for _, want := range []string{"PURPOSE_SUFFIX_SENTINEL", "Changed since review.", "Keep running choice remain.", "READ ONLY"} {
			if !strings.Contains(visible, want) {
				t.Fatal("narrow detail clipped full purpose/warning/default choice", width, want, visible)
			}
		}
		if strings.Count(visible, "meaningful segment") != 120 {
			t.Fatal("long purpose body was clipped", width)
		}
	}
	if string(op.Review) != before {
		t.Fatal("view changed review")
	}
	altered := detail
	altered.Host = strings.Repeat("e", 32)
	if meshStopLeaseMatches(&op, q, altered) {
		t.Fatal("foreign host response accepted")
	}
	op.ReviewRevision++
	if meshStopLeaseMatches(&op, q, detail) {
		t.Fatal("late details survived changed review")
	}
	if !strings.Contains(meshStopLeasePreview(*item), "recorded-user (UID 501)") || !strings.Contains(meshStopLeasePreview(*item), "Shortened preview") {
		t.Fatal("preview omitted who/why/truncation")
	}
	item.Lease = nil
	item.LeaseDetails = "not-permitted"
	review.SHA256, _ = review.Plan.Digest()
	op.Review = stopUIRaw(t, review)
	if _, err = meshStopLeaseRequest(ui); err == nil {
		t.Fatal("redacted lease became readable")
	}
	if strings.Contains(meshStopLeasePreview(*item), "recorded-user") || !strings.Contains(meshStopLeasePreview(*item), "not shared") {
		t.Fatal("redaction leaked recorded account")
	}
}

func TestMeshStopLeaseReadSelectionRetainsOwnerAndRejectsStaleSuccessAndError(t *testing.T) {
	for _, lateError := range []bool{false, true} {
		t.Run(fmt.Sprint(lateError), func(t *testing.T) {
			m, s := fixtureDashboard(t)
			m.view = 6
			review := stopUIReview(t)
			review.Plan.Impact.Outstanding = 2
			review.Plan.Impact.Obligations = []nodestop.Obligation{{ID: "lease:A", Kind: "lease", State: "active", Revision: 4, LeaseDetails: "permitted", Lease: &nodestop.LeaseSummary{Owner: "501", Reason: "A"}}, {ID: "lease:B", Kind: "lease", State: "active", Revision: 5, LeaseDetails: "permitted", Lease: &nodestop.LeaseSummary{Owner: "501", Reason: "B"}}}
			review.SHA256, _ = review.Plan.Digest()
			op := &meshops.Operation{Key: review.Key, Kind: "stop", Targets: []string{"host:" + review.Plan.Target.Host}, Review: stopUIRaw(t, review), ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64)}
			ui := &meshDashboard{mode: "operation", operation: op}
			m.meshUI = ui
			before := string(op.Review)
			s.reply = func(operation string, params, result any) error {
				if operation != "mesh_operator_stop_lease" {
					t.Fatal("read sent another operation", operation)
				}
				q := params.(*meshops.StopLeaseRequest)
				if q.Obligation == "lease:A" && lateError {
					return errors.New("stale A read failure")
				}
				*result.(*meshops.StopLeaseDetail) = meshops.StopLeaseDetail{Key: q.Key, ReviewRevision: q.ReviewRevision, ReviewHash: q.ReviewHash, Obligation: q.Obligation, ID: strings.TrimPrefix(q.Obligation, "lease:"), Host: review.Plan.Target.Host, Owner: "501", Reason: q.Obligation, Revision: 5, ReviewedRevision: 4}
				return nil
			}
			original := m.meshKey("d")
			if original == nil || !ui.loading {
				t.Fatal("original A read not retained")
			}
			sequence := ui.sequence
			m.meshKey("]")
			if ui.stopLeaseIndex != 1 || !ui.loading || ui.sequence != sequence {
				t.Fatal("selection abandoned the original loading/owner")
			}
			ui.err = "B remains selected"
			msg := original().(meshReadMsg)
			m.acceptMeshRead(msg)
			if ui.loading || ui.stopLease != nil || ui.err != "B remains selected" {
				t.Fatal("stale A success/failure replaced new selection", ui.stopLease, ui.err)
			}
			next := m.meshKey("d")
			if next == nil {
				t.Fatal("explicit B read blocked after original completion")
			}
			m.acceptMeshRead(next().(meshReadMsg))
			if ui.stopLease == nil || ui.stopLease.ID != "B" || ui.err != "" || ui.loading {
				t.Fatal("explicit current B read failed", ui.stopLease, ui.err)
			}
			if string(op.Review) != before || ui.prompt != nil || len(s.operations) != 2 {
				t.Fatal("read changed immutable review/default decision", s.operations)
			}
		})
	}
}

func TestMeshStopModalFramesUseActualViewportAndPreserveDecisionOnResize(t *testing.T) {
	m, service := fixtureDashboard(t)
	m.view = 6
	m.width, m.height = 144, 48
	m.meshUI = &meshDashboard{mode: "node", form: newMeshStopForm(strings.Repeat("b", 32))}
	for _, size := range [][2]int{{144, 48}, {120, 36}, {70, 24}, {45, 14}} {
		m.width, m.height = size[0], size[1]
		m.syncViewport()
		title, _ := m.windowTitle()
		if title != "STOP ARM · DRAFT" {
			t.Fatal("Stop draft mislabeled", title)
		}
		for _, line := range strings.Split(m.viewportContent(), "\n") {
			if ansi.StringWidth(line) > m.viewport.Width() {
				t.Fatal("draft frame wider than modal viewport", size, ansi.StringWidth(line), m.viewport.Width(), line)
			}
		}
	}
	review := stopUIReview(t)
	review.Plan.Reason = strings.Repeat("Full reviewed purpose ", 35) + "purpose-suffix"
	review.CompletionCheck.Detail = "Original helper SHA-256 " + strings.Repeat("d", 64) + "; checks only the exact original generation."
	review.SHA256, _ = review.Plan.Digest()
	raw := stopUIRaw(t, review)
	m.timeNow = review.Plan.Observed.Add(time.Second)
	m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: review.Key, Kind: "stop", Targets: []string{"host:" + review.Plan.Target.Host}, Phase: "awaiting_review", WorkerLive: true, Review: raw, ReviewRevision: 2, ReviewHash: review.SHA256, ReviewExpires: review.Plan.Expires}}
	m.meshDecision(true)
	if m.meshUI.prompt == nil || len(service.operations) != 0 {
		t.Fatal("review sent a decision")
	}
	originalRequest := string(m.meshUI.prompt.request.Params)
	originalSnapshot := string(m.meshUI.prompt.stopReview)
	// An unrelated current-operation buffer may change; the displayed final review
	// and its exact decision remain the original independently copied snapshot.
	m.meshUI.operation.Review[0] = 'X'
	for _, size := range [][2]int{{144, 48}, {120, 36}, {70, 24}, {45, 14}, {144, 48}} {
		m.width, m.height = size[0], size[1]
		m.syncViewport()
		for _, line := range strings.Split(m.viewportContent(), "\n") {
			if ansi.StringWidth(line) > m.viewport.Width() {
				t.Fatal("final review borders rewrapped outside modal viewport", size, ansi.StringWidth(line), m.viewport.Width(), line)
			}
		}
		body := ansi.Strip(m.viewportContent())
		if !strings.Contains(body, "purpose-suffix") || !strings.Contains(body, "EXACT BACKEND") {
			t.Fatal("full review content lost", size, body)
		}
		screen := ansi.Strip(m.View().Content)
		lines := strings.Split(screen, "\n")
		if len(lines) != m.height {
			t.Fatal("modal escaped terminal height", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != m.width {
				t.Fatal("modal escaped terminal width", size, ansi.StringWidth(line))
			}
		}
		if !strings.Contains(screen, "[n] Keep unchanged") || !strings.Contains(screen, "[y] Stop ARM") {
			t.Fatal("fixed default-no/action controls hidden", size, screen)
		}
		m.meshKey("end")
		if m.viewport.YOffset() == 0 {
			t.Fatal("long exact review did not remain scrollable", size)
		}
		if string(m.meshUI.prompt.request.Params) != originalRequest || string(m.meshUI.prompt.stopReview) != originalSnapshot || len(service.operations) != 0 {
			t.Fatal("resize/scroll changed exact consent or sent a request")
		}
		m.meshKey("i")
		if m.record == nil {
			t.Fatal("captured review record lost to a live-operation change")
		}
		fields, ok := m.record.value.(map[string]any)
		if !ok || len(fields) != 2 || fields["reviewed_stop_snapshot"] == nil || fields["exact_request"] == nil {
			t.Fatal("record does not distinguish the captured review and exact request", m.record.value)
		}
		var expected any
		if err := json.Unmarshal([]byte(originalSnapshot), &expected); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(expected)
		got, _ := json.Marshal(fields["reviewed_stop_snapshot"])
		if string(got) != string(want) {
			t.Fatal("structured review differs from the original framed snapshot")
		}
		m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.record != nil || m.meshUI.prompt == nil || string(m.meshUI.prompt.request.Params) != originalRequest || string(m.meshUI.prompt.stopReview) != originalSnapshot || len(service.operations) != 0 {
			t.Fatal("record Escape changed or sent the retained decision")
		}
	}
	m.meshKey("esc")
	if m.meshUI.prompt != nil || len(service.operations) != 0 {
		t.Fatal("Keep running sent a decision")
	}
}
