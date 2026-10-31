package mesh

import (
	"strings"
	"testing"
)

func TestResidentRuntimeSourceRetirementForeignOrCopiedRefusesBeforeEffects(t *testing.T) {
	pin, err := NewResidentImagePinPending("/unused-resident-pin", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	actual := pin.source
	copied := *actual
	foreign := NewResidentRuntimePending()
	for _, candidate := range []*ResidentRuntimePending{nil, {}, &copied, foreign, actual} {
		// The nonnil original pin ensures nil-pin cannot mask holder binding checks.
		if err := candidate.retirePinSourceOriginal(t.Context(), pin); err == nil {
			t.Fatal("unbound/uncaptured source retirement accepted")
		}
	}
	if err := actual.retirePinSourceOriginal(t.Context(), nil); err == nil {
		t.Fatal("nil pin accepted")
	}
	copiedPin := *pin
	if err := actual.retirePinSourceOriginal(t.Context(), &copiedPin); err == nil {
		t.Fatal("copied pin accepted")
	}
	for _, candidate := range []*ResidentRuntimePending{actual, foreign} {
		seen := candidate.InspectOriginal()
		if seen.CaptureAttempted || seen.SourceReferenceCloseAttempted || seen.SourceReferenceCloseKnown || seen.OperationPending || seen.RawDescriptorRetained || seen.FileRetained {
			t.Fatal(seen)
		}
	}
	seen := pin.InspectOriginal()
	if seen.PrepareAttempted || seen.Prepared || seen.FilesRetained != 0 || seen.SourceReadonlyCloseKnown {
		t.Fatal(seen)
	}
}
