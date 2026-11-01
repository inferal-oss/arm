package mesh

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Constructors below allocate metadata only; no native pin/capture is prepared.
func workerPinMetadataOnly(t *testing.T) (*WorkerResidentImagePinPending, *WorkerPinParentReferenceOwner) {
	t.Helper()
	var p *WorkerResidentImagePinPending
	var r *WorkerPinParentReferenceOwner
	if err := NewWorkerResidentImagePinForHomeWithParentReferences(&p, &r, "/synthetic-worker", strings.Repeat("a", 64), strings.Repeat("b", 64), 1, ResidentImagePinHomeData{Inode: 1}); err != nil {
		t.Fatal(err)
	}
	return p, r
}

func TestWorkerImageScopesRefuseCrossRoleBindingsBeforeEffects(t *testing.T) {
	p, _ := workerPinMetadataOnly(t)
	command := &exec.Cmd{ExtraFiles: make([]*os.File, 13)}
	if _, err := NewResidentImageP1Pending(p.inner, command); err == nil {
		t.Fatal("worker pin entered legacy binding")
	}
	if p.inner.p1Binding != nil || p.inner.parentReferences != nil || p.inner.parentRetirement != nil {
		t.Fatal("legacy ownership installed")
	}
	b, err := NewWorkerImageBindingPending(p, command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewWorkerImageBindingPending(p, command); err == nil {
		t.Fatal("second binding accepted")
	}
	copy := *b
	for _, invalid := range []*WorkerImageBindingPending{nil, {}, &copy} {
		if invalid.BindOriginal(t.Context()) == nil {
			t.Fatal("unissued binding accepted")
		}
	}
	if b.BindOriginal(t.Context()) == nil {
		t.Fatal("unprepared pin accepted")
	}
	if v := p.InspectOriginal(); v.PrepareAttempted || v.FilesRetained != 0 || v.NativeOutcome != "unknown" {
		t.Fatal(v)
	}
	if s := p.inner.source.InspectOriginal(); s.CaptureAttempted || s.RawDescriptorRetained {
		t.Fatal(s)
	}
}

func TestWorkerImagePendingAndReferencesRefuseCopiesBeforeEffects(t *testing.T) {
	p, r := workerPinMetadataOnly(t)
	copyPin := *p
	if copyPin.PrepareOriginal(t.Context()) == nil {
		t.Fatal("copied pin accepted")
	}
	if _, err := copyPin.InventoryOriginal(t.Context()); err == nil {
		t.Fatal("copied pin inventory accepted")
	}
	copyRefs := *r
	for _, invalid := range []*WorkerPinParentReferenceOwner{nil, {}, &copyRefs, r} {
		if invalid.RetireOriginal(t.Context(), &exec.Cmd{}) == nil {
			t.Fatal("unhanded references closed")
		}
	}
	if p.retirement != nil || p.inner.active != nil || p.inner.tried || len(p.inner.files) != 0 {
		t.Fatal("refusal changed native custody")
	}
	var other *WorkerResidentImagePinPending
	occupied := r
	if NewWorkerResidentImagePinForHomeWithParentReferences(&other, &occupied, "/synthetic-worker", strings.Repeat("a", 64), strings.Repeat("b", 64), 1, ResidentImagePinHomeData{Inode: 1}) == nil || other != nil || occupied != r {
		t.Fatal("occupied destination replaced")
	}
}
