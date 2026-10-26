package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/meshops"
)

func recoveryReviewFixture(kind string) map[string]any {
	identity := func(name, pin string) map[string]any {
		return map[string]any{"name": name, "host": name + "-host", "owner_uid": "1000", "owner_username": name + "-user", "address": name + ".example:48500", "certificate_sha256": pin, "public_key_sha256": pin + "-public", "certificate_expires": "2027-01-01T00:00:00Z"}
	}
	forward := map[string]any{"permissions": []string{"observe-host", "observe-own"}, "limits": map[string]any{"cpu": 2}, "max_ttl_ns": int64(10 * time.Minute)}
	reverse := map[string]any{"permissions": []string{}}
	return map[string]any{"operation": kind, "original_key": "original-receipt", "pair": "exact-pair", "local_listen": "192.0.2.10:48510", "remote_listen": "192.0.2.20:48520", "local_revision": 2, "remote_revision": 3, "source_permissions_on_target": forward, "target_permissions_on_source": reverse, "local": map[string]any{"identity": identity("source", "source-pin"), "prior_trust": map[string]any{"pair": "exact-pair", "revision": 2, "state": "active", "grant": reverse}}, "remote": map[string]any{"identity": identity("target", "target-pin"), "prior_trust": map[string]any{"pair": "exact-pair", "revision": 3, "state": "active", "grant": forward}}, "effect": "Use only existing exact trust; do not enroll or retry execution."}
}

func TestExistingRestoreAndVerifyReviewsHaveConcreteHumanApproval(t *testing.T) {
	for _, kind := range []string{"restore-existing-connection", "verify-existing-connection"} {
		t.Run(kind, func(t *testing.T) {
			review := recoveryReviewFixture(kind)
			raw, _ := json.Marshal(review)
			text, supported := meshReviewSummary(raw)
			if !supported {
				t.Fatal("known generated recovery shape rejected", text)
			}
			for _, want := range []string{"original-receipt", "exact-pair", "source-user (UID 1000)", "target-user (UID 1000)", "source-pin", "target-pin", "WE MAY DO THERE", "PEER MAY DO HERE", "Host summary", "No access", "not included in this recovery plan", "do not enroll or retry execution"} {
				if !strings.Contains(text, want) {
					t.Fatal("recovery review omitted required distinction", want, text)
				}
			}
			if strings.Contains(text, `"local"`) || strings.Contains(text, `"permissions"`) {
				t.Fatal("raw JSON became primary recovery review")
			}
			m, s := fixtureDashboard(t)
			m.view = 6
			m.meshUI = &meshDashboard{mode: "operation", operation: &meshops.Operation{Key: local.ID(), Phase: "awaiting_review", WorkerLive: true, Review: raw, ReviewRevision: 2, ReviewHash: strings.Repeat("a", 64), ReviewExpires: m.timeNow.Add(time.Minute)}}
			m.meshDecision(true)
			if m.meshUI.prompt == nil || len(s.operations) != 0 {
				t.Fatal("known review could not be concretely reviewed without dispatch")
			}
			press(m, "n")
			if len(s.operations) != 0 || m.meshUI.prompt != nil {
				t.Fatal("default keep applied recovery")
			}
		})
	}
}

func TestRecoveryReviewRefusesMissingKnownEnrollmentFacts(t *testing.T) {
	verify := recoveryReviewFixture("verify-existing-connection")
	delete(verify, "source_permissions_on_target")
	restore := recoveryReviewFixture("restore-existing-connection")
	delete(restore["remote"].(map[string]any), "prior_trust")
	for _, review := range []map[string]any{verify, restore} {
		raw, _ := json.Marshal(review)
		if _, supported := meshReviewSummary(raw); supported {
			t.Fatal("missing grant/enrollment was treated as explicit no-access")
		}
	}
}
