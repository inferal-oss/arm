package residentservice

import (
	"context"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

func TestP1PreparedServiceRequiresActualReceivedBirthAndOwners(t *testing.T) {
	for _, child := range []*ChildPending{nil, {}, {owner: &owner{}}} {
		if _, err := child.ValidateP1PreparedOriginal(context.Background(), &residentrefs.ServiceOriginal{}, &mesh.RuntimeExecutable{}, &ManagerCharge{}); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	child := &ChildPending{}
	o := &owner{child: child}
	child.owner = o
	if _, err := child.ValidateP1PreparedOriginal(context.Background(), nil, nil, nil); err != ErrInvalid {
		t.Fatal(err)
	}
	if o.active != nil || o.p1PreparedTried || o.p1PreparedValidation != nil || len(o.reads) != 0 || o.receiveTried || o.runtimeBound {
		t.Fatal("refusal spent a journal/native/runtime operation")
	}
}
