package manager

import (
	"context"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
	"testing"
)

func TestWorkerCurrentManagerRequiresActualPreparedChargeAndChallenge(t *testing.T) {
	o, err := NewResidentWorkerManagerPending(&lifetimeview.WorkerManagerView{}, residentNegativeSettings(), &serviceview.WorkerManagerCharge{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.ValidateCurrentOriginal(context.Background(), &lifetimeview.WorkerManagerView{}, &serviceview.WorkerCurrentChallengeView{}, &serviceview.WorkerManagerCharge{}, &ResidentWorkerManagerServerBinding{}); err == nil {
		t.Fatal("unissued current Manager accepted")
	}
	if _, err = o.OriginalCurrentData(nil, nil, nil, nil); err == nil {
		t.Fatal("unissued current result accepted")
	}
	if o.currentTried || o.currentValidation != nil || o.active != nil || o.database != nil || o.started {
		t.Fatal("refusal entered original native work")
	}
}

func TestWorkerCurrentManagerContextComparisonRejectsUncomparableData(t *testing.T) {
	type foreignContext struct {
		context.Context
		bytes []byte
	}
	root := context.Background()
	foreign := foreignContext{root, []byte{1}}
	if sameWorkerCurrentManagerContextData(foreign, root) || sameWorkerCurrentManagerContextData(root, foreign) || sameWorkerCurrentManagerContextData(nil, root) {
		t.Fatal("uncomparable context accepted")
	}
}
