package residentservice

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestP1ChildPendingNeverCallsLegacyFourFDReceiver(t *testing.T) {
	var census *residentrefs.ChildPending
	err := residentrefs.NewFixedChildPending(&census)
	if err != nil {
		t.Fatal(err)
	}
	service, _, _, _, err := census.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	child, err := NewP1ChildPending(service)
	if err != nil {
		t.Fatal(err)
	}
	if err = child.ReceiveOriginal(context.Background()); err != ErrInvalid {
		t.Fatal("legacy receive allowed", err)
	}
	if child.owner.receiveTried {
		t.Fatal("legacy receive began")
	}
	for _, entry := range child.owner.files.slots {
		if entry != nil {
			t.Fatal("duplicate raw registry populated")
		}
	}
	if state := census.InspectOriginal(); state.MaterializedEntries != 0 || state.Registered != 13 {
		t.Fatal(state)
	}
}
