package manager

import (
	"context"
	"testing"

	"getarm.org/internal/server/challengeview"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

func TestCurrentManagerAndBorrowerRejectPreparedFlagsWithoutOriginalCurrentCall(t *testing.T) {
	o := &ResidentManagerPending{}
	o.self = o
	o.preparedTried = true
	o.preparedValidation = &residentManagerPreparedCall{owner: o, returned: true}
	prepared := o.preparedValidation
	copyOwner := &ResidentManagerPending{self: o} // foreign self; do not copy mutex
	native, challenge, charge := &lifetimeview.ManagerView{}, &challengeview.CurrentChallengeView{}, &serviceview.ManagerCharge{}
	borrower := &ResidentManagerServerBinding{owner: o}
	copyBorrower := *borrower
	for _, owner := range []*ResidentManagerPending{nil, {}, copyOwner, o} {
		if data, err := owner.ValidateCurrentOriginal(context.Background(), native, challenge, charge, borrower); err != errResidentManagerOriginal || data != (ResidentCurrentManagerData{}) {
			t.Fatal("unprepared current Manager accepted", err)
		}
		if data, err := owner.OriginalCurrentData(native, challenge, charge, borrower); err != errResidentManagerOriginal || data != (ResidentCurrentManagerData{}) {
			t.Fatal("prepared Manager DATA projected as current", err)
		}
	}
	for _, binding := range []*ResidentManagerServerBinding{nil, {}, borrower, &copyBorrower} {
		if data, err := binding.CurrentOriginalData(challenge, charge, &lifetimeview.ServerClaim{}); err != errResidentManagerOriginal || data != (ResidentCurrentManagerData{}) {
			t.Fatal("unissued/copied current borrower accepted", err)
		}
	}
	if o.currentTried || o.currentValidation != nil || o.active != nil || o.firstError != nil || o.openTried || o.startTried || o.started || o.database != nil || o.schemaTx != nil || !o.preparedTried || o.preparedValidation != prepared {
		t.Fatal("refusal changed prepared/Manager/SQL lifecycle")
	}
}
