package manager

import (
	"context"
	"errors"
	"testing"
)

func TestSyncScopeGateWriterVerifyWriterHandoff(t *testing.T) {
	g := newSyncScopeGate()
	ctx := t.Context()
	first, err := g.beginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.tryVerify(ctx); !errors.Is(err, errSyncScopeGateBusy) || v != nil {
		t.Fatalf("verifier overlapped writer: %v %v", v, err)
	}
	first.release()
	v, err := g.tryVerify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithCancel(ctx)
	cancel()
	if w, err := g.beginWrite(blocked); !errors.Is(err, context.Canceled) || w != nil {
		t.Fatalf("cancelled writer entered verification hold: %v %v", w, err)
	}
	v.release()
	next, err := g.beginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !next.follows(v) {
		t.Fatal("fresh writer lost its exact verifier epoch")
	}
	next.release()
	next.release()
	if again, err := g.beginWrite(ctx); err != nil {
		t.Fatalf("double release corrupted gate: %v", err)
	} else {
		if again.follows(v) {
			t.Fatal("intervening writer reused old verifier epoch")
		}
		again.release()
	}
}

func TestSyncScopeGateRefusesUnknownAndOverlappingHolds(t *testing.T) {
	ctx := t.Context()
	var nilGate *syncScopeGate
	if v, err := nilGate.tryVerify(ctx); !errors.Is(err, errSyncScopeGateBusy) || v != nil {
		t.Fatalf("nil gate gave verifier: %v %v", v, err)
	}
	g := newSyncScopeGate()
	if w, err := g.beginWrite(nil); !errors.Is(err, errSyncScopeGateBusy) || w != nil {
		t.Fatalf("nil context gave writer: %v %v", w, err)
	}
	v, err := g.tryVerify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := g.tryVerify(ctx); !errors.Is(err, errSyncScopeGateBusy) || second != nil {
		t.Fatalf("overlapping verifier entered gate: %v %v", second, err)
	}
	v.release()
	v.release()
	if next, err := g.tryVerify(ctx); err != nil {
		t.Fatalf("double release corrupted verifier gate: %v", err)
	} else {
		next.release()
	}
}
