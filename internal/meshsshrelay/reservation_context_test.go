package meshsshrelay

import (
	"context"
	"errors"
	"testing"
)

func TestReservationChecksRetainParentAndRecheckAfterDelay(t *testing.T) {
	expired := false
	parentCalls, childCalls := 0, 0
	parent, err := withReservation(t.Context(), func(context.Context) error {
		parentCalls++
		if expired {
			return errors.New("expired original hold")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := withReservation(parent, func(context.Context) error { childCalls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckReservation(child); err != nil || parentCalls != 3 || childCalls != 2 {
		t.Fatal(parentCalls, childCalls, err)
	}
	expired = true // context remains live, as after a suspended Go timer
	if err = CheckReservation(child); err == nil || childCalls != 2 {
		t.Fatal("expired parent lent its authority", childCalls, err)
	}
	if _, err = withReservation(t.Context(), nil); err == nil {
		t.Fatal("missing holder check accepted")
	}
}
