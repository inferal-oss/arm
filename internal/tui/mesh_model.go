package tui

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
)

// These are presentation descriptions, never permission checks. The destination
// and coordinator independently authorize each operation against current facts.
func meshPhaseLabel(op meshops.Operation) string {
	if op.Kind == "provision" {
		if meshProvisionConfirmed(op) {
			return "ARM installation completed"
		}
		if op.Approved && op.Error == nil && op.Phase == "completed" && !op.WorkerLive && op.HasResult && len(op.Review) == 0 && len(op.Result) == 0 {
			return "Installation result recorded"
		}
		if op.Approved && op.Error == nil && op.WorkerLive && (op.Phase == "applying" || op.Phase == "review_approved") {
			return "Installing reviewed ARM node"
		}
		if !op.Approved && op.Error == nil && op.WorkerLive && op.Phase == "review_declined" {
			return "Keeping host unchanged"
		}
		if op.Phase == "rejected" && op.Error != nil && op.Error.Code == "confirmation_declined" {
			return "Installation declined · unchanged"
		}
		if op.Approved {
			return "Original installation needs inspection"
		}
	}
	if op.Kind == "uninstall" && len(op.Result) > 0 {
		return strings.SplitN(meshUninstallOutcome(op), "\n", 2)[0]
	}
	if op.Kind == "stop" {
		if len(op.Result) > 0 {
			return strings.SplitN(meshStopOperationResult(op), "\n", 2)[0]
		}
		if op.LookupOnly || op.Approved {
			if op.HasResult {
				return "Stop result recorded"
			}
			return "Stop decision recorded"
		}
		if op.CanPrepareFresh {
			return "Stop not applied"
		}
		if op.Phase == "awaiting_review" {
			return "Waiting for stop review"
		}
		if op.Phase == "preparing" {
			return "Preparing stop review"
		}
		return "Recorded stop operation"
	}
	if op.ResolvedBy != "" {
		return "Resolved by recovery"
	}
	if op.CancelRequested && op.WorkerLive {
		return "Cancellation requested"
	}
	if op.Phase == "rejected" && op.Error != nil && op.Error.Code == "confirmation_declined" {
		return "Declined"
	}
	if op.Phase == "rejected" && op.CancelRequested && !op.Approved {
		return "Cancelled before approval"
	}
	labels := map[string]string{
		"preparing": "Preparing review", "awaiting_review": "Waiting for your review",
		"review_approved": "Review approved", "review_declined": "Declining review", "applying": "Applying reviewed change",
		"completed": "Completed", "incomplete": "Needs attention", "rejected": "Not applied",
		"declined": "Declined", "cancelled": "Cancelled", "handed_off": "Handed off · inspect independently",
	}
	if label, ok := labels[op.Phase]; ok {
		return label
	}
	if op.Phase != "" {
		return safe(op.Phase)
	}
	return "Unknown"
}

func meshCanReview(op meshops.Operation, now time.Time) bool {
	if op.Kind == "provision" && (!meshProvisionOperationBound(op) || op.Approved || op.LookupOnly) {
		return false
	}
	if op.Kind == "uninstall" {
		p, allowed := parseMeshUninstallReview(op.Review)
		if !meshUninstallOperationBound(op) || !allowed || op.Approved || op.LookupOnly || now.Before(p.Stop.Plan.Observed) || !now.Before(p.Stop.Plan.Expires) {
			return false
		}
	}
	if op.Kind == "stop" {
		r, valid := parseMeshStopReview(op.Review)
		if !meshStopOperationBound(op) || !valid || now.Before(r.Plan.Observed) || !now.Before(r.Plan.Expires) {
			return false
		}
	}
	hash, err := hex.DecodeString(op.ReviewHash)
	return !(op.Kind == "stop" && (op.LookupOnly || op.Approved)) && op.Phase == "awaiting_review" && op.WorkerLive && op.ReviewRevision > 0 &&
		len(op.Review) > 0 && err == nil && len(hash) == 32 &&
		op.ReviewHash == strings.ToLower(op.ReviewHash) && now.Before(op.ReviewExpires)
}

