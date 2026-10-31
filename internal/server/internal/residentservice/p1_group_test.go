package residentservice

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestP1ServiceGroupMissingOriginalDebitRefusesBeforeEffects(t *testing.T) {
	parent, err := NewParentPending("/unused-resident-home", Build{SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	for _, debit := range []*OriginalDebit{nil, {}} {
		if group, err := NewP1ServiceGroup(parent, debit, command); err == nil || group != nil {
			t.Fatal("unissued debit accepted")
		}
	}
	seen := parent.InspectOriginal()
	if seen.OpenAttempted || seen.DebitAttempted || seen.OpenedFDs != 0 || seen.OriginalAppendRetained || seen.WriterWithdrawn || seen.TransferAttempted || seen.TransferWriteComplete || seen.TransferWritten != 0 || seen.TransferWrites != 0 {
		t.Fatal(seen)
	}
}
func TestP1ServiceGroupZeroHolderRefusesBeforeEffects(t *testing.T) {
	for _, group := range []*P1ServiceGroup{nil, {}} {
		if err := group.ValidateFundingOriginal(t.Context()); err == nil {
			t.Fatal("zero funding holder accepted")
		}
		if err := group.PrepareOriginalChannel(t.Context()); err == nil {
			t.Fatal("zero channel holder accepted")
		}
		if err := group.BindOriginal(t.Context()); err == nil {
			t.Fatal("zero bind holder accepted")
		}
		if err := group.WithdrawOriginalWriter(t.Context()); err == nil {
			t.Fatal("zero writer holder accepted")
		}
		if err := group.ValidateOriginalBeforeStart(t.Context(), &exec.Cmd{}); err == nil {
			t.Fatal("zero start validation accepted")
		}
	}
}
