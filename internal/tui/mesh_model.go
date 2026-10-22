package tui

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
)

// These are presentation descriptions, never permission checks. The destination
// and coordinator independently authorize each operation against current facts.
func meshPhaseLabel(op meshops.Operation) string {
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
	hash, err := hex.DecodeString(op.ReviewHash)
	return op.Phase == "awaiting_review" && op.WorkerLive && op.ReviewRevision > 0 &&
		len(op.Review) > 0 && err == nil && len(hash) == 32 &&
		op.ReviewHash == strings.ToLower(op.ReviewHash) && now.Before(op.ReviewExpires)
}

func meshOperationAdvice(op meshops.Operation) string {
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
	labels := map[string]string{"observe-host": "Host summary", "observe-own": "Our leases", "observe-all": "All leases", "lease-own": "Manage our leases", "run": "Run programs", "message": "Send messages", "contact-read": "Read allowed contacts"}
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
