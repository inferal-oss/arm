package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/meshssh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

// Synthetic display/consent controls, not native Stop or ownership evidence.
func uninstallUIReview(t *testing.T) (mesh.UninstallPlan, meshops.Operation) {
	t.Helper()
	stop := stopUIReview(t)
	key := "uninstall-ui"
	stop.Plan.Key = mesh.UninstallStopKey(key)
	hash, e := stop.Plan.Digest()
	if e != nil {
		t.Fatal(e)
	}
	id := stop.Plan.Target.Directory
	id.BirthKnown = true
	id.BornSeconds = 1
	transfer := meshssh.Transfer{Root: "/private/install", Key: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64), Size: 33554432, ExpectedUID: stop.Plan.Target.Owner, OS: "linux", Arch: "arm64", Version: "dev", Protocol: 1, Reason: "original publication"}
	p := mesh.UninstallPlan{Schema: mesh.UninstallSchema, Key: key, Reason: stop.Plan.Reason, Transfer: transfer, Ownership: mesh.InstallOwnership{Schema: 1, Transfer: transfer, Root: id, Artifact: id, Binary: id, Homes: []string{stop.Plan.Target.Home}}, Stop: nodestop.Prepared{Plan: stop.Plan, SHA256: hash, OwnerUsername: "operator"}, Recovery: mesh.UninstallRecoveryPath(transfer, key), Preserved: []string{"database", "node identity and grants", "configuration and startup policy", "lease and operation history", "backups and retained artifacts", "companion catalog configuration", "recovery helper and uninstall receipt"}}
	reviewHash, e := p.Digest()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(p)
	return p, meshops.Operation{Key: key, Kind: "uninstall", Targets: []string{"host:" + stop.Plan.Target.Host}, Phase: "awaiting_review", WorkerLive: true, Revision: 3, Review: raw, ReviewRevision: 2, ReviewHash: reviewHash, ReviewExpires: stop.Plan.Expires}
}
func TestMeshUninstallRequiresSeparateGrantAndExactSelectedProvenance(t *testing.T) {
	node := stopUINode()
	if ok, _ := meshUninstallAvailability(node); ok {
		t.Fatal("Stop implied uninstall")
	}
	node.Outbound.Grant.Permissions = append(node.Outbound.Grant.Permissions, "node-uninstall")
	if ok, _ := meshUninstallAvailability(node); !ok {
		t.Fatal("separate grants hidden")
	}
	f := newMeshStopForm(node.Host)
	f.uninstallHost = node.Host
	f.fields[2].value = "Retire node"
	if _, e := f.request(); e == nil {
		t.Fatal("draft without fresh original provenance")
	}
	f.contextReady = true
	f.provisionKey = "original-provision"
	f.provisions = []meshops.UninstallCandidate{{ProvisionKey: f.provisionKey}}
	q, e := f.request()
	if e != nil || !q.LookupOnly {
		t.Fatal(q, e)
	}
	var draft meshPrepareRequest
	if e = json.Unmarshal(q.Params, &draft); e != nil || draft.Uninstall == nil || draft.Stop != nil || draft.Uninstall.ProvisionKey != f.provisionKey || draft.Uninstall.Policy.Force || draft.Uninstall.Policy.Wait {
		t.Fatal(draft, e)
	}
	preparing := meshops.Operation{Key: q.Key, Kind: "uninstall", Targets: []string{"host:" + node.Host}, Revision: 1, Phase: "preparing"}
	if e = validateMeshOutcome(q, preparing); e != nil {
		t.Fatal("genuine uninstall preparation rejected", e)
	}
	preparing.Targets = []string{"host:" + strings.Repeat("f", 32)}
	if e = validateMeshOutcome(q, preparing); e == nil {
		t.Fatal("foreign preparation accepted")
	}
	f.provisions = nil
	if _, e = f.request(); e == nil {
		t.Fatal("removed selected receipt silently retargeted")
	}
}

