package mesh

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestInheritedImagePendingCancellationAndBorrowedCloseRefuseBeforeEffects(t *testing.T) {
	var census *residentrefs.ChildPending
	err := residentrefs.NewFixedChildPending(&census)
	if err != nil {
		t.Fatal(err)
	}
	_, _, custody, _, err := census.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	pending, err := NewInheritedImagePending(context.Background(), custody)
	if err != nil {
		t.Fatal(err)
	}
	copied := *pending
	if _, err = copied.RuntimeForOriginalRoot(); err == nil {
		t.Fatal("copied borrower accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = pending.VerifyOriginal(ctx, ResidentImageInventory{}); err != context.Canceled {
		t.Fatal(err)
	}
	if pending.state.runtime.InspectOriginal().RawDescriptorRetained || census.InspectOriginal().MaterializedEntries != 0 {
		t.Fatal("cancelled original acquired an image")
	}
	borrower := &RuntimeExecutable{inherited: pending}
	if err = borrower.Close(); err != errResidentInheritedBorrowedClose {
		t.Fatal("borrower close allowed", err)
	}
}
