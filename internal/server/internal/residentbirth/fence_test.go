package residentbirth

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/server/internal/residentservice"
)

// These are pure refusal cases. They invent no grant/debit/active native holder.
func TestComposedFreshFenceRequiresOriginalFundedServiceGroup(t *testing.T) {
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	for _, group := range []*residentservice.P1ServiceGroup{nil, {}} {
		if fence, err := NewFreshFencePending(group, &mesh.ResidentImagePinPending{}, command); err == nil || fence != nil {
			t.Fatal("unfunded constructor accepted")
		}
	}
}
func TestComposedFreshFenceZeroHoldersAndNonclosingJournalRefuse(t *testing.T) {
	for _, fence := range []*FreshFence{nil, {}} {
		if prepared, err := fence.PrepareOriginal(context.Background()); err == nil || prepared != nil {
			t.Fatal("zero prepare accepted")
		}
		if err := fence.BindOriginal(context.Background(), &PreparedFence{}); err == nil {
			t.Fatal("zero bind accepted")
		}
		if err := fence.WithdrawOriginalWriter(context.Background(), &PreparedFence{}); err == nil {
			t.Fatal("zero withdrawal accepted")
		}
		if err := fence.ValidateOriginalBeforeStart(context.Background(), &PreparedFence{}, &exec.Cmd{}); err == nil {
			t.Fatal("zero validation accepted")
		}
	}
	for _, journal := range []*LaunchJournal{nil, {}} {
		if err := journal.AppendOriginal(context.Background(), []byte("synthetic")); err == nil {
			t.Fatal("zero journal accepted")
		}
	}
}
