package database

import (
	"context"
	"testing"

	"getarm.org/internal/server/challengeview"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

func TestCurrentDatabaseRejectsPreparedFlagsAndUnissuedBindingsBeforeEffects(t *testing.T) {
	claim := &lifetimeview.ManagerClaim{}
	o, err := NewResidentOpen(claim) // pure pending only, no pool/Connect/native claim
	if err != nil {
		t.Fatal(err)
	}
	// Inert flag/returned DATA is deliberately not an original prepared call.
	o.preparedTried = true
	o.preparedValidation = &residentPreparedDatabaseCall{owner: o, returned: true}
	prepared := o.preparedValidation
	copyOwner := &ResidentOpen{self: o} // foreign original identity, no mutex copy
	view, charge := &challengeview.CurrentChallengeView{}, &serviceview.ManagerCharge{}
	for _, owner := range []*ResidentOpen{nil, {}, copyOwner, o} {
		if data, err := owner.ValidateCurrentOriginal(context.Background(), &ResidentDatabaseBinding{}, claim, nil, view, charge); err != errResidentDatabaseOriginal || data != (ResidentCurrentDatabaseData{}) {
			t.Fatal("unissued current binding accepted", err)
		}
		if data, err := owner.CurrentOriginalData(&ResidentDatabaseBinding{}, claim, nil, view, charge); err != errResidentDatabaseOriginal || data != (ResidentCurrentDatabaseData{}) {
			t.Fatal("prepared DATA projected as current", err)
		}
	}
	if o.currentTried || o.currentValidation != nil || o.active != nil || o.firstError != nil || o.openTried || o.closeTried || o.connect != nil || o.pool != nil || !o.preparedTried || o.preparedValidation != prepared {
		t.Fatal("refusal reset prepared state or changed original custody")
	}
	for _, file := range o.files.slots {
		if file != nil {
			t.Fatal("refusal opened a native file")
		}
	}
}