func TestMeshUninstallShortcutUsesDashboardRouting(t *testing.T) {
	m, _ := fixtureDashboard(t)
	node := stopUINode()
	node.Outbound.Grant.Permissions = append(node.Outbound.Grant.Permissions, "node-uninstall")
	m.view = 6
	m.meshUI = &meshDashboard{mode: "node", node: node}
	if m.canOpenRecovery() || strings.Contains(ansi.Strip(m.exitFooter()), "[u] Recovery") {
		t.Fatal("global recovery footer conflicts with node uninstall")
	}
	if cmd := m.key(tea.KeyPressMsg{Code: 'u'}); cmd == nil || m.recovery != nil || m.meshUI.form == nil || m.meshUI.form.uninstallHost != node.Host || !m.meshUI.loading {
		t.Fatal("displayed uninstall key opened global recovery or failed to read exact provenance")
	}
	if text := meshGrantText(&node.Outbound.Grant); !strings.Contains(text, "Remove managed ARM executable") || strings.Contains(text, "Unrecognized") {
		t.Fatal("uninstall grant has no human label", text)
	}
	m.meshUI = &meshDashboard{mode: "node", node: stopUINode()}
	if cmd := m.key(tea.KeyPressMsg{Code: 'u'}); cmd != nil || m.recovery != nil || m.meshUI.form != nil || !strings.Contains(m.meshUI.err, "Separate") {
		t.Fatal("blocked uninstall bypassed grant refusal or opened recovery")
	}
	m.meshUI = &meshDashboard{mode: "nodes"}
	m.key(tea.KeyPressMsg{Code: 'u'})
	if m.recovery == nil {
		t.Fatal("global recovery lost outside the remote node action")
	}
}
func TestMeshUninstallDefaultNoRetainsImmutableReviewAcrossResizeAndRecord(t *testing.T) {
	m, s := fixtureDashboard(t)
	p, op := uninstallUIReview(t)
	if preserved := meshUninstallPreserved(p.Preserved); !strings.Contains(preserved, "database\nnode identity") || strings.Contains(preserved, `\n`) {
		t.Fatal("retained data list is escaped instead of readable lines", preserved)
	}
	m.width = 100
	m.height = 35
	m.view = 6
	m.timeNow = p.Stop.Plan.Observed.Add(time.Second)
	m.meshUI = &meshDashboard{mode: "operation", operation: &op}
	m.meshDecision(true)
	if m.meshUI.prompt == nil || !m.meshUI.prompt.request.LookupOnly || len(s.operations) != 0 {
		t.Fatal("approval sent without final confirmation")
	}
	snapshot := string(m.meshUI.prompt.uninstallReview)
	request := string(m.meshUI.prompt.request.Params)
	m.meshUI.operation.Review[0] = 'X'
	for _, width := range []int{100, 56, 40} {
		m.width = width
		screen := strings.Join(strings.Fields(strings.ReplaceAll(ansi.Strip(m.meshText()), "│", "")), " ")
		for _, want := range []string{"KEEP YOUR DATA", "operator (UID 501)", "database", "node identity", "Finish maintenance"} {
			if !strings.Contains(screen, want) {
				t.Fatal("lost human review fact", width, want, screen)
			}
		}
	}
	m.meshKey("i")
	if m.record == nil {
		t.Fatal("immutable record unavailable")
	}
	fields, ok := m.record.value.(map[string]any)
	if !ok || fields["reviewed_uninstall_snapshot"] == nil || fields["exact_request"] == nil {
		t.Fatal("record did not use immutable captured review")
	}
	m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.record != nil || string(m.meshUI.prompt.uninstallReview) != snapshot || string(m.meshUI.prompt.request.Params) != request || len(s.operations) != 0 {
		t.Fatal("inspection/resize changed consent")
	}
	m.meshKey("n")
	if m.meshUI.prompt != nil || len(s.operations) != 0 {
		t.Fatal("default Keep sent a decision")
	}
}
func TestMeshUninstallPositiveOutcomeCannotBorrowAnotherOperation(t *testing.T) {
	p, op := uninstallUIReview(t)
	op.Approved = true
	op.LookupOnly = true
	op.WorkerLive = false
	op.Phase = "completed"
	hash, _ := p.Digest()
	r := mesh.UninstallResult{Receipt: mesh.UninstallReceipt{Schema: mesh.UninstallSchema, Plan: p, SHA256: hash, Phase: "removed", Updated: time.Now(), RecoveryIdentity: p.Ownership.Binary}, StopObserved: true, ArtifactAbsent: true}
	op.Result, _ = json.Marshal(r)
	if !strings.Contains(meshUninstallOutcome(op), "Managed executable removed") {
		t.Fatal(meshUninstallOutcome(op))
	}
	for _, kind := range []string{"key", "target", "hash", "unapproved"} {
		t.Run(kind, func(t *testing.T) {
			bad := op
			switch kind {
			case "key":
				bad.Key = "other"
			case "target":
				bad.Targets = []string{"host:" + strings.Repeat("f", 32)}
			case "hash":
				bad.ReviewHash = strings.Repeat("a", 64)
			case "unapproved":
				bad.Approved = false
			}
			if strings.Contains(meshUninstallOutcome(bad), "Managed executable removed") {
				t.Fatal("unbound success", kind)
			}
		})
	}
}
func TestMeshUninstallFinishNeedsFreshNativeReadAndNeverResendsUnknown(t *testing.T) {
	m, s := fixtureDashboard(t)
	p, op := uninstallUIReview(t)
	op.Approved = true
	op.LookupOnly = true
	op.WorkerLive = false
	op.Phase = "incomplete"
	r := mesh.UninstallResult{Receipt: mesh.UninstallReceipt{Schema: mesh.UninstallSchema, Plan: p, SHA256: op.ReviewHash, Phase: "stop-admitted", Updated: time.Now(), RecoveryIdentity: p.Ownership.Binary}, StopObserved: true}
	op.Result, _ = json.Marshal(r)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "operation", operation: &op}
	if meshUninstallFinishReady(m.meshUI) {
		t.Fatal("generic receipt armed finish")
	}
	m.meshUI.uninstallCheckedKey = op.Key
	m.meshUI.uninstallCheckedRevision = op.Revision
	m.meshUI.uninstallCheckedHash = op.ReviewHash
	if !meshUninstallFinishReady(m.meshUI) {
		t.Fatal("fresh original native check hidden")
	}
	m.meshFinishUninstall()
	if m.meshUI.prompt == nil || !m.meshUI.prompt.request.LookupOnly || len(s.operations) != 0 {
		t.Fatal("finish dispatched before confirmation")
	}
	q := m.meshUI.prompt.request
	if validateMeshOutcome(q, op) == nil {
		t.Fatal("stale original source Get settled unknown finish")
	}
	confirmed := op
	confirmed.Revision++
	if e := validateMeshOutcome(q, confirmed); e != nil {
		t.Fatal("higher original finish receipt refused", e)
	}
	m.meshKey("n")
	if len(s.operations) != 0 {
		t.Fatal("Keep sent finish")
	}
	op.Revision++
	if meshUninstallFinishReady(m.meshUI) {
		t.Fatal("changed source revision retained native-read permission")
	}
}
func TestMeshUninstallContextLateReplyCannotReopenClosedEditor(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 6
	m.meshUI = &meshDashboard{mode: "node", node: stopUINode()}
	f := newMeshStopForm(m.meshUI.node.Host)
	f.uninstallHost = f.stopHost
	m.meshUI.form = f
	m.meshUI.loading = true
	m.meshUI.sequence = 7
	msg := meshReadMsg{ui: m.meshUI, session: m.session, sequence: 7, mode: "uninstall-context", uninstall: &meshops.UninstallContext{Host: f.uninstallHost, Candidates: []meshops.UninstallCandidate{{ProvisionKey: "original"}}}, node: m.meshUI.node}
	m.meshKey("esc")
	m.acceptMeshRead(msg)
	if m.meshUI.form != nil || m.meshUI.loading {
		t.Fatal("late provenance reopened editor or abandoned read ownership")
	}
}
func TestMeshUninstallBlockedDefaultStillAllowsConfirmedDecline(t *testing.T) {
	p, op := uninstallUIReview(t)
	p.Stop.Plan.Policy = model.ShutdownRequest{}
	p.Stop.SHA256, _ = p.Stop.Plan.Digest()
	op.Review, _ = json.Marshal(p)
	op.ReviewHash, _ = p.Digest()
	if meshCanReview(op, time.Now()) {
		t.Fatal("blocked default offered approve")
	}
	if !meshUninstallCanDecline(op, time.Now()) {
		t.Fatal("blocked default cannot be declined")
	}
}
