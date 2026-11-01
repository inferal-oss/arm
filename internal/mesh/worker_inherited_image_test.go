package mesh

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestWorkerInheritedImageRefusesBeforePublishingOrClaiming(t *testing.T) {
	ctx := context.Background()
	var destination *WorkerInheritedImagePending
	if NewWorkerInheritedImagePending(nil, ctx, &residentrefs.ImageOriginal{}) == nil {
		t.Fatal("nil destination accepted")
	}
	for _, source := range []*residentrefs.ImageOriginal{nil, {}} {
		if NewWorkerInheritedImagePending(&destination, ctx, source) == nil || destination != nil {
			t.Fatal("unissued image published pending custody")
		}
	}
	occupied := &WorkerInheritedImagePending{}
	destination = occupied
	if NewWorkerInheritedImagePending(&destination, ctx, &residentrefs.ImageOriginal{}) == nil || destination != occupied {
		t.Fatal("occupied destination replaced")
	}
}

func TestWorkerRuntimeViewRejectsMetadataCopiesAndOldRuntime(t *testing.T) {
	var nilPending *WorkerInheritedImagePending
	zero := &WorkerInheritedImagePending{}
	original := &WorkerInheritedImagePending{}
	original.self = original
	copyPending := *original
	for _, p := range []*WorkerInheritedImagePending{nilPending, zero, &copyPending} {
		if p.VerifyOriginal(context.Background(), ResidentImageInventory{}) == nil {
			t.Fatal("unissued pending verified")
		}
		if v, err := p.RuntimeViewOriginal(); err == nil || v != nil {
			t.Fatal("unissued pending produced a view")
		}
	}
	// Even an old runtime pointer with matching private metadata cannot create
	// a worker view backed by the real worker-scoped fixed13 census.
	p := &WorkerInheritedImagePending{custody: &residentrefs.ImageOriginal{}}
	p.self = p
	inner := &InheritedImagePending{}
	inner.state = &inheritedImageState{self: inner, rootContext: context.Background()}
	p.inner = inner
	v := &WorkerRuntimeView{owner: p, image: &RuntimeExecutable{inherited: inner}}
	v.self, p.view = v, v
	copyView := *v
	for _, view := range []*WorkerRuntimeView{nil, {}, v, &copyView} {
		if _, err := view.ProofOriginal(context.Background(), &residentrefs.ServiceOriginal{}, "/synthetic/home", "synthetic"); err == nil {
			t.Fatal("metadata or old runtime issued a worker proof")
		}
	}
	if inner.state.verified || inner.state.active != nil || inner.state.image != nil {
		t.Fatal("refusal reached native image verification")
	}
}
