package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
)

func TestMeshReviewRequiresLiveExactUnexpiredSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	op := meshops.Operation{Phase: "awaiting_review", WorkerLive: true, ReviewRevision: 1, ReviewHash: strings.Repeat("a", 64), Review: []byte(`{"operation":"connect-direct-peers"}`), ReviewExpires: now.Add(time.Second)}
	if !meshCanReview(op, now) {
		t.Fatal("complete current review unavailable")
	}
	for _, change := range []func(*meshops.Operation){
		func(o *meshops.Operation) { o.WorkerLive = false },
		func(o *meshops.Operation) { o.ReviewRevision = 0 },
		func(o *meshops.Operation) { o.ReviewHash = strings.Repeat("A", 64) },
		func(o *meshops.Operation) { o.ReviewHash = strings.Repeat("z", 64) },
		func(o *meshops.Operation) { o.Review = nil },
		func(o *meshops.Operation) { o.Phase = "applying" },
		func(o *meshops.Operation) { o.ReviewExpires = now },
	} {
		changed := op
		change(&changed)
		if meshCanReview(changed, now) {
			t.Fatalf("invalid review enabled: %+v", changed)
		}
	}
}

func TestMeshPresentationKeepsUncertaintyAndDirectionHonest(t *testing.T) {
	declined := meshops.Operation{Phase: "rejected", Error: &model.Error{Code: "confirmation_declined"}}
	if meshPhaseLabel(declined) != "Declined" || !strings.Contains(meshOperationAdvice(declined), "identities may remain") {
		t.Fatal("real coordinator decline mislabeled", declined)
	}
	if !strings.Contains(meshGrantText(nil), "Unknown") || meshGrantText(&model.NodeGrant{}) != "No access" {
		t.Fatal("unknown rights confused with explicit none")
	}
	if !strings.Contains(meshFreshnessText(model.NodeReadFreshness{}), "Unavailable") {
		t.Fatal("missing observation looks current")
	}
	for _, op := range []meshops.Operation{{Phase: "incomplete"}, {Phase: "applying"}, {Phase: "future_state"}} {
		if !strings.Contains(meshOperationAdvice(op), "partial or unknown") {
			t.Fatal("unrecognized/non-live operation looked settled", op)
		}
	}
	op := meshops.Operation{Phase: "applying", WorkerLive: true, CancelRequested: true}
	if meshPhaseLabel(op) != "Cancellation requested" {
		t.Fatal("cancel request incorrectly claimed cancellation completed")
	}
	op.Phase = "handed_off"
	if !strings.Contains(meshOperationAdvice(op), "does not prove cleanup") {
		t.Fatal("handoff falsely implies release")
	}
}
