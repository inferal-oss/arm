package nativeowner

import (
	"context"
	"errors"
	"testing"
)

func TestOriginalBorrowerValidationConcurrencyKeepsFreshIdentity(t *testing.T) {
	issuer, err := NewServePending(nativeFixture(t, 0600))
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, issuer) // only original native fixture FDs; no SQL or real borrower tasks
	if err = issuer.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	mv, err := issuer.ManagerView()
	if err != nil {
		t.Fatal(err)
	}
	mc, err := mv.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	sv, err := issuer.ServerView()
	if err != nil {
		t.Fatal(err)
	}
	sc, err := sv.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	want, err := mc.ValidateOriginalContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			for j := 0; j < 3; j++ {
				var got Snapshot
				var err error
				if j%2 == 0 {
					got, err = mc.ValidateOriginalContext(t.Context())
				} else {
					got, err = sc.ValidateOriginalContext(t.Context())
				}
				if err != nil {
					results <- err
					return
				}
				if got != want {
					results <- errors.New("original native identity changed")
					return
				}
			}
			results <- nil
		}()
	}
	close(start)
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Error("ordinary concurrent borrower validation failed", err)
		}
	}
	seen, err := issuer.InspectOriginal()
	if err != nil || seen.OperationPending || seen.FirstFailure != nil || seen.Retired {
		t.Fatal(seen, err)
	}
}

func TestOriginalBorrowerCancelledValidationDoesNotPoisonCustody(t *testing.T) {
	issuer, err := NewServePending(nativeFixture(t, 0600))
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, issuer)
	if err = issuer.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	view, err := issuer.ServerView()
	if err != nil {
		t.Fatal(err)
	}
	claim, err := view.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := claim.ValidateOriginalContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = claim.ValidateOriginalContext(t.Context()); err != nil {
		t.Fatal("cancelled caller poisoned original custody", err)
	}
	copy := &ServerClaim{owner: claim.owner, view: claim.view}
	if _, err := copy.ValidateOriginalContext(t.Context()); !errors.Is(err, errNotOriginal) {
		t.Fatal("copied claim accepted", err)
	}
}
