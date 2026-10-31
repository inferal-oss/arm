package residentrefs

import (
	"context"
	"encoding/json"
	"testing"
)

// The fixed-prefix check is also used by actual slot registration. These are
// DATA sequences only: no original native operation or positive census is made.
func TestP1ChildControlPrefixDataRequiresAllFourRolesInOrder(t *testing.T) {
	want := []string{"p1-bootstrap-read", "p1-bound-ack-write", "p1-current-challenge-read", "p1-current-proof-write"}
	for next := range want {
		if !childControlPrefixData(want[:next], want[next]) {
			t.Fatal("valid DATA prefix refused", next)
		}
		for other := range want {
			if other != next && childControlPrefixData(want[:next], want[other]) {
				t.Fatal("skipped/replayed/reversed role accepted", next, other)
			}
		}
		for changed := 0; changed < next; changed++ {
			bad := append([]string(nil), want[:next]...)
			bad[changed] = want[(changed+1)%len(want)]
			if childControlPrefixData(bad, want[next]) {
				t.Fatal("foreign prior role accepted", next, changed)
			}
		}
	}
	if childControlPrefixData(want, want[0]) || childControlPrefixData(nil, "read") {
		t.Fatal("exhausted/generic role accepted")
	}
}

func TestP1ChildControlRefusesWithoutActualOriginalAndFiniteScope(t *testing.T) {
	original := inertChildData() // no process claim, materialization or native FD
	service, _, _, control, err := original.NewOriginalRecipients()
	if err != nil {
		t.Fatal(err)
	}
	copyControl := *control
	for _, c := range []*ControlOriginal{nil, {}, &copyControl, control} {
		if _, err := c.ReceiveOriginalData(context.Background()); err != ErrOriginal {
			t.Fatal(err)
		}
		if err := c.SendOriginalData(nil, nil); err != ErrOriginal {
			t.Fatal("generic send accepted", err)
		}
		if err := c.SendP1BoundAckOriginal(context.Background(), nil); err != ErrOriginal {
			t.Fatal(err)
		}
		var read *P1ChallengeRead
		if err := c.ReceiveP1CurrentChallengeOriginal(&read, context.Background()); err != ErrOriginal || read != nil {
			t.Fatal("refusal published a read", err)
		}
		if err := c.SendP1CurrentProofOriginal(context.Background(), nil); err != ErrOriginal {
			t.Fatal(err)
		}
	}
	read := &P1ChallengeRead{}
	read.self = read
	copyRead := *read
	for _, r := range []*P1ChallengeRead{nil, {}, read, &copyRead} {
		if err := service.ValidateOriginalChallengePhase(r); err != ErrOriginal {
			t.Fatal("unissued/copied read accepted", err)
		}
		if _, err := service.OriginalChallengePayloadData(r, make([]byte, 37)); err != ErrOriginal {
			t.Fatal("unissued read exposed payload", err)
		}
	}
	if original.state.active != nil || original.state.firstError != nil {
		t.Fatal("pre-effect refusal changed original operation state")
	}
	for _, slot := range control.state.controlPhases {
		if slot != nil {
			t.Fatal("pre-effect refusal consumed a native slot")
		}
	}
	for _, e := range original.state.entries {
		if e.file != nil || e.cloexecTried || e.materializeTried || e.closeTried {
			t.Fatal("refusal touched a native descriptor")
		}
	}
}

func TestP1ChallengeReadRejectsJSONValuePointerAndDecode(t *testing.T) {
	r := P1ChallengeRead{}
	for _, value := range []any{r, &r, struct{ Read P1ChallengeRead }{r}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("opaque read serialized")
		}
	}
	if err := json.Unmarshal([]byte(`{}`), &r); err != ErrOriginal || r.self != nil || r.call != nil || r.control != nil {
		t.Fatal("JSON constructed an original read", err)
	}
}
