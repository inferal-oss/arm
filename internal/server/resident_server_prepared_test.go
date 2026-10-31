package server

import (
	"context"
	"testing"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/lifetimeview"
)

func TestPreparedServerRefusesUnopenedAndForeignBeforeNative(t *testing.T) {
	view := &lifetimeview.ServerView{}
	originalManager := &manager.ResidentManagerPending{}
	owner, err := newResidentServerPending(view, originalManager)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*residentServerPending{nil, {}, {self: owner}, owner} {
		if _, err = candidate.validatePreparedOriginal(context.Background(), view, originalManager); err != errResidentServerOriginal {
			t.Fatal(err)
		}
	}
	if owner.active != nil || owner.preparedTried || owner.startTried || owner.openTried || owner.claim != nil || owner.preparedValidation != nil || owner.server.Manager != nil {
		t.Fatal("refusal borrowed Manager or started work")
	}
}
