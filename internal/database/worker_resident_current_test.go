package database

import (
	"context"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
	"testing"
)

func TestWorkerCurrentDatabaseRejectsPreparedFlagsBeforeEffects(t *testing.T) {
	o, err := NewWorkerResidentOpen(&lifetimeview.WorkerManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []*WorkerResidentOpen{nil, {}, o} {
		if _, err = owner.ValidateCurrentOriginal(context.Background(), nil, nil, nil, &serviceview.WorkerCurrentChallengeView{}, &serviceview.WorkerManagerCharge{}); err == nil {
			t.Fatal("unissued current DB accepted")
		}
		if _, err = owner.CurrentOriginalData(nil, nil, nil, &serviceview.WorkerCurrentChallengeView{}, &serviceview.WorkerManagerCharge{}); err == nil {
			t.Fatal("DATA supplied a current DB")
		}
	}
	if o.active != nil || o.currentTried || o.currentValidation != nil || o.pool != nil || o.connect != nil || o.files.opened != 0 {
		t.Fatal("refusal entered native/SQL work")
	}
}
