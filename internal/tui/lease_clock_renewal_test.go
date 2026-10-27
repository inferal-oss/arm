package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/leaseclock"
	"github.com/inferal-oss/arm/internal/model"
)

func clockReviewLease(expires *time.Time) model.Lease {
	l := activeLease("clock-review")
	l.Expires = expires
	l.Clock = &leaseclock.Deadline{Version: 1, Boot: "controlled-review-boot", Anchor: 1, End: 1 + int64(time.Minute)}
	if expires != nil {
		l.Clock.Wall = *expires
	}
	return l
}

func TestDashboardClockRenewalIgnoresAuditWallDiscontinuities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset time.Duration
		code   string
	}{
		{"wall-forward", -24 * time.Hour, "lease_inactive"},
		{"wall-backward", 48 * time.Hour, "renewal_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			expires := time.Now().Add(tc.offset)
			lease := clockReviewLease(&expires)
			m.detail = &lease
			m.startReview("renew")
			if m.inputMode != "ttl" || m.review != nil {
				t.Fatal("calendar audit timestamp blocked native-clock review", m.notice)
			}
			m.reviewTTL("15m")
			if m.review == nil {
				t.Fatal("calendar extension comparison blocked native-clock request", m.notice)
			}
			request := m.review.request
			if request.ID != lease.ID || request.Revision != lease.Revision || request.Action != "renew" || request.TTL != 15*time.Minute {
				t.Fatal("review changed exact revision-bound duration request", request)
			}
			// Preview changes are display-only and never change the reviewed RPC.
			m.timeNow = time.Now()
			before := m.leaseReviewText()
			m.timeNow = m.timeNow.Add(tc.offset)
			if after := m.leaseReviewText(); before == after || !strings.Contains(after, "Approximate expiry if accepted now:") || !reflect.DeepEqual(m.review.request, request) {
				t.Fatal("calendar preview acquired renewal authority")
			}
			refusal := model.Err(tc.code, "Manager decides native expiry and extension")
			transport.reply = func(operation string, params, _ any) error {
				if operation != "lease_action" || !reflect.DeepEqual(params.(model.LeaseAction), request) {
					t.Fatal("confirmation changed the reviewed request", operation, params)
				}
				return refusal
			}
			if len(transport.operations) != 0 {
				t.Fatal("editing or calendar preview dispatched a mutation")
			}
			cmd := m.reviewKey("y")
			if cmd == nil || !m.pending || m.unknownLease != lease.ID {
				t.Fatal("calendar audit timestamp blocked confirmation", m.notice)
			}
			result := cmd().(leaseResultMsg)
			if result.err != refusal || len(transport.operations) != 1 || m.reviewKey("y") != nil {
				t.Fatal("Manager refusal changed or pending request could retry")
			}
			m.acceptLeaseResult(result)
			if !strings.Contains(m.notice, "Request rejected:") || m.pending || m.unknownLease != "" {
				t.Fatal("client overrode the Manager's decision", m.notice)
			}
		})
	}
}

func TestDashboardClockRenewalDefersUnavailableProvenanceToManager(t *testing.T) {
	for _, mode := range []string{"missing-calendar", "malformed-clock"} {
		t.Run(mode, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			expires := time.Now().Add(48 * time.Hour)
			lease := clockReviewLease(&expires)
			if mode == "missing-calendar" {
				lease.Expires = nil
			} else {
				lease.Clock.Version = 2
			}
			m.detail = &lease
			m.startReview("renew")
			m.reviewTTL("15m")
			if m.inputMode != "ttl" || m.review == nil {
				t.Fatal("client tried to decide unavailable native provenance", m.notice)
			}
			refusal := model.Err("lease_clock_unavailable", "clock provenance unavailable")
			transport.reply = func(string, any, any) error { return refusal }
			cmd := m.reviewKey("y")
			if cmd == nil {
				t.Fatal("native clock authority never reached Manager", m.notice)
			}
			m.acceptLeaseResult(cmd().(leaseResultMsg))
			if m.pending || m.unknownLease != lease.ID || len(transport.operations) != 1 || !strings.Contains(m.notice, "Outcome unknown") {
				t.Fatal("unavailable clock bypassed existing unknown-outcome fence", m.notice)
			}
			m.inputMode = ""
			if m.startReview("renew") != nil || m.inputMode != "" {
				t.Fatal("unknown outcome allowed a second action")
			}
		})
	}
}

