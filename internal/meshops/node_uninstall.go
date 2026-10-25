package meshops

import (
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
	"time"
)

type UninstallDraft struct {
	Host         string                `json:"host"`
	ProvisionKey string                `json:"provision_key"`
	Reason       string                `json:"reason"`
	Policy       model.ShutdownRequest `json:"policy"`
}
type UninstallNativeRequest struct {
	Action   string           `json:"action"`
	Transfer meshssh.Transfer `json:"installation"`
	Key      string           `json:"key"`
}

func (a *Coordinator) Uninstall(key string, q UninstallDraft, yes, dry bool) (int, error) {
	return a.run(func() (int, error) { return a.uninstall(key, q, yes, dry) })
}
func (a *Coordinator) uninstall(key string, q UninstallDraft, yes, dry bool) (int, error) {
	if !mesh.ValidKey(key) || !mesh.ValidKey(q.ProvisionKey) {
		return 2, model.Err("invalid_request", "exact uninstall and source provisioning keys required")
	}
	if e := (StopDraft{Host: q.Host, Reason: q.Reason, Policy: q.Policy}).Validate(); e != nil {
		return 2, e
	}
	digest, e := mesh.ReviewDigest(q)
	if e != nil {
		return 1, e
	}
	if a.guard == nil {
		if prior, ok, e := mesh.ReadCoordination(a.Home, key); e != nil {
			return 1, e
		} else if ok {
			if prior.Kind != "uninstall" || prior.Digest != digest {
				return 1, model.Err("idempotency_conflict", "original uninstall request differs")
			}
			return 0, a.Print(Project(prior))
		}
	}
	provision, ok, e := mesh.ReadCoordination(a.Home, q.ProvisionKey)
	if e != nil {
		return 1, e
	}
	if !ok || provision.Kind != "provision" || !provision.Approved {
		return 1, model.Err("uninstall_unavailable", "original approved source provisioning receipt required")
	}
	original, e := uninstallProvisionRequest(provision, q.ProvisionKey)
	if e != nil {
		return 1, e
	}
	peer, scope, e := a.stopPeer(q.Host)
	if e != nil {
		return 1, e
	}
	connection, e := a.stopConnection(peer)
	if e != nil {
		return 1, e
	}
	if connection == nil || connection.SSHLocator != original.Destination || connection.SSHAccount != original.SSH.Account || connection.SSHHostKey != original.SSH.HostKey || connection.RemoteHome != original.Home || connection.Binary != mesh.UninstallInstalledPath(original.Transfer) {
		return 1, model.Err("node_context_changed", "exact source-managed install/connection binding unavailable")
	}
	var plan mesh.UninstallPlan
	if e = a.stopRPC(q.Host, scope, "node_uninstall_plan", "", mesh.UninstallPrepare{Key: key, Reason: q.Reason, Transfer: original.Transfer, Policy: q.Policy}, &plan); e != nil {
		return 1, e
	}
	hash, e := plan.Digest()
	stop := plan.Stop.Plan
	if e != nil || plan.Key != key || plan.Transfer != original.Transfer || plan.Reason != q.Reason || stop.Policy != q.Policy || stop.Target.Host != peer.Identity.Host || stop.Target.Certificate != peer.Identity.Fingerprint || stop.Target.SPKI != peer.Identity.SPKI || stop.Target.Owner != peer.Identity.Owner || stop.Target.Home != connection.RemoteHome || stop.Actor.Pair != scope.Pair || stop.Actor.GrantRevision != scope.Revision {
		return 1, model.Err("node_context_changed", "uninstall review differs from exact requested destination")
	}
	if !dry && !(a.Machine && !yes) {
		if e = a.begin(key, "uninstall", digest, "host:"+q.Host); e != nil {
			return 1, e
		}
		if e = a.guard.Change(func(op *mesh.Coordination) error { raw, err := json.Marshal(q); op.Request = raw; return err }); e != nil {
			return 1, e
		}
	}
	if e = a.Confirm(plan, yes, dry); e != nil {
		return 1, e
	}
	if dry {
		return 0, nil
	}
	if a.guard == nil {
		return 1, model.Err("confirmation_required", "explicit review approval required")
	}
	if e = a.markApproved(); e != nil {
		return 1, e
	}
	// Store the exact admitted plan before the sole remote apply. Lost replies
	// lead to original-key lookup, never another preparation or apply.
	pending := mesh.UninstallResult{Receipt: mesh.UninstallReceipt{Schema: mesh.UninstallSchema, Plan: plan, SHA256: hash, Phase: "unknown"}, Detail: "Admission outcome unknown; original-key lookup only."}
	if e = a.guard.Change(func(op *mesh.Coordination) error { raw, err := json.Marshal(pending); op.Result = raw; return err }); e != nil {
		return 1, e
	}
	var receipt mesh.UninstallReceipt
	if e = a.stopRPC(q.Host, scope, "node_uninstall_apply", key, mesh.UninstallApply{Key: key, Plan: plan, SHA256: hash}, &receipt); e != nil {
		_ = a.Print(pending)
		return 1, model.Err("node_uninstall_outcome_unknown", fmt.Sprintf("original key %q requires lookup; apply is never retried: %v", key, e))
	}
	if receipt.SHA256 != hash || receipt.Plan.Key != key || mesh.ValidateUninstallResult(plan, mesh.UninstallResult{Receipt: receipt, Detail: "Original target admission"}) != nil {
		_ = a.Print(pending)
		return 1, model.Err("node_context_changed", "uninstall admission receipt differs")
	}
	pending.Receipt = receipt
	pending.Detail = "Original admission retained. Run --lookup; then --finish --yes after positive original Stop closure. No automatic removal retry."
	if receipt.Phase == "stop-admitted" {
		var completed mesh.UninstallResult
		_, finishErr := meshssh.UninstallHelper(a.Context, connection.SSHLocator, plan.Recovery, plan.Transfer, UninstallNativeRequest{Action: "node_uninstall_finish", Transfer: plan.Transfer, Key: key}, original.SSH, &completed)
		if finishErr == nil && mesh.ValidateUninstallResult(plan, completed) == nil && completed.Receipt.SHA256 == hash && completed.StopObserved && completed.ArtifactAbsent && completed.Receipt.Phase == "removed" {
			return 0, a.Print(completed)
		}
		if finishErr != nil {
			pending.Detail += " Original one-shot completion observation/removal unavailable: " + finishErr.Error()
		}
	}
	if e = a.Print(pending); e != nil {
		return 1, e
	}
	return 1, model.Err("node_uninstall_incomplete", "original admission retained; inspect original key before an explicit finish")
}

