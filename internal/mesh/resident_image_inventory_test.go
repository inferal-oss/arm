package mesh

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Pure pre-effect cases only. No capture/pin/grant/native Start or custody proof.
func TestResidentImageInventoryOriginalZeroCopiedAndUnpreparedRefuse(t *testing.T) {
	actual, err := NewResidentImagePinPending("/unopened-synthetic-home", strings.Repeat("a", 64), strings.Repeat("b", 64), 1)
	if err != nil {
		t.Fatal(err)
	}
	copied := *actual
	for _, subject := range []*ResidentImagePinPending{nil, {}, &copied, actual} {
		if _, err = subject.InventoryOriginal(context.Background()); err == nil {
			t.Fatal("unprepared inventory accepted")
		}
	}
	state := actual.InspectOriginal()
	if state.FilesRetained != 0 || state.PrepareAttempted || state.Prepared || actual.p1InventoryTried {
		t.Fatal(state)
	}
	// Adding the census does not give an unprepared pin an executable binding.
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	binding, err := NewResidentImageP1Pending(actual, command)
	if err != nil {
		t.Fatal(err)
	}
	if err = binding.BindOriginal(context.Background()); err == nil {
		t.Fatal("unprepared pin bound")
	}
}
