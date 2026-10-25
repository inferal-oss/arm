package mesh

import (
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/model"
	"time"
)

// ValidateUninstallResult binds native reports to the immutable reviewed scope.
// Positive flags come only from the fixed native inspector, never a PID probe.
func ValidateUninstallResult(p UninstallPlan, r UninstallResult) error {
	hash, e := p.Digest()
	actual, actualErr := r.Receipt.Plan.Digest()
	if e != nil || actualErr != nil || actual != hash || r.Receipt.SHA256 != hash || r.Receipt.Schema != UninstallSchema || len(r.Detail) > 4096 {
		return fmt.Errorf("uninstall report differs from original reviewed plan")
	}
	switch r.Receipt.Phase {
	case "unknown":
		if r.StopObserved || r.ArtifactAbsent {
			return fmt.Errorf("unknown admission claims completion")
		}
	case "copy-intent", "copy-unknown", "accepted", "stop-admitted", "stop-unknown", "remove-intent", "removed":
		if e = validateNativeUninstallReceipt(r.Receipt); e != nil {
			return e
		}
	default:
		return fmt.Errorf("unknown uninstall report phase")
	}
	if r.StopObserved && !r.Receipt.RecoveryIdentity.BirthKnown {
		return fmt.Errorf("positive observation lacks the original retained recovery identity")
	}
	if r.ArtifactAbsent && r.Receipt.Phase != "removed" && r.Receipt.Phase != "remove-intent" {
		return fmt.Errorf("absence has no original removal intent")
	}
	return nil
}

// RecordUninstallLookup settles one explicitly requested native observation by
// exact source revision. It never clears an unjoined worker or retries effects.
func RecordUninstallLookup(home, key string, revision int64, result json.RawMessage, inspectionError error) (Coordination, error) {
	unlock, e := coordinationLock(home)
	if e != nil {
		return Coordination{}, e
	}
	defer unlock()
	op, ok, e := ReadCoordination(home, key)
	if e != nil {
		return op, e
	}
	if !ok || op.Kind != "uninstall" || op.Revision != revision || !op.Approved || op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		return op, model.Err("review_changed", "original uninstall receipt changed or its worker is still active")
	}
	var plan UninstallPlan
	if e = Decode(op.Review, &plan); e != nil {
		return op, e
	}
	hash, e := plan.Digest()
	if e != nil || hash != op.ReviewHash || plan.Key != key {
		return op, model.Err("review_changed", "original uninstall review differs")
	}
	var request struct {
		Host         string                `json:"host"`
		ProvisionKey string                `json:"provision_key"`
		Reason       string                `json:"reason"`
		Policy       model.ShutdownRequest `json:"policy"`
	}
	if e = Decode(op.Request, &request); e != nil {
		return op, e
	}
	digest, e := ReviewDigest(request)
	if e != nil || digest != op.Digest || request.Host != plan.Stop.Plan.Target.Host || request.Reason != plan.Reason || request.Policy != plan.Stop.Plan.Policy || !ValidKey(request.ProvisionKey) {
		return op, model.Err("idempotency_conflict", "original uninstall request differs")
	}
	var value UninstallResult
	if e = Decode(result, &value); e != nil {
		return op, e
	}
	if e = ValidateUninstallResult(plan, value); e != nil {
		return op, e
	}
	if len(op.Result) > 0 {
		var prior UninstallResult
		if e = Decode(op.Result, &prior); e != nil {
			return op, e
		}
		if e = ValidateUninstallResult(plan, prior); e != nil {
			return op, e
		}
		if op.Phase == "completed" && prior.Receipt.Phase == "removed" && prior.StopObserved && prior.ArtifactAbsent {
			return op, nil
		}
	}
	if op.Revision == 1<<63-1 {
		return op, model.Err("history_full", "uninstall source revision exhausted")
	}
	op.Result = append(json.RawMessage(nil), result...)
	op.Phase = "incomplete"
	op.Error = &model.Error{Code: "node_uninstall_incomplete", Message: "Original uninstall remains unresolved; inspect this key; no automatic retry."}
	if inspectionError == nil && value.Receipt.Phase == "removed" && value.StopObserved && value.ArtifactAbsent && op.Worker == "" {
		op.Phase = "completed"
		op.Error = nil
	}
	if op.Worker != "" {
		op.Error = &model.Error{Code: "source_owner_unresolved", Message: "Native outcome observed, but original source worker has no retained join; its obligation remains."}
	}
	op.Revision++
	op.Updated = time.Now().UTC()
	e = saveCoordination(home, op)
	traceUninstallSource("source-cas-return", plan, home, op, e)
	return op, e
}
