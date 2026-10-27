package server

import (
	"errors"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func TestPoolReadRefusesOversizedSnapshotWithoutTruncatingReview(t *testing.T) {
	view := model.PoolView{Pool: model.Pool{ID: "pool"}, Users: make([]model.ResourceUse, 900)}
	for i := range view.Users {
		view.Users[i] = model.ResourceUse{Reason: strings.Repeat("x", 4096)}
	}
	result, err := boundedPoolRead(view, nil)
	var detail *model.Error
	if result != nil || !errors.As(err, &detail) || detail.Code != "response_too_large" {
		t.Fatalf("oversized read: %T %v", result, err)
	}
	if len(view.Users) != 900 {
		t.Fatal("read truncated authoritative users")
	}
	failure := model.Err("pool_not_found", "not found")
	if _, err := boundedPoolRead(nil, failure); err != failure {
		t.Fatal("read obscured backend refusal")
	}
	if result, err := boundedPoolRead(model.PoolView{Pool: view.Pool}, nil); result == nil || err != nil {
		t.Fatal("small read refused", err)
	}
}