func TestDashboardClockRenewalRetainsClientFences(t *testing.T) {
	for _, mode := range []string{"inactive", "disconnected", "pending", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			expires := time.Now().Add(-24 * time.Hour)
			lease := clockReviewLease(&expires)
			m.detail = &lease
			switch mode {
			case "inactive":
				lease.State = "needs_attention"
			case "disconnected":
				m.connected = false
			case "pending":
				m.pending = true
			case "unknown":
				m.unknownLease = lease.ID
			}
			if m.startReview("renew") != nil || m.inputMode != "" || m.review != nil || len(transport.operations) != 0 {
				t.Fatal("Clock presence bypassed a client action fence")
			}
		})
	}
	for _, mode := range []string{"pending", "disconnected"} {
		t.Run("confirm-"+mode, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			expires := time.Now().Add(-24 * time.Hour)
			lease := clockReviewLease(&expires)
			m.detail = &lease
			m.reviewTTL("15m")
			if m.review == nil {
				t.Fatal(m.notice)
			}
			if mode == "pending" {
				m.pending = true
			} else {
				m.connected = false
			}
			if m.reviewKey("y") != nil || len(transport.operations) != 0 {
				t.Fatal("Clock presence bypassed a confirmation fence")
			}
		})
	}
}

func TestDashboardClockRenewalKeepsDurationAndConsumerReview(t *testing.T) {
	m, transport := fixtureDashboard(t)
	expires := time.Now().Add(48 * time.Hour)
	lease := clockReviewLease(&expires)
	m.detail = &lease
	for _, text := range []string{"0s", "8d", "7d0.000000001s"} {
		m.reviewTTL(text)
		if m.review != nil {
			t.Fatal("Clock presence bypassed duration bounds", text)
		}
	}
	m.reviewTTL("7d")
	if m.review == nil || m.review.request.TTL != 7*24*time.Hour || m.review.request.Revision != lease.Revision {
		t.Fatal("valid maximum duration lost its exact revision review", m.notice)
	}
	m.review = nil
	lease.Request.Kind = "storage"
	child := activeLease("attached-consumer")
	child.Request.StorageLease = lease.ID
	m.data.status.Leases = []model.Lease{child}
	m.startReview("release")
	if m.review == nil || len(m.review.request.Consumers) != 1 || m.review.request.Consumers[0] != (model.LeaseRevision{ID: child.ID, Revision: child.Revision}) || len(transport.operations) != 0 {
		t.Fatal("Clock presence changed attached-consumer review")
	}
}

func TestDashboardLegacyRenewalKeepsCalendarGuards(t *testing.T) {
	for _, mode := range []string{"missing-expiry", "expired", "shortening", "extension", "expired-during-review"} {
		t.Run(mode, func(t *testing.T) {
			m, transport := fixtureDashboard(t)
			lease := activeLease("legacy-review")
			expires := time.Now().Add(time.Minute)
			lease.Expires = &expires
			switch mode {
			case "missing-expiry":
				lease.Expires = nil
			case "expired":
				expires = time.Now().Add(-24 * time.Hour)
			case "shortening":
				expires = time.Now().Add(48 * time.Hour)
			}
			m.detail = &lease
			m.startReview("renew")
			if mode == "missing-expiry" || mode == "expired" {
				if m.inputMode != "" || m.review != nil {
					t.Fatal("legacy expiry eligibility guard disappeared")
				}
				return
			}
			m.reviewTTL("15m")
			if mode == "shortening" {
				if m.review != nil {
					t.Fatal("legacy calendar extension comparison disappeared")
				}
				return
			}
			if m.review == nil {
				t.Fatal("legacy extension refused", m.notice)
			}
			if mode == "expired-during-review" {
				expires = time.Now().Add(-24 * time.Hour)
				if m.reviewKey("y") != nil || m.review != nil || m.pending || len(transport.operations) != 0 {
					t.Fatal("legacy confirmation expiry guard disappeared")
				}
			} else if cmd := m.reviewKey("y"); cmd == nil {
				t.Fatal("legacy valid renewal confirmation refused", m.notice)
			} else if result := cmd().(leaseResultMsg); result.request.TTL != 15*time.Minute || result.request.Revision != lease.Revision || len(transport.operations) != 1 {
				t.Fatal("legacy request changed at confirmation")
			}
		})
	}
}