func meshOperationAdvice(op meshops.Operation) string {
	if op.Kind == "provision" {
		if meshProvisionConfirmed(op) {
			return "This receipt records the verified installation and startup at that time. Current node availability and peer trust are separate. [g] inspects a known linked connection or opens its separate review."
		}
		if op.Approved && op.Error == nil && op.Phase == "completed" && !op.WorkerLive && op.HasResult && len(op.Review) == 0 && len(op.Result) == 0 {
			return "A completed installation result is recorded. Open its exact receipt to inspect the original evidence; this summary does not establish current availability or peer trust."
		}
		if !op.Approved && op.Error != nil && op.Error.Code == "confirmation_declined" {
			return "You kept this host unchanged. No installation was approved. The exact review and decision remain recorded."
		}
		if op.Approved || op.LookupOnly {
			return "Installation may already have effects. Inspect this exact original key; upload, startup and catalog streaming are never resent here."
		}
		return "Review only the managed installation and startup. Keep this host unchanged by default; mesh trust requires a separate later decision."
	}
	if op.Kind == "uninstall" {
		if op.Approved || op.LookupOnly {
			return "Original uninstall is lookup-only. Explicit finish completes only its admitted removal after fresh ownership and Stop checks; it never reapplies Stop."
		}
		return "No uninstall is approved yet. Review the exact managed executable, affected work and retained data. Decline keeps this installation unchanged."
	}
	if op.Kind == "stop" {
		if op.LookupOnly || op.Approved {
			return "Read this exact Stop receipt; it will never be resent or resumed here."
		}
		if op.CanPrepareFresh {
			return "This attempt ended before admission. A new stop requires a fresh review and key; the old receipt remains."
		}
		return "ARM is still running until a positive stop result is recorded. Review its exact backend and affected work."
	}
	if op.ResolvedBy != "" {
		return "Recovery " + safe(op.ResolvedBy) + " recorded the outcome. The original receipt is preserved."
	}
	if op.Phase == "rejected" && op.Error != nil && op.Error.Code == "confirmation_declined" {
		return "This exact trust review was declined. No new trust was enabled; ordinary prepared backends and identities may remain."
	}
	if op.Phase == "rejected" && op.CancelRequested && !op.Approved {
		return "Cancellation ended this attempt before trust approval. Ordinary prepared backends and identities may remain."
	}
	switch op.Phase {
	case "awaiting_review":
		return "No new trust is enabled. Check the exact identities, endpoints and permissions before deciding."
	case "completed":
		if op.Kind == "disconnect" {
			return "This enrollment is disconnected. Existing leases and cleanup obligations retain their lifetimes."
		}
		return "The reviewed connection is established. Permissions remain directional and destination-enforced."
	case "handed_off":
		return "Workflow exclusion was released. This does not prove cleanup, revoke trust, or release leases."
	}
	if op.Phase == "incomplete" || (!op.WorkerLive && op.Phase != "rejected" && op.Phase != "declined" && op.Phase != "cancelled") {
		return "The outcome may be partial or unknown. Inspect this exact receipt. Closing or reconnecting never retries it."
	}
	return "Closing keeps this operation discoverable. No action is replayed on reconnect."
}

func meshGrantText(grant *model.NodeGrant) string {
	if grant == nil {
		return "Unknown · no authority inferred"
	}
	if len(grant.Permissions) == 0 {
		return "No access"
	}
	labels := map[string]string{"observe-host": "Host summary", "observe-own": "Our leases", "observe-all": "All leases", "lease-own": "Manage our leases", "run": "Run programs", "node-stop": "Stop this ARM backend", "node-uninstall": "Remove managed ARM executable", "message": "Send messages", "contact-read": "Read allowed contacts"}
	parts := make([]string, 0, len(grant.Permissions))
	for _, permission := range grant.Permissions {
		label, ok := labels[permission]
		if !ok {
			label = "Unrecognized permission: " + safe(permission)
		}
		parts = append(parts, label)
	}
	text := strings.Join(parts, " · ")
	if grant.Limits.CPU != 0 || grant.Limits.Memory != 0 || grant.Limits.Disk != 0 || grant.MaxTTL != 0 {
		text += fmt.Sprintf("\nCeilings: %g cores · %s memory · %s disk · TTL %s", grant.Limits.CPU, byteCount(fmt.Sprint(grant.Limits.Memory)), byteCount(fmt.Sprint(grant.Limits.Disk)), grant.MaxTTL)
	}
	if grant.OperatorInbox {
		text += "\nOperator inbox allowed"
	}
	if len(grant.Contacts) > 0 {
		text += "\nExact allowed contact IDs: " + safe(strings.Join(grant.Contacts, ", "))
	}
	return text
}

func meshFreshnessText(f model.NodeReadFreshness) string {
	if !f.Known {
		return "Unavailable · no successful observation"
	}
	if f.Received == nil {
		return "Observation time unavailable"
	}
	label := "Observed"
	if f.Stale {
		label = "STALE observation"
	}
	return label + " " + f.Received.Local().Format(time.RFC1123) + " · not authorization"
}
