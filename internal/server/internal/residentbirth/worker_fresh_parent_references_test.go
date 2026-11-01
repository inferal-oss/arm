package residentbirth

import (
	"context"
	"testing"
)

func TestWorkerFenceParentReferenceOwnerRefusesUnissuedCopiedBeforeEffects(t *testing.T) {
	f := &WorkerFreshFencePending{}
	o := &workerFenceOwner{self: f}
	f.owner = o
	r := &WorkerFenceParentReferenceOwner{fence: f}
	r.self = r
	o.parentReferences = r
	copied := *r
	for name, owner := range map[string]*WorkerFenceParentReferenceOwner{"nil": nil, "zero": {}, "copied": &copied, "not-launched": r} {
		t.Run(name, func(t *testing.T) {
			if err := owner.RetireOriginal(context.Background(), nil, nil); err != errOriginalWorkerFence {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if o.active != nil || o.parentRetirement != nil || o.firstError != nil {
		t.Fatal("refusal changed original owner")
	}
}

func TestWorkerFenceParentRetirementPermanentlySealsOrdinaryEntry(t *testing.T) {
	f := &WorkerFreshFencePending{}
	o := &workerFenceOwner{self: f, parentRetirement: &workerFenceParentRetirement{}}
	f.owner = o
	if op, err := f.begin(context.Background(), "prepare", nil); op != nil || err != errOriginalWorkerFence {
		t.Fatalf("sealed entry = %v, %v", op, err)
	}
	if o.active != nil || o.prepareTried {
		t.Fatal("sealed owner reopened")
	}
}
