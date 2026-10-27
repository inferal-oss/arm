package manager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

func TestNodeStopRetainsOriginalSSHAccountSlotUntilActualRelease(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	before := stopApplication(t, m, actor, external, model.ShutdownRequest{})
	held, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	// Controlled owner of the real bounded native-lookup slot. This does not
	// pretend to force a real platform NSS call to stall or cancel such a call.
	go func() { sshAccountSlot <- struct{}{}; close(held); <-release; <-sshAccountSlot; close(joined) }()
	<-held
	released := false
	defer func() {
		if !released {
			close(release)
		}
		<-joined
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolveSSHAccount(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation did not retain original occupied slot", err)
	}
	blocked, err := m.PlanNodeStop("lookup-pending", actor, model.ShutdownRequest{}, "wait for original lookup owner", external)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, blocker := range blocked.Impact.Blockers {
		found = found || blocker.ID == "ssh-account-lookup"
	}
	if !found || blocked.Impact.SHA256 == before.Plan.Impact.SHA256 {
		t.Fatal("pending original lookup missing from exact scope/hash", blocked.Impact)
	}
	if _, err = m.BeginNodeStop(before, actor, true, external); err == nil {
		t.Fatal("old clean review crossed a newly pending original native lookup")
	}
	if _, present, err := m.NodeStopDesiredState(); err != nil || present {
		t.Fatal("refused scope changed durable stopped intent", present, err)
	}
	close(release)
	released = true
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("original slot owner did not release")
	}
	after, err := m.PlanNodeStop("lookup-finished", actor, model.ShutdownRequest{}, "fresh review after actual lookup release", external)
	if err != nil || len(after.Impact.Blockers) != 0 || after.Impact.SHA256 != before.Plan.Impact.SHA256 {
		t.Fatal("actual original slot release did not restore clean scope", after.Impact, err)
	}
	hash, err := after.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.BeginNodeStop(nodestop.Apply{Key: after.Key, Plan: after, ReviewSHA256: hash}, actor, true, external); err != nil {
		t.Fatal("fresh exact review after actual slot release refused", err)
	}
}