// UninstallLookup is a separate explicit reconciliation. Read-only lookup never
// admits Stop or deletion. --finish reviews the immutable original plan again.
func (a *Coordinator) UninstallLookup(key, host string, finish, yes bool) (int, error) {
	return a.UninstallLookupAt(key, host, 0, finish, yes)
}
func (a *Coordinator) UninstallLookupAt(key, host string, expectedRevision int64, finish, yes bool) (int, error) {
	op, ok, e := mesh.ReadCoordination(a.Home, key)
	if e != nil {
		return 1, e
	}
	if expectedRevision != 0 && op.Revision != expectedRevision {
		return 1, model.Err("review_changed", "original uninstall source revision changed")
	}
	if !ok || op.Kind != "uninstall" || !op.Approved {
		return 1, model.Err("not_found", "approved original uninstall receipt required")
	}
	var q UninstallDraft
	if e = mesh.Decode(op.Request, &q); e != nil || q.Host != host {
		return 1, model.Err("idempotency_conflict", "original uninstall host differs")
	}
	if op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		return 1, model.Err("worker_active", "original uninstall worker is still active")
	}
	var result mesh.UninstallResult
	if e = mesh.Decode(op.Result, &result); e != nil {
		return 1, e
	}
	var plan mesh.UninstallPlan
	if e = mesh.Decode(op.Review, &plan); e != nil {
		return 1, e
	}
	hash, e := plan.Digest()
	if e != nil || hash != op.ReviewHash || plan.Key != key {
		return 1, model.Err("review_changed", "original uninstall review changed")
	}
	if e = mesh.ValidateUninstallResult(plan, result); e != nil {
		return 1, e
	}
	if op.Phase == "completed" && result.Receipt.Phase == "removed" && result.StopObserved && result.ArtifactAbsent {
		return 0, a.Print(result)
	}
	priorResult := result
	localFailure := func(err error) (int, error) { _ = a.Print(Project(op)); return 1, err }
	peer, own, e := a.stopPinnedPeer(host)
	if e != nil {
		return localFailure(e)
	}
	connection, e := a.stopConnection(peer)
	if e != nil {
		return localFailure(e)
	}
	if connection == nil || connection.RemoteHome != plan.Stop.Plan.Target.Home || connection.Binary != mesh.UninstallInstalledPath(plan.Transfer) {
		return localFailure(model.Err("node_context_changed", "original native connection unavailable"))
	}
	action := "node_uninstall_lookup"
	if finish {
		if e = a.Confirm(plan, yes, false); e != nil {
			return 1, e
		}
		action = "node_uninstall_finish"
	}
	_, e = meshssh.UninstallHelper(a.Context, connection.SSHLocator, plan.Recovery, plan.Transfer, UninstallNativeRequest{Action: action, Transfer: plan.Transfer, Key: key}, meshssh.Identity{Account: connection.SSHAccount, HostKey: connection.SSHHostKey}, &result)
	if e != nil && !finish {
		// A pre-Stop copy/admission failure may have no executable recovery helper.
		// This fallback is strictly a read against the same authenticated destination.
		if scope, scopeErr := a.stopLiveScope(peer, own); scopeErr == nil {
			var read mesh.UninstallResult
			if readErr := a.stopRPC(host, scope, "node_uninstall_lookup", "", mesh.UninstallLookupRequest{Key: key, Transfer: plan.Transfer}, &read); readErr == nil {
				result = read
				e = nil
			}
		}
	}
	inspectionError := e
	if e != nil {
		result = priorResult
	} else if e = mesh.ValidateUninstallResult(plan, result); e != nil {
		return 1, e
	}
	raw, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		return 1, encodeErr
	}
	saved, saveErr := mesh.RecordUninstallLookup(a.Home, key, op.Revision, raw, inspectionError)
	if saveErr != nil {
		_ = a.Print(result)
		return 1, saveErr
	}
	if e = a.Print(result); e != nil {
		return 1, e
	}
	if inspectionError != nil {
		return 1, model.Err("node_uninstall_incomplete", fmt.Sprintf("original uninstall remains unresolved; no automatic retry: %v", inspectionError))
	}
	if finish && saved.Phase != "completed" {
		return 1, model.Err("node_uninstall_incomplete", "original source owner or removal outcome remains unresolved")
	}
	return 0, nil
}

