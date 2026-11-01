package residentservice

import (
	"context"
	"encoding/json"
	"testing"

	"getarm.org/internal/residentcost"
)

// Pure refusal controls only; no native input, actual launcher claim, scheduler
// setter, grant/debit/image/child/SQL or resource authority is constructed.
func TestWorkerStartupFundingIncompleteLineagesRefuseBeforeReadData(t *testing.T) {
	for _, p := range []*WorkerStartupFundingPending{nil, {}, {ctx: context.Background()}, {ctx: context.Background(), owner: &workerCustody{}}} {
		if p.completedLocked() {
			t.Fatal("incomplete original funding became complete")
		}
		if err := p.CheckOriginal(context.Background(), residentcost.PendingSourceContractData{}); err == nil {
			t.Fatal("unissued funding accepted")
		}
		if _, err := p.ClaimOriginalServeFunding(); err == nil {
			t.Fatal("unissued funding issued serve view")
		}
	}
	var p *WorkerStartupFundingPending
	if err := NewWorkerStartupFundingPending(&p, nil, nil, nil, nil); err == nil || p != nil {
		t.Fatal("missing original child accepted")
	}
	if err := NewWorkerParentStartupFundingPending(&p, nil, nil, context.Background()); err == nil || p != nil {
		t.Fatal("missing original parent accepted")
	}
	for _, reader := range []*workerFundingInputRead{nil, {}, {owner: &WorkerStartupFundingPending{}}, {reservation: &workerStartupVerifierReservation{}}} {
		if err := reader.check(context.Background()); err == nil {
			t.Fatal("unfunded reader accepted")
		}
	}
}

func TestWorkerServeFundingViewCannotBeCopiedSerializedOrForgedData(t *testing.T) {
	v := &WorkerStartupFundingView{}
	v.self = v
	var binding *WorkerServeFundingBinding
	if err := v.ClaimOriginalServeBinding(&binding, &WorkerServeBirthView{}); err == nil || binding != nil {
		t.Fatal("unissued view bound native birth")
	}
	copy := *v
	if err := copy.ClaimOriginalServeBinding(&binding, &WorkerServeBirthView{}); err == nil {
		t.Fatal("copied view accepted")
	}
	for _, value := range []any{&WorkerStartupFundingPending{}, v, &WorkerServeFundingBinding{}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("funding capability serialized")
		}
		if err := json.Unmarshal([]byte(`{}`), value); err == nil {
			t.Fatal("funding capability reconstructed")
		}
	}
	for _, b := range []*WorkerServeFundingBinding{nil, {}, {view: v}} {
		if err := b.RequireOriginalBirth(&WorkerServeBirthView{}); err == nil {
			t.Fatal("unissued binding accepted")
		}
	}
}

func TestWorkerPendingSourceFloorMatchesEntireBoundProspectiveContractData(t *testing.T) {
	profile, _ := workerEvidenceData(t)
	actual, err := profile.Contract.LogicalFloorData()
	if err != nil {
		t.Fatal(err)
	}
	got := WorkerPendingProspectiveFloorData()
	if got != (residentcost.BoundsData{Tasks: uint64(actual.Tasks), Descriptors: uint64(actual.Descriptors), Children: uint64(actual.Children), Sockets: uint64(actual.Sockets), NativePending: uint64(actual.NativePending), IOBytes: actual.IOBytes, RetainedReceiptBytes: actual.RetainedReceiptBytes}) {
		t.Fatal("compiled prospective source floor diverged", got, actual)
	}
}

func TestWorkerOperatorMissingSourceRetainsFailureWithoutNativeEffectsData(t *testing.T) {
	p := workerOperatorRefusalData(t)
	if _, err := p.PlanOriginal(context.Background()); err == nil {
		t.Fatal("missing compiled source accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
	if !p.planTried || p.planComplete || p.ObserveOriginal().FirstError == nil || len(p.reads) != 0 {
		t.Fatal("missing-source failure not retained")
	}
	if p.BindCompiledPendingSourceData(residentcost.PendingSourceContractData{}) == nil {
		t.Fatal("failed owner rebound")
	}
}

func TestWorkerParentMissingLauncherRetainsOriginalFailureBeforeEffectsData(t *testing.T) {
	p := workerParentRefusalData(t)
	if err := p.OpenOriginal(context.Background()); err == nil {
		t.Fatal("missing actual launcher accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
	if !p.openTried || p.opened || p.structural != nil || p.custody.image != nil || len(p.custody.reads) != 0 || p.ObserveOriginal().FirstError == nil {
		t.Fatal("missing-launcher attempt lost or reached scheduler/image/native effects")
	}
	if err := p.OpenOriginal(context.Background()); err == nil {
		t.Fatal("failed original parent reused")
	}
}
