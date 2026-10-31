package residentservice

import (
	"context"
	"testing"
)

// A comparable TYPE can still panic on equality when an interface field holds
// an incomparable dynamic value. The helper must inspect actual VALUES without
// invoking any context method or constructing a timer/positive holder.
func TestP1CurrentProofContextIdentityRefusesNestedIncomparableValues(t *testing.T) {
	type embeddedContext struct {
		context.Context
		extra any
	}
	base := context.Background()
	good := embeddedContext{Context: base, extra: "same"}
	other := embeddedContext{Context: base, extra: "different"}
	slice := embeddedContext{Context: base, extra: []byte{1}}
	mapping := embeddedContext{Context: base, extra: map[string]int{"key": 1}}
	for _, pair := range []struct{ a, b context.Context }{{nil, base}, {base, nil}, {nil, nil}, {good, other}, {slice, slice}, {good, slice}, {slice, good}, {mapping, mapping}, {good, mapping}, {mapping, good}} {
		if sameP1ProofContextData(pair.a, pair.b) {
			t.Fatal("nil/foreign/nested incomparable context accepted")
		}
	}
	if !sameP1ProofContextData(base, base) || !sameP1ProofContextData(good, good) {
		t.Fatal("same comparable context DATA refused")
	}
}

func TestP1CurrentProofSignerRefusesUnissuedCurrentAndCopiedLineage(t *testing.T) {
	child := &ChildPending{}
	o := &owner{child: child}
	child.owner = o
	charge := &ManagerCharge{owner: o, child: child}
	o.charge = charge
	view := &CurrentChallengeView{owner: o, charge: charge}
	view.self = view
	o.p1ChallengeView = view
	observation := &P1CurrentServiceObservation{owner: o, child: child, view: view, charge: charge}
	observation.self = observation
	o.p1CurrentObservation = observation
	// These are inert adversarial flags, not successful prepared/auth/current
	// originals. No original grant, custody, secret or census is constructed.
	current := &p1CurrentServiceCall{owner: o, ctx: context.Background(), view: view, charge: charge, observation: observation, normalReturned: true, op: &operation{owner: o, kind: "p1-current-service", normalFinished: true}}
	observation.call, o.p1Current, o.p1CurrentTried = current, current, true
	copyChild, copyObservation, copyView, copyCharge := *child, *observation, *view, *charge // no mutex in wrappers
	for _, c := range []*ChildPending{nil, {}, &copyChild, child} {
		if frame, err := c.SignP1CurrentProofOriginal(context.Background(), observation, view, charge); err != ErrInvalid || frame != nil {
			t.Fatal("unissued current lineage signed", err)
		}
	}
	for _, input := range []struct {
		observation *P1CurrentServiceObservation
		view        *CurrentChallengeView
		charge      *ManagerCharge
	}{{nil, view, charge}, {&copyObservation, view, charge}, {observation, &copyView, charge}, {observation, view, &copyCharge}, {&P1CurrentServiceObservation{}, view, charge}, {observation, &CurrentChallengeView{}, charge}, {observation, view, &ManagerCharge{}}} {
		if frame, err := child.SignP1CurrentProofOriginal(context.Background(), input.observation, input.view, input.charge); err != ErrInvalid || frame != nil {
			t.Fatal("copied/foreign lineage signed", err)
		}
	}
	if frame, err := child.SignP1CurrentProofOriginal(nil, observation, view, charge); err != ErrInvalid || frame != nil {
		t.Fatal("nil lifetime signed", err)
	}
	if o.active != nil || o.firstError != nil || o.p1CurrentProofSignTried || o.p1CurrentProofSign != nil || o.p1AckSign != nil || o.p1ChallengeAuth != nil || o.p1RootBound != nil || o.p1PreparedValidation != nil || len(o.reads) != 0 || o.append != nil {
		t.Fatal("refusal changed original state or registered signing")
	}
	active := &operation{owner: o, kind: "p1-current-service"}
	o.active = active
	if frame, err := child.SignP1CurrentProofOriginal(context.Background(), observation, view, charge); err != ErrInvalid || frame != nil || o.active != active || o.p1CurrentProofSignTried {
		t.Fatal("active original cleared or signed", err)
	}
}

func TestP1CurrentProofPrivateGatePreservesPublicActiveRefusals(t *testing.T) {
	child := &ChildPending{}
	o := &owner{child: child}
	child.owner = o
	charge := &ManagerCharge{owner: o, child: child}
	o.charge = charge
	view := &CurrentChallengeView{owner: o, charge: charge}
	view.self = view
	observation := &P1CurrentServiceObservation{owner: o, child: child, view: view, charge: charge}
	observation.self = observation
	op := &operation{owner: o, kind: "p1-current-proof-sign"}
	call := &p1CurrentProofSignCall{owner: o, op: op, ctx: context.Background(), child: child, view: view, charge: charge, observation: observation}
	o.p1CurrentProofSign, o.p1CurrentProofSignTried, o.active = call, true, op
	// An active-kind name and returned/prepared flags cannot issue a proof call
	// or relax the old public predicates. No census or encoder can be reached.
	if _, err := view.ValidateOriginal(charge); err != ErrInvalid {
		t.Fatal("public active view refusal relaxed", err)
	}
	if o.completedCurrentObservationLocked(observation, child, view, charge) {
		t.Fatal("public current active refusal relaxed")
	}
	copyCall := *call // fixed records/pointers only, no mutex
	for _, candidate := range []*p1CurrentProofSignCall{nil, {}, &copyCall, call} {
		if o.currentProofSignCallLocked(candidate) {
			t.Fatal("unissued/copied active call accepted")
		}
		if err := o.checkP1CurrentProofPhase(candidate); err != ErrInvalid {
			t.Fatal("unissued phase reached census", err)
		}
	}
	if o.active != op || call.phaseCalls != 0 || call.payloadAttempted || call.frameAttempted || call.payload != nil || call.frame != nil || call.output != nil || o.firstError != nil || o.p1Current != nil {
		t.Fatal("refusal mutated active/census/signing state")
	}
}
