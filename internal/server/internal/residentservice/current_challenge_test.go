package residentservice

import (
	"context"
	"encoding/json"
	"testing"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

func TestP1ParentControlPrefixDataRequiresAllFourRolesInOrder(t *testing.T) {
	want := []string{"p1-transfer", "p1-bound-ack-read", "p1-current-challenge-write", "p1-current-proof-read"}
	for next := range want {
		if !parentControlPrefixData(want[:next], want[next]) {
			t.Fatal("valid DATA prefix refused", next)
		}
		for other := range want {
			if other != next && parentControlPrefixData(want[:next], want[other]) {
				t.Fatal("skipped/replayed/reversed role accepted", next, other)
			}
		}
		for changed := 0; changed < next; changed++ {
			bad := append([]string(nil), want[:next]...)
			bad[changed] = want[(changed+1)%len(want)]
			if parentControlPrefixData(bad, want[next]) {
				t.Fatal("foreign prior role accepted", next, changed)
			}
		}
	}
	if parentControlPrefixData(want, want[0]) || parentControlPrefixData(nil, "write") {
		t.Fatal("exhausted/generic role accepted")
	}
}

func TestP1ChallengeRequiresActualCompletedRootBoundBeforeSecretUse(t *testing.T) {
	c := &ChildPending{}
	o := &owner{child: c}
	c.owner = o // inert pending identity only; no grant, birth, prepared or RootBound
	copyChild := *c
	for _, child := range []*ChildPending{nil, {}, {owner: &owner{}}, &copyChild, c} {
		if frame, err := child.SignP1BoundAckOriginal(context.Background(), residentprotocol.ContextData{}); err != ErrInvalid || frame != nil {
			t.Fatal("unprepared signer accepted", err)
		}
		var view *CurrentChallengeView
		if err := child.AuthenticateP1CurrentChallengeOriginal(&view, context.Background(), &residentrefs.P1ChallengeRead{}, &ManagerCharge{}); err != ErrInvalid || view != nil {
			t.Fatal("unprepared challenge published a view", err)
		}
	}
	if o.active != nil || o.p1AckSignTried || o.p1ChallengeAuthTried || o.p1AckSign != nil || o.p1ChallengeAuth != nil || o.p1ChallengeView != nil || o.firstError != nil || len(o.reads) != 0 || o.append != nil {
		t.Fatal("pre-effect refusal changed signing/read/journal state")
	}
	v := &CurrentChallengeView{owner: o}
	v.self = v
	copyView := *v
	for _, view := range []*CurrentChallengeView{nil, {}, v, &copyView} {
		if data, err := view.ValidateOriginal(&ManagerCharge{}); err != ErrInvalid || data != (residentprotocol.CurrentChallengeBodyData{}) {
			t.Fatal("unissued/copied view accepted", err)
		}
	}
}

func TestP1ParentControlRefusesWithoutActualGroupAndFiniteScope(t *testing.T) {
	p := &P1ServiceGroup{p1ServiceState: &p1ServiceState{}}
	p.self = p
	copyGroup := *p
	for _, group := range []*P1ServiceGroup{nil, {}, &copyGroup, p} {
		var ack, proof *P1ParentRead
		if err := group.ReceiveP1BoundAckOriginal(&ack, context.Background(), nil); err != ErrInvalid || ack != nil {
			t.Fatal("unprepared group published Ack read", err)
		}
		if err := group.SendP1CurrentChallengeOriginal(context.Background(), nil, nil); err != ErrInvalid {
			t.Fatal(err)
		}
		if err := group.ReceiveP1CurrentProofOriginal(&proof, context.Background(), nil); err != ErrInvalid || proof != nil {
			t.Fatal("unprepared group published proof read", err)
		}
	}
	for _, call := range p.controlPhases {
		if call != nil {
			t.Fatal("refusal registered endpoint IO")
		}
	}
	r := &P1ParentRead{}
	r.self = r
	copyRead := *r
	for _, read := range []*P1ParentRead{nil, {}, r, &copyRead} {
		if data, err := read.BoundAckPayloadOriginalData(); err != ErrInvalid || data != nil {
			t.Fatal("unissued read exposed Ack", err)
		}
		if data, err := read.CurrentProofPayloadOriginalData(); err != ErrInvalid || data != nil {
			t.Fatal("unissued read exposed proof", err)
		}
	}
}

func TestP1ChallengeAndParentReadRejectJSONAuthority(t *testing.T) {
	v, r := CurrentChallengeView{}, P1ParentRead{}
	for _, value := range []any{v, &v, r, &r, struct{ View CurrentChallengeView }{v}, struct{ Read P1ParentRead }{r}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("opaque phase/read serialized")
		}
	}
	if err := json.Unmarshal([]byte(`{}`), &v); err != ErrInvalid || v.self != nil || v.issue != nil || v.owner != nil {
		t.Fatal("JSON constructed phase authority", err)
	}
	if err := json.Unmarshal([]byte(`{}`), &r); err != ErrInvalid || r.self != nil || r.call != nil || r.group != nil {
		t.Fatal("JSON constructed original read", err)
	}
}

// Independently computed SHA-256 DATA vectors are repeated in residentbirth's
// separate test package. No production service -> birth dependency is added.
func TestP1OriginalKeySHADataConformsToFooterDomain(t *testing.T) {
	for key, want := range map[string]string{
		"synthetic-data-key":  "0b2f0003e4ccb3559d9c657e9bf68832ac9e2263bd021b1aba150a2be0ffa680",
		"synthetic-data-key ": "58932bf26ffc7bbe8cc9906320d948ab4e8440d549ada8437a5d6667ec344070",
		"synthetic-κ\x01":     "f362412b990897710ac4836a17ff8f19075fcea236eeac86fadec7b14e77cce3",
	} {
		if got := p1OriginalKeySHAData(key); got != want {
			t.Fatal("key domain/raw UTF-8 DATA changed", got)
		}
	}
}
