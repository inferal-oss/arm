package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestPoolReviewRefusesOversizedCompleteWireScope(t *testing.T) {
	view := poolFixture()
	view.Review.Consumers = make([]model.LeaseRevision, 1200)
	for i := range view.Review.Consumers {
		view.Review.Consumers[i] = model.LeaseRevision{ID: fmt.Sprintf("%032d", i), Revision: 1}
	}
	_, err := preparePoolPlan(poolDraft{operation: "set", value: "0", reason: "Complete review cannot fit a wire frame"}, &view, view.Pool.Authority)
	if err == nil || !strings.Contains(err.Error(), "60 KiB") {
		t.Fatalf("oversized review accepted: %v", err)
	}
	if len(view.Review.Consumers) != 1200 {
		t.Fatal("complete scope was truncated")
	}
}
