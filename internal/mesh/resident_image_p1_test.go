package mesh

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestResidentImageP1OriginalZeroCopiedAndForeignCommandRefuse(t *testing.T) {
	pin, err := NewResidentImagePinPending("/unused-resident-pin", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	original, err := NewResidentImageP1Pending(pin, &exec.Cmd{ExtraFiles: make([]*os.File, 13)})
	if err != nil {
		t.Fatal(err)
	}
	copied := *original
	for _, candidate := range []*ResidentImageP1Pending{nil, {}, &copied} {
		if err := candidate.BindOriginal(t.Context()); err == nil {
			t.Fatal("invalid holder accepted")
		}
	}
	if _, err := original.ValidateOriginal(t.Context(), &exec.Cmd{ExtraFiles: make([]*os.File, 13)}); err == nil {
		t.Fatal("foreign command accepted")
	}
	if seen := original.InspectOriginal(); seen.BindAttempted || seen.Bound || seen.OperationPending {
		t.Fatal(seen)
	}
	if _, err := NewResidentImageP1Pending(pin, &exec.Cmd{ExtraFiles: make([]*os.File, 13)}); err == nil {
		t.Fatal("second binding adopted original pin")
	}
	if seen := pin.InspectOriginal(); seen.PrepareAttempted || seen.FilesRetained != 0 {
		t.Fatal(seen)
	}
}
func TestResidentImageP1OriginalUnpreparedPinRefusesBeforeEffects(t *testing.T) {
	pin, err := NewResidentImagePinPending("/unused-resident-pin", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewResidentImageP1Pending(pin, &exec.Cmd{ExtraFiles: make([]*os.File, 13)})
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.BindOriginal(t.Context()); err == nil {
		t.Fatal("unprepared pin accepted")
	}
	seen := binding.InspectOriginal()
	if !seen.BindAttempted || seen.Bound || seen.OperationPending || seen.FirstError == nil || seen.NativeOutcome != "unknown" {
		t.Fatal(seen)
	}
	if source := pin.source.InspectOriginal(); source.CaptureAttempted || source.RawDescriptorRetained || source.SourceReferenceCloseAttempted {
		t.Fatal(source)
	}
	if seen := pin.InspectOriginal(); seen.PrepareAttempted || seen.FilesRetained != 0 {
		t.Fatal(seen)
	}
}
