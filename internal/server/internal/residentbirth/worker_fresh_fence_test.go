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
func TestWorkerFreshFenceRequiresOriginalFundedServiceGroup(t *testing.T) {
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	for _, group := range []*residentservice.WorkerServiceGroup{nil, {}} {
		if fence, err := NewWorkerFreshFencePending(group, &mesh.WorkerResidentImagePinPending{}, command); err == nil || fence != nil {
			t.Fatal("unfunded constructor accepted")
		}
	}
}
func TestWorkerFreshFenceZeroHoldersAndNonclosingJournalRefuse(t *testing.T) {
	for _, fence := range []*WorkerFreshFencePending{nil, {}} {
		if prepared, err := fence.PrepareOriginal(context.Background()); err == nil || prepared != nil {
			t.Fatal("zero prepare accepted")
		}
		if err := fence.BindOriginal(context.Background(), &WorkerPreparedFence{}); err == nil {
			t.Fatal("zero bind accepted")
		}
		if err := fence.WithdrawOriginalWriter(context.Background(), &WorkerPreparedFence{}); err == nil {
			t.Fatal("zero withdrawal accepted")
		}
		if err := fence.ValidateOriginalBeforeStart(context.Background(), &WorkerPreparedFence{}, &exec.Cmd{}); err == nil {
			t.Fatal("zero validation accepted")
		}
	}
	for _, journal := range []*WorkerLaunchJournal{nil, {}} {
		if err := journal.AppendOriginal(context.Background(), []byte("synthetic")); err == nil {
			t.Fatal("zero journal accepted")
		}
	}
}
