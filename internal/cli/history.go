package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
)

func (a *app) historyCommand(args []string) (int, error) {
	if a.group("history", args) {
		return 0, nil
	}
	if len(args) == 0 || args[0] != "ssh-source" {
		return 2, model.Err("invalid_request", "use arm history ssh-source --help")
	}
	args = args[1:]
	if a.group("history ssh-source", args) {
		return 0, nil
	}
	if len(args) == 0 {
		return 2, fmt.Errorf("source history command required")
	}
	if args[0] == "recovery" {
		return a.historyRecovery(args[1:])
	}
	action := args[0]
	if action != "plan" && action != "archive" && action != "inspect" && action != "reconcile" && action != "receipt" {
		return 2, model.Err("invalid_request", "unknown source history command")
	}
	f := a.flags("history ssh-source " + action)
	f.BoolVar(&a.forceJSON, "json", a.forceJSON, "force structured output")
	var key, reason, target, before, expected string
	limit := 64
	var yes, dry bool
	if action == "plan" || action == "archive" {
		f.StringVar(&target, "target", "", "exact stable target host ID")
		f.StringVar(&before, "before", "", "receipts settled before RFC3339 time")
		f.IntVar(&limit, "limit", 64, "maximum selected receipts (1..64)")
	}
	if action == "archive" || action == "receipt" {
		f.StringVar(&key, "key", "", "exact original operation key")
	}
	if action == "archive" {
		f.StringVar(&reason, "reason", "", "required archive purpose")
		f.StringVar(&expected, "expect-plan", "", "exact reviewed plan SHA256")
	}
	if action == "reconcile" {
		f.StringVar(&expected, "expect-request", "", "exact original request SHA256")
	}
	if action == "archive" || action == "reconcile" {
		f.BoolVar(&yes, "yes", false, "approve this exact local history operation")
		f.BoolVar(&dry, "dry-run", false, "review without initializing or changing history")
	}
	visited, e := a.historyParse(f, args[1:])
	if e != nil {
		return 2, e
	}
	if yes && dry {
		return 2, model.Err("invalid_request", "--yes and --dry-run cannot be combined")
	}
	if (action == "archive" || action == "receipt") && !historyText(key, 128) {
		return 2, model.Err("invalid_request", "--key requires nonempty text of at most 128 bytes")
	}
	if visited["reason"] && !historyText(reason, 4096) {
		return 2, model.Err("invalid_request", "reason requires nonempty text of at most 4096 bytes")
	}
	if (visited["expect-plan"] || visited["expect-request"]) && !historyHashPattern.MatchString(expected) {
		return 2, model.Err("invalid_request", "expected digest must be 64 lowercase hexadecimal characters")
	}
	selection := sshservice.SourceHistorySelection{Limit: limit, Target: target}
	if action == "plan" || action == "archive" {
		if limit < 1 || limit > 64 || target != "" && !historyHostPattern.MatchString(target) {
			return 2, model.Err("invalid_request", "selection requires 1..64 receipts and an exact stable target ID")
		}
		if before != "" {
			selection.Before, e = time.Parse(time.RFC3339Nano, before)
			if e != nil {
				return 2, model.Err("invalid_request", "--before requires an RFC3339 timestamp")
			}
			selection.Before = selection.Before.UTC()
		}
	}
	home, registry, b, e := a.sshOfflineAuthority()
	if e != nil {
		return 1, e
	}
	ctx, cancel := a.historyContext()
	defer cancel()
	switch action {
	case "receipt":
		id, e := historyTarget(registry, f.Arg(0))
		if e != nil {
			return 1, e
		}
		v, e := sshservice.LookupSourceHistory(ctx, home, b, id, key)
		if e != nil {
			return 1, e
		}
		return 0, a.print(v)
	case "inspect":
		v, e := sshservice.InspectSourceHistoryOperation(ctx, home, b, f.Arg(0))
		if e != nil {
			return 1, e
		}
		return 0, a.print(v)
	case "reconcile":
		key = f.Arg(0)
		v, e := sshservice.InspectSourceHistoryOperation(ctx, home, b, key)
		if e != nil {
			return 1, e
		}
		if visited["expect-request"] && expected != v.RequestSHA256 {
			return 1, model.Err("idempotency_conflict", "original history request differs from reviewed digest")
		}
		if v.Phase == "completed" {
			return 0, a.print(v)
		}
		if a.machine() && !dry && (!yes || expected != v.RequestSHA256) {
			return 1, model.Err("confirmation_required", "reconcile requires --yes and exact --expect-request from inspect; nothing changed")
		}
		review := historyReview{Operation: "Continue only the original archive steps", Binding: b, Key: key, Reason: v.Request.Reason, Digest: v.RequestSHA256, Phase: v.Phase, Plan: &v.Request.Plan, Request: v.Request}
		if e = a.historyConfirm(review, yes, dry); e != nil {
			return 1, e
		}
		if dry {
			return 0, nil
		}
		v, e = sshservice.ReconcileSourceHistory(ctx, home, b, key)
		if e != nil {
			return 1, historyUnknown(key, "archive", e)
		}
		return 0, a.print(v)
	case "archive":
		old, e := sshservice.InspectSourceHistoryOperation(ctx, home, b, key)
		if e == nil {
			if visited["reason"] && reason != old.Request.Reason || visited["expect-plan"] && expected != old.Request.Plan.SHA256 || visited["target"] && target != old.Request.Plan.Selection.Target || visited["before"] && !selection.Before.Equal(old.Request.Plan.Selection.Before) || visited["limit"] && limit != old.Request.Plan.Selection.Limit {
				return 1, model.Err("idempotency_conflict", "explicit flags differ from the original immutable archive request")
			}
			return 0, a.print(old)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return 1, e
		}
		if !historyText(reason, 4096) {
			return 2, model.Err("invalid_request", "new archive requires --reason")
		}
		plan, e := sshservice.PlanSourceHistory(ctx, home, b, selection)
		if e != nil {
			return 1, e
		}
		if expected != "" && expected != plan.SHA256 {
			return 1, model.Err("plan_changed", "selected source receipts changed; review a fresh plan")
		}
		if a.machine() && !dry && (!yes || expected != plan.SHA256) {
			return 1, model.Err("confirmation_required", "archive requires --yes and exact --expect-plan from plan; nothing changed")
		}
		q := sshservice.SourceHistoryRequest{Schema: 1, Key: key, Reason: reason, Plan: plan}
		if e = a.historyConfirm(historyReview{Operation: "Archive verified local source receipts", Binding: b, Key: key, Reason: reason, Digest: plan.SHA256, Plan: &plan, Request: q}, yes, dry); e != nil {
			return 1, e
		}
		if dry {
			return 0, nil
		}
		result, e := sshservice.ArchiveSourceHistory(ctx, home, b, q)
		if e != nil {
			return 1, historyUnknown(key, "archive", e)
		}
		return 0, a.print(result)
	default:
		plan, e := sshservice.PlanSourceHistory(ctx, home, b, selection)
		if e != nil {
			return 1, e
		}
		return 0, a.print(struct {
			Plan sshservice.SourceHistoryPlan `json:"plan"`
			SHA  string                       `json:"plan_sha256"`
		}{plan, plan.SHA256})
	}
}
func (a *app) historyRecovery(args []string) (int, error) {
	if a.group("history ssh-source recovery", args) {
		return 0, nil
	}
	if len(args) == 0 {
		return 2, fmt.Errorf("recovery command required")
	}
	action := args[0]
	if action != "plan" && action != "inspect" && action != "apply" {
		return 2, model.Err("invalid_request", "unknown history recovery command")
	}
	f := a.flags("history ssh-source recovery " + action)
	f.BoolVar(&a.forceJSON, "json", a.forceJSON, "force structured output")
	var key, reason, expected string
	var yes, dry bool
	if action != "inspect" {
		f.StringVar(&key, "key", "", "exact recovery operation key")
		f.StringVar(&reason, "reason", "", "required recovery purpose")
	}
	if action == "apply" {
		f.StringVar(&expected, "expect-review", "", "SHA256 of the complete typed recovery request")
		f.BoolVar(&yes, "yes", false, "approve exact reviewed recovery")
		f.BoolVar(&dry, "dry-run", false, "review without changing recovery state")
	}
	visited, e := a.historyParse(f, args[1:])
	if e != nil {
		return 2, e
	}
	if action == "inspect" {
		key = f.Arg(0)
	}
	if !historyText(key, 128) || visited["reason"] && !historyText(reason, 4096) {
		return 2, model.Err("invalid_request", "valid exact key and nonempty purpose required")
	}
	if yes && dry {
		return 2, model.Err("invalid_request", "--yes and --dry-run cannot be combined")
	}
	if visited["expect-review"] && !historyHashPattern.MatchString(expected) {
		return 2, model.Err("invalid_request", "expected review must be 64 lowercase hexadecimal characters")
	}
	home, _, b, e := a.sshOfflineAuthority()
	if e != nil {
		return 1, e
	}
	ctx, cancel := a.historyContext()
	defer cancel()
	if action == "inspect" {
		v, e := sshservice.InspectSourceHistoryRecovery(ctx, home, b, key)
		if e != nil {
			return 1, e
		}
		return 0, a.print(v)
	}
	old, inspectErr := sshservice.InspectSourceHistoryRecovery(ctx, home, b, key)
	if inspectErr == nil {
		if visited["reason"] && reason != old.Reason {
			return 1, model.Err("idempotency_conflict", "original recovery reason differs")
		}
		reason = old.Reason
	} else if !errors.Is(inspectErr, os.ErrNotExist) {
		return 1, inspectErr
	}
	if !historyText(reason, 4096) {
		return 2, model.Err("invalid_request", "new recovery requires --reason")
	}
	q, e := sshservice.PlanSourceHistoryRecovery(ctx, home, b, key, reason)
	if e != nil {
		return 1, e
	}
	digest := historyReviewSHA(q)
	if expected != "" && expected != digest {
		return 1, model.Err("idempotency_conflict", "complete recovery review differs")
	}
	if action == "plan" {
		return 0, a.print(struct {
			Request sshservice.SourceHistoryRecoveryRequest `json:"request"`
			SHA     string                                  `json:"review_sha256"`
		}{q, digest})
	}
	if inspectErr == nil && old.Completed != nil {
		return 0, a.print(old)
	}
	if a.machine() && !dry && (!yes || expected != digest) {
		return 1, model.Err("confirmation_required", "recovery apply requires --yes and exact --expect-review from recovery plan; nothing changed")
	}
	if e = a.historyConfirm(historyReview{Operation: "Recover this exact local SQLite journal and verify all history", Binding: b, Key: key, Reason: reason, Digest: digest, Request: q}, yes, dry); e != nil {
		return 1, e
	}
	if dry {
		return 0, nil
	}
	v, e := sshservice.RecoverSourceHistoryCatalog(ctx, home, b, q)
	if e != nil {
		return 1, historyUnknown(key, "recovery", e)
	}
	return 0, a.print(v)
}
