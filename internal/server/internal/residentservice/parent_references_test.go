package residentservice

import (
	"context"
	"testing"
)

func TestServiceParentReferenceOwnerRefusesUnissuedCopiedBeforeEffects(t *testing.T) {
	p, r, err := NewP1ExistingGrantParentWithReferences("/fixture")
	if err != nil {
		t.Fatal(err)
	}
	copied := *r
	for name, owner := range map[string]*ServiceParentReferenceOwner{"nil": nil, "zero": {}, "copied": &copied, "not-launched": r} {
		t.Run(name, func(t *testing.T) {
			if err := owner.RetireServiceOriginal(context.Background(), nil); err != ErrInvalid {
				t.Fatalf("service refusal = %v", err)
			}
			if err := owner.RetireControlOriginal(context.Background(), nil); err != ErrInvalid {
				t.Fatalf("control refusal = %v", err)
			}
		})
	}
	if p.owner.active != nil || p.owner.parentRetirement != nil || p.owner.firstError != nil || p.owner.files.opened != 0 {
		t.Fatal("refusal changed original owner")
	}
}

func TestServiceParentRetirementPermanentlySealsOrdinaryEntry(t *testing.T) {
	p, r, err := NewP1ExistingGrantParentWithReferences("/fixture")
	if err != nil {
		t.Fatal(err)
	}
	c := &serviceParentRetirement{owner: r}
	p.owner.parentRetirement = c
	if op, err := p.begin("open"); op != nil || err != ErrInvalid {
		t.Fatalf("sealed entry = %v, %v", op, err)
	}
	if p.owner.active != nil || p.owner.openTried || p.owner.parentRetirement != c {
		t.Fatal("sealed owner reopened")
	}
}
