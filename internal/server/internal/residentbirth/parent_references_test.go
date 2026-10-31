package residentbirth

import (
	"context"
	"testing"
)

func TestFenceParentReferenceOwnerRefusesUnissuedCopiedBeforeEffects(t *testing.T) {
	f := &FreshFence{}
	o := &fenceOwner{self: f}
	f.owner = o
	r := &FenceParentReferenceOwner{fence: f}
	r.self = r
	o.parentReferences = r
	copied := *r
	for name, owner := range map[string]*FenceParentReferenceOwner{"nil": nil, "zero": {}, "copied": &copied, "not-launched": r} {
		t.Run(name, func(t *testing.T) {
			if err := owner.RetireOriginal(context.Background(), nil); err != errOriginalFence {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if o.active != nil || o.parentRetirement != nil || o.firstError != nil {
		t.Fatal("refusal changed original owner")
	}
}

func TestFenceParentRetirementPermanentlySealsOrdinaryEntry(t *testing.T) {
	f := &FreshFence{}
	o := &fenceOwner{self: f, parentRetirement: &fenceParentRetirement{}}
	f.owner = o
	if op, err := f.begin(context.Background(), "prepare", nil); op != nil || err != errOriginalFence {
		t.Fatalf("sealed entry = %v, %v", op, err)
	}
	if o.active != nil || o.prepareTried {
		t.Fatal("sealed owner reopened")
	}
}
