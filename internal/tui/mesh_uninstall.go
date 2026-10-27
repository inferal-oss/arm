package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshops"
	"getarm.org/internal/model"
)

// The selected remote node owns u, including its draft/review, so the global
// recovery label and mouse hit region must yield together with key routing.
func (m *dashboardModel) meshOwnsUninstallShortcut() bool {
	return m.view == 6 && m.recovery == nil && m.record == nil && m.meshUI != nil && m.meshUI.mode == "node" && m.meshUI.node != nil && !m.meshUI.node.Local
}

func meshUninstallAvailability(node *model.NodeEntry) (bool, string) {
	if ok, why := meshStopAvailability(node); !ok {
		return false, why
	}
	for _, permission := range node.Outbound.Grant.Permissions {
		if permission == "node-uninstall" {
			return true, "Prepare exact managed-installation review; data and identity are retained."
		}
	}
	return false, "Separate destination-local node-uninstall and node-stop grants are required."
}
func parseMeshUninstallReview(raw json.RawMessage) (mesh.UninstallPlan, bool) {
	var p mesh.UninstallPlan
	if json.Unmarshal(raw, &p) != nil || p.Validate() != nil {
		return p, false
	}
	impact := p.Stop.Plan.Impact
	return p, impact.Complete && len(impact.Blockers) == 0 && (impact.Outstanding == 0 || p.Stop.Plan.Policy.Wait || p.Stop.Plan.Policy.Force)
}
func meshUninstallOperationBound(op meshops.Operation) bool {
	if op.Kind != "uninstall" || !mesh.ValidKey(op.Key) || len(op.Targets) != 1 || !strings.HasPrefix(op.Targets[0], "host:") || !validMeshStopHost(strings.TrimPrefix(op.Targets[0], "host:")) {
		return false
	}
	if len(op.Review) == 0 {
		return !op.Approved && !op.LookupOnly
	}
	var p mesh.UninstallPlan
	if json.Unmarshal(op.Review, &p) != nil || p.Validate() != nil {
		return false
	}
	hash, e := p.Digest()
	return e == nil && op.Key == p.Key && op.Targets[0] == "host:"+p.Stop.Plan.Target.Host && op.ReviewHash == hash
}
func meshUninstallCanDecline(op meshops.Operation, now time.Time) bool {
	return meshUninstallOperationBound(op) && op.Phase == "awaiting_review" && op.WorkerLive && !op.Approved && !op.LookupOnly && op.ReviewRevision > 0 && now.Before(op.ReviewExpires)
}
func meshUninstallPreserved(values []string) string {
	lines := make([]string, len(values))
	for i, value := range values {
		lines[i] = safe(value)
	}
	return strings.Join(lines, "\n")
}
func meshUninstallReviewSummary(raw json.RawMessage) (string, bool) {
	p, allowed := parseMeshUninstallReview(raw)
	if p.Validate() != nil {
		return "Exact managed installation review unavailable. Keep the installation; inspect the complete original record.", false
	}
	return fmt.Sprintf("UNINSTALL THIS ARM INSTALLATION\nStop the exact backend and remove ONLY its ARM-managed executable.\n\nHost: %s\nRun as: %s (UID %s)\nARM home: %s\nOriginal executable: %s\nOriginal publication: %s\nImage SHA-256: %s\nImage bytes: %d\nBackend generation: %s\n\nPolicy: %s\nOutstanding work: %d · blockers: %d\n\nRETAINED\n%s\n\nRecovery helper: %s\n\nWHY\n%s", safe(p.Stop.Plan.Target.Host), safe(p.Stop.OwnerUsername), safe(p.Stop.Plan.Target.Owner), safe(p.Stop.Plan.Target.Home), safe(mesh.UninstallInstalledPath(p.Transfer)), safe(p.Transfer.Key), safe(p.Transfer.SHA256), p.Transfer.Size, safe(p.Stop.Plan.Target.Generation), meshStopPolicy(p.Stop.Plan.Policy), p.Stop.Plan.Impact.Outstanding, len(p.Stop.Plan.Impact.Blockers), meshUninstallPreserved(p.Preserved), safe(p.Recovery), safe(p.Reason)), allowed
}
func meshUninstallReviewWindows(raw json.RawMessage, width int) string {
	p, allowed := parseMeshUninstallReview(raw)
	if p.Validate() != nil {
		return meshStopWindow("UNINSTALL REVIEW UNAVAILABLE", "Keep this installation. Exact original scope cannot be read; inspect the complete record.", width, "warning")
	}
	account := "UID " + p.Stop.Plan.Target.Owner
	if p.Stop.OwnerUsername != "" {
		account = p.Stop.OwnerUsername + " (" + account + ")"
	}
	target := fmt.Sprintf("Host %s\nRun as %s\nARM home %s\nBackend generation %s\n%s", safe(p.Stop.Plan.Target.Host), safe(account), safe(p.Stop.Plan.Target.Home), safe(p.Stop.Plan.Target.Generation), meshStopPolicy(p.Stop.Plan.Policy))
	publication := fmt.Sprintf("Remove ONLY %s\nOriginal source receipt %s\n%s / %s · %s\nSHA-256 %s", safe(mesh.UninstallInstalledPath(p.Transfer)), safe(p.Transfer.Key), safe(p.Transfer.OS), safe(p.Transfer.Arch), byteCount(fmt.Sprint(p.Transfer.Size)), safe(p.Transfer.SHA256))
	work := fmt.Sprintf("Outstanding obligations %d · affected peers %d\n", p.Stop.Plan.Impact.Outstanding, len(p.Stop.Plan.Impact.Peers))
	for _, b := range p.Stop.Plan.Impact.Blockers {
		work += "BLOCKED · " + safe(b.Detail) + "\n"
	}
	for _, o := range p.Stop.Plan.Impact.Obligations {
		work += safe(o.ID) + " · " + safe(o.Kind) + " · " + safe(o.State) + "\n"
		work += "  " + meshStopLeasePreview(o) + "\n"
	}
	if !allowed {
		work += "Approval unavailable: current scope is incomplete or work prevents the default Stop. Keep the installation.\n"
	}
	return strings.Join([]string{meshStopWindow("STOP EXACT BACKEND", target, width, "warning"), meshStopWindow("REMOVE MANAGED EXECUTABLE", publication, width, "warning"), meshStopWindow("AFFECTED WORK", work, width, "warning"), meshStopWindow("KEEP YOUR DATA", meshUninstallPreserved(p.Preserved)+"\n\nRetained recovery helper\n"+safe(p.Recovery), width, ""), meshStopWindow("WHY", safe(p.Reason), width, "")}, "\n\n")
}
func meshUninstallOutcome(op meshops.Operation) string {
	var plan mesh.UninstallPlan
	var result mesh.UninstallResult
	if !op.Approved || !meshUninstallOperationBound(op) || json.Unmarshal(op.Review, &plan) != nil || json.Unmarshal(op.Result, &result) != nil || mesh.ValidateUninstallResult(plan, result) != nil {
		return "Uninstall outcome unknown · read the original receipt; nothing is resent."
	}
	if result.Receipt.Phase == "removed" && result.StopObserved && result.ArtifactAbsent {
		return "Managed executable removed · original backend stopped\nDatabase, identity, configuration, history and recovery remain."
	}
	return "Needs attention · " + safe(result.Receipt.Phase) + "\n" + safe(result.Detail) + "\nRead the original key. Finish is separate and never reapplies Stop."
}
func (f *meshForm) uninstallRequest() (meshIntent, error) {
	if !f.contextReady || f.provisionKey == "" {
		return meshIntent{}, fmt.Errorf("select an exact completed source receipt from a fresh context")
	}
	found := false
	for _, p := range f.provisions {
		if p.ProvisionKey == f.provisionKey {
			found = true
		}
	}
	if !found {
		return meshIntent{}, fmt.Errorf("selected source receipt changed; refresh provenance")
	}
	original, err := f.stopRequest()
	if err != nil {
		return original, err
	}
	var request meshPrepareRequest
	if err = json.Unmarshal(original.Params, &request); err != nil {
		return meshIntent{}, err
	}
	request.Kind = "uninstall"
	request.Uninstall = &meshops.UninstallDraft{Host: f.uninstallHost, ProvisionKey: f.provisionKey, Reason: request.Stop.Reason, Policy: request.Stop.Policy}
	request.Stop = nil
	return newMeshIntent("mesh_operator_prepare", request.Key, request)
}

