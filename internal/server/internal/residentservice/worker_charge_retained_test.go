package residentservice

import (
	"testing"
)

func TestWorkerRetainedChargeDataRefusesUnissuedSourcesWithoutEffects(t *testing.T) {
	for _, claim := range []*WorkerManagerCharge{nil, {}, {owner: newWorkerCustodyData("/synthetic")}} {
		if _, err := claim.ObserveRetainedOriginalData(); err == nil {
			t.Fatal("DATA issued retained charge")
		}
	}
	p := workerParentRefusalData(t)
	claim := &WorkerManagerCharge{owner: p.custody}
	claim.self = claim
	if _, err := claim.ObserveRetainedOriginalData(); err == nil {
		t.Fatal("parent source entered child charge")
	}
	copy := *claim
	if _, err := copy.ObserveRetainedOriginalData(); err == nil {
		t.Fatal("copied charge accepted")
	}
	foreign := &WorkerManagerCharge{owner: newWorkerCustodyData("/foreign"), child: &WorkerChildPending{}}
	foreign.self = foreign
	if _, err := foreign.ObserveRetainedOriginalData(); err == nil {
		t.Fatal("foreign child accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
}
