package server

import (
	"context"
	"testing"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/challengeview"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

func TestCurrentServerRejectsPreparedFlagsWithoutActualCurrentManagerBorrower(t *testing.T) {
	native := &lifetimeview.ServerView{}
	o, err := newResidentServerPending(native, &manager.ResidentManagerPending{}) // pure holder only
	if err != nil {
		t.Fatal(err)
	}
	o.preparedTried = true
	o.preparedValidation = &residentServerPreparedCall{owner: o, returned: true}
	prepared := o.preparedValidation
	copyOwner := &residentServerPending{self: o} // foreign self, no mutex copy
	challenge, charge := &challengeview.CurrentChallengeView{}, &serviceview.ManagerCharge{}
	for _, owner := range []*residentServerPending{nil, {}, copyOwner, o} {
		if data, err := owner.validateCurrentOriginal(context.Background(), native, challenge, charge); err != errResidentServerOriginal || data != (residentCurrentServerData{}) {
			t.Fatal("unprepared current Server accepted", err)
		}
		if data, err := owner.currentOriginalData(native, challenge, charge); err != errResidentServerOriginal || data != (residentCurrentServerData{}) {
			t.Fatal("prepared Server DATA projected as current", err)
		}
	}
	if o.currentTried || o.currentValidation != nil || o.active != nil || o.firstError != nil || o.openTried || o.opened || o.startTried || o.started || o.claim != nil || o.managerBound != nil || o.binding != nil || o.server.Manager != nil || !o.preparedTried || o.preparedValidation != prepared {
		t.Fatal("refusal changed prepared/Server/borrower lifecycle")
	}
}
