package mesh

import (
	"context"
	"strings"
	"testing"
)

func TestResidentImagePinOriginalInvalidInputsRefuse(t *testing.T) {
	valid := strings.Repeat("a", 64)
	for _, input := range []struct {
		home, debit, digest string
		size                int64
	}{
		{"relative", valid, valid, 1}, {"/", valid, valid, 1},
		{"/unused/../pin", valid, valid, 1}, {"/unused", strings.Repeat("A", 64), valid, 1},
		{"/unused", valid, "z" + valid[1:], 1}, {"/unused", valid, valid, 0},
		{"/unused", valid, valid, MaxArtifactBytes + 1},
	} {
		if owner, err := NewResidentImagePinPending(input.home, input.debit, input.digest, input.size); err == nil || owner != nil {
			t.Fatal("invalid constructor accepted")
		}
	}
}
func TestResidentImagePinOriginalZeroAndCopiedHolderRefuse(t *testing.T) {
	if err := (&ResidentImagePinPending{}).PrepareOriginal(t.Context()); err == nil {
		t.Fatal("zero accepted")
	}
	original, err := NewResidentImagePinPending("/unused-resident-pin", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	copied := *original
	if err := copied.PrepareOriginal(t.Context()); err == nil {
		t.Fatal("copied owner accepted")
	}
	seen := original.InspectOriginal()
	if seen.PrepareAttempted || seen.FilesRetained != 0 || seen.Prepared {
		t.Fatal(seen)
	}
	if source := original.source.InspectOriginal(); source.CaptureAttempted {
		t.Fatal(source)
	}
}
func TestResidentImagePinOriginalCancelledBeforeEffectsRetainsFailure(t *testing.T) {
	original, err := NewResidentImagePinPending("/unused-resident-pin", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := original.PrepareOriginal(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	seen := original.InspectOriginal()
	if !seen.PrepareAttempted || seen.Prepared || seen.OperationPending || seen.FilesRetained != 0 || seen.FirstError != context.Canceled || seen.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	if source := original.source.InspectOriginal(); source.CaptureAttempted {
		t.Fatal(source)
	}
	if err := original.PrepareOriginal(t.Context()); err != context.Canceled {
		t.Fatal("failure lost or retried", err)
	}
}
