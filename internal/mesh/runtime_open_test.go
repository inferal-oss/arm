package mesh

import (
	"context"
	"testing"
)

func TestResidentRuntimeOriginalZeroAndCopiedHolderRefuse(t *testing.T) {
	if err := (&ResidentRuntimePending{}).CaptureOriginal(t.Context()); err == nil {
		t.Fatal("zero holder accepted")
	}
	original := NewResidentRuntimePending()
	copied := *original
	if err := copied.CaptureOriginal(t.Context()); err == nil {
		t.Fatal("copied holder accepted")
	}
	seen := original.InspectOriginal()
	if seen.CaptureAttempted || seen.RawDescriptorRetained || seen.FileRetained || seen.ImageRetained {
		t.Fatal(seen)
	}
}
func TestResidentRuntimeOriginalCancelledBeforeEffectsRetainsFailure(t *testing.T) {
	original := NewResidentRuntimePending()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := original.CaptureOriginal(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	seen := original.InspectOriginal()
	if !seen.CaptureAttempted || seen.Captured || seen.RawDescriptorRetained || seen.FileRetained || seen.ImageRetained || seen.FirstError != context.Canceled || seen.OperationPending || seen.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	if err := original.CaptureOriginal(t.Context()); err != context.Canceled {
		t.Fatal("original failure lost or retried", err)
	}
	if _, err := original.ValidateOriginal(t.Context()); err != context.Canceled {
		t.Fatal(err)
	}
}
