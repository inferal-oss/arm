package mesh

import (
	"context"
	"strings"
	"testing"
)

func TestPinParentReferenceOwnerRefusesUnissuedCopiedBeforeEffects(t *testing.T) {
	p, r, err := NewResidentImagePinForHomeWithParentReferences("/fixture", strings.Repeat("a", 64), strings.Repeat("b", 64), 1, ResidentImagePinHomeData{Inode: 1})
	if err != nil {
		t.Fatal(err)
	}
	copied := *r
	for name, owner := range map[string]*PinParentReferenceOwner{"nil": nil, "zero": {}, "copied": &copied, "not-launched": r} {
		t.Run(name, func(t *testing.T) {
			if err := owner.RetireOriginal(context.Background(), nil); err != errResidentPinOriginal {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if p.active != nil || p.parentRetirement != nil || p.firstError != nil || len(p.files) != 0 {
		t.Fatal("refusal changed original owner")
	}
}

func TestPinParentRetirementPermanentlySealsOrdinaryEntries(t *testing.T) {
	p, r, err := NewResidentImagePinForHomeWithParentReferences("/fixture", strings.Repeat("a", 64), strings.Repeat("b", 64), 1, ResidentImagePinHomeData{Inode: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.parentRetirement = &pinParentRetirement{owner: r}
	if err := p.PrepareOriginal(context.Background()); err != errResidentPinOriginal {
		t.Fatalf("prepare refusal = %v", err)
	}
	if _, err := p.InventoryOriginal(context.Background()); err != errResidentPinOriginal {
		t.Fatalf("inventory refusal = %v", err)
	}
	b := &ResidentImageP1Pending{&residentImageP1State{pin: p, parentRetired: true}}
	b.self = b
	if op, err := b.begin("bind"); op != nil || err != errResidentImageP1Original {
		t.Fatalf("image refusal = %v, %v", op, err)
	}
	if p.active != nil || p.tried || p.p1InventoryTried || b.active != nil || b.tried {
		t.Fatal("sealed owner reopened")
	}
}
