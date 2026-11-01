package residentservice

import (
	"math"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestRuntimeBoundsDataEachDimensionAndUnsignedLimits(t *testing.T) {
	required := Bounds{Tasks: 2, Descriptors: 3, Children: 4, Sockets: 5, NativePending: 6, IOBytes: 7, RetainedReceiptBytes: 8}
	if err := runtimeBoundsSufficientData(required, required); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range []string{"tasks", "descriptors", "children", "sockets", "native-pending", "IO", "receipts"} {
		t.Run(dimension, func(t *testing.T) {
			available := required
			switch dimension {
			case "tasks":
				available.Tasks--
			case "descriptors":
				available.Descriptors--
			case "children":
				available.Children--
			case "sockets":
				available.Sockets--
			case "native-pending":
				available.NativePending--
			case "IO":
				available.IOBytes--
			case "receipts":
				available.RetainedReceiptBytes--
			}
			if err := runtimeBoundsSufficientData(available, required); err != ErrInvalid {
				t.Fatalf("insufficient data = %v", err)
			}
		})
	}
	maximum := Bounds{Tasks: math.MaxUint32, Descriptors: math.MaxUint32, Children: math.MaxUint32, Sockets: math.MaxUint32, NativePending: math.MaxUint32, IOBytes: math.MaxUint64, RetainedReceiptBytes: math.MaxUint64}
	if err := runtimeBoundsSufficientData(maximum, maximum); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeBudgetOriginalChargeRefusesCopiedAndUnavailableBeforeEffects(t *testing.T) {
	o := &owner{grant: &GrantRecord{}, debit: &OriginalDebit{record: &DebitRecord{}}, p1Child: &residentrefs.ServiceOriginal{}}
	child := &ChildPending{owner: o}
	charge := &ManagerCharge{owner: o, child: child}
	o.child, o.charge = child, charge
	copied := *charge
	for name, c := range map[string]*ManagerCharge{"nil": nil, "zero": {}, "copied": &copied} {
		t.Run(name, func(t *testing.T) {
			if err := c.CheckOriginalRuntimeBounds(Bounds{}); err != ErrInvalid {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if err := charge.CheckOriginalRuntimeBounds(Bounds{}); err != ErrUnresolved {
		t.Fatalf("unavailable original = %v", err)
	}
	if o.active != nil || o.firstError != nil || o.runtimeBound || o.prepared {
		t.Fatal("pure refusal changed original state")
	}
}