type meshUninstallFinishRequest struct {
	Key      string `json:"key"`
	Revision int64  `json:"revision"`
	Hash     string `json:"review_sha256"`
}

func (m *dashboardModel) meshFinishUninstall() tea.Cmd {
	ui := m.meshUI
	if ui == nil || ui.operation == nil {
		return nil
	}
	if ui.unknown != nil {
		ui.err = "Inspect the unresolved original request before reviewing a new finish."
		return nil
	}
	op := *ui.operation
	if !meshUninstallFinishReady(ui) {
		ui.err = "Read the exact inactive approved uninstall receipt before explicit finish."
		return nil
	}
	q, e := newMeshIntent("mesh_operator_uninstall_finish", op.Key, meshUninstallFinishRequest{Key: op.Key, Revision: op.Revision, Hash: op.ReviewHash})
	if e != nil {
		ui.err = e.Error()
		return nil
	}
	ui.prompt = &meshPrompt{title: "FINISH ORIGINAL UNINSTALL · FINAL REVIEW", text: "Complete only the original admitted removal after a fresh native check. No Stop is prepared or resent. Default: keep unchanged.", request: q, uninstallReview: append(json.RawMessage(nil), op.Review...)}
	m.viewport.GotoTop()
	return nil
}

func cycleMeshProvenance(f *meshForm, step int) {
	if len(f.provisions) == 0 {
		return
	}
	index := -1
	for i, p := range f.provisions {
		if p.ProvisionKey == f.provisionKey {
			index = i
		}
	}
	index = max(0, min(len(f.provisions)-1, index+step))
	f.provisionKey = f.provisions[index].ProvisionKey
}
func meshUninstallFinishReady(ui *meshDashboard) bool {
	if ui == nil || ui.operation == nil {
		return false
	}
	op := ui.operation
	if op.Kind != "uninstall" || !op.Approved || op.WorkerLive || !meshUninstallOperationBound(*op) || ui.uninstallCheckedKey != op.Key || ui.uninstallCheckedRevision != op.Revision || ui.uninstallCheckedHash != op.ReviewHash || op.Error != nil && op.Error.Code == "source_owner_unresolved" {
		return false
	}
	var p mesh.UninstallPlan
	var r mesh.UninstallResult
	return json.Unmarshal(op.Review, &p) == nil && json.Unmarshal(op.Result, &r) == nil && mesh.ValidateUninstallResult(p, r) == nil && r.StopObserved && !r.ArtifactAbsent
}
