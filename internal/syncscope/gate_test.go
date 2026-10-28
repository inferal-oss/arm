package syncscope

import (
	"context"
	"testing"
)

func TestFreshWriterFollowsOnlyUninterruptedVerification(t *testing.T) {
	ctx := context.Background()
	gate := NewGate()
	verify, err := gate.TryVerify(ctx)
	if err != nil {
		t.Fatal(err)
	}
	verify.Release()
	first, err := gate.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Follows(verify) {
		t.Fatal("next writer lost original verification order")
	}
	first.Release()
	first.Release()
	second, err := gate.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Follows(verify) {
		t.Fatal("intervening writer reused stale verification")
	}
	second.Release()
}
