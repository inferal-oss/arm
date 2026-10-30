package nativeowner

import (
	"errors"
	"testing"
	"time"
)

// Explicit compiler overlays alone enable a pause in the actual original open
// method after its real FD is registered, before Fstat. No production hook exists.
const expectNativeEffectPause = false

func nativeEffectPauseChannels() (<-chan struct{}, func()) { return nil, func() {} }

func TestPendingOriginalNativeEffectAllowsInspectAndSeal(t *testing.T) {
	if !expectNativeEffectPause {
		t.Skip("requires the separately reviewed original-method compiler overlay")
	}
	reached, release := nativeEffectPauseChannels()
	issuer, err := NewServePending(nativeFixture(t, 0600))
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, issuer)
	done := make(chan error, 1)
	go func() { done <- issuer.OpenOriginal() }()
	// Always release this actual original method and wait before fixture native
	// retirement, including an assertion failure. This proves no product closure.
	defer func() {
		release()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("original native fixture return still pending")
		}
	}()
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatal("original native FD pause was not reached")
	}
	type inspected struct {
		value Inspection
		err   error
	}
	inspection := make(chan inspected, 1)
	go func() { v, err := issuer.InspectOriginal(); inspection <- inspected{v, err} }()
	select {
	case value := <-inspection:
		if value.err != nil || !value.value.OperationPending || !value.value.NativeCensusUncertain || value.value.LiveDescriptors < 1 || value.value.OriginalRetirement.Complete {
			t.Fatal("pending original native custody was hidden", value)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("inspection blocked behind original native effect")
	}
	sealed := make(chan error, 1)
	go func() { _, _, err := issuer.Seal(); sealed <- err }()
	select {
	case err := <-sealed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("seal blocked behind original native effect")
	}
	if _, err := issuer.RetireOriginal(); !errors.Is(err, ErrNativePending) {
		t.Fatal("pending original allowed retirement", err)
	}
	if _, err := issuer.ManagerView(); !errors.Is(err, ErrNativePending) && !errors.Is(err, errSealed) {
		t.Fatal("pending original allowed new native work", err)
	}
}