// recordProvisionRequest persists the shared StartRequest envelope, not the
// nested request alone. Require that exact completed original source evidence.
func uninstallProvisionRequest(op mesh.Coordination, key string) (ProvisionRequest, error) {
	var saved StartRequest
	if op.Kind != "provision" || op.Key != key || !op.Approved || op.Phase != "completed" || op.Worker != "" || op.Error != nil {
		return ProvisionRequest{}, model.Err("uninstall_unavailable", "completed original source provisioning receipt required")
	}
	if e := mesh.Decode(op.Request, &saved); e != nil {
		return ProvisionRequest{}, e
	}
	if saved.Kind != "provision" || saved.Key != key || saved.Provision == nil || saved.Connect != nil || saved.Reconnect != nil || saved.Disconnect != nil || saved.Stop != nil || saved.Uninstall != nil {
		return ProvisionRequest{}, model.Err("idempotency_conflict", "original source provisioning wrapper differs")
	}
	original := *saved.Provision
	if e := original.Validate(); e != nil {
		return ProvisionRequest{}, e
	}
	digest, e := mesh.ReviewDigest(original)
	if e != nil || digest != op.Digest || original.Transfer.Key != key {
		return ProvisionRequest{}, model.Err("idempotency_conflict", "original source provisioning request digest differs")
	}
	return original, nil
}
