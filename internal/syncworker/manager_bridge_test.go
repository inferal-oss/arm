package syncworker

import (
	"github.com/inferal-oss/arm/internal/manager"
	"testing"
)

// A caller cannot pass a foreign channel/context or run the endpoint outside
// the Manager one-shot launch. This signature is part of the authority fence.
var _ func(*manager.SyncWorkerHold) error = RunDestination

func TestDestinationBridgeRejectsUnmintedOwner(t *testing.T) {
	if err := RunDestination(nil); err == nil {
		t.Fatal("nil worker owner accepted")
	}
	if err := RunDestination(&manager.SyncWorkerHold{}); err == nil {
		t.Fatal("unminted worker owner accepted")
	}
}
