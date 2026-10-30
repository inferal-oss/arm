package nativeowner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// These fixtures own only temporary directories/descriptors. They construct no
// core, Manager, SQL pool, worker or live job and prove no borrower closure.
func nativeFixture(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// testing.TempDir children use0777 subject to umask. This fixture owns
	// its leaf and must explicitly prepare the strict private0700 precondition.
	// Production construction never repairs another directory's permissions.
	if err = os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	setup, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Mode().Perm() != 0700 {
		t.Fatal("fixture home is not prepared private0700")
	}
	f, err := os.OpenFile(filepath.Join(path, "daemon.lock"), os.O_CREATE|os.O_EXCL|os.O_RDWR, mode)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func retainFixtureIssuer(t *testing.T, i *ServeIssuer) {
	t.Helper()
	t.Cleanup(func() {
		// These fixtures create ONLY native issuer/view records. No actual borrower
		// can exist; product retirement still requires the real root closure permit.
		if _, _, err := i.Seal(); err != nil {
			t.Error(err)
			return
		}
		closure, err := i.RetireOriginal()
		if !closure.Complete || closure.Unknown != 0 {
			t.Error("original fixture FD closure unknown", closure, err)
		}
	})
}
func TestPendingExistsBeforeNativeOpenAndOneAttempt(t *testing.T) {
	path := nativeFixture(t, 0600)
	i, err := NewServePending(path)
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, i)
	before, err := i.InspectOriginal()
	if err != nil || before.OpenAttempted || before.Opened != 0 || before.LiveDescriptors != 0 {
		t.Fatal(before, err)
	}
	if _, err = i.ManagerView(); err == nil {
		t.Fatal("pending issuer issued borrower")
	}
	if err = i.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	after, err := i.InspectOriginal()
	if err != nil || !after.OpenComplete || after.LiveDescriptors != 2 || len(after.Generation) != 32 {
		t.Fatal(after, err)
	}
	if err = i.OpenOriginal(); err == nil {
		t.Fatal("second original open accepted")
	}
	view, err := i.ManagerView()
	if err != nil {
		t.Fatal("pre-open refusal poisoned later actual view", err)
	}
	claim, err := view.ClaimOriginal()
	if err != nil {
		t.Fatal("post-open original claim unusable", err)
	}
	if _, err = claim.ValidateOriginal(); err != nil {
		t.Fatal("post-open actual claim failed native validation", err)
	}
	if c, err := i.RetireOriginal(); err == nil || c.Complete {
		t.Fatal("unsealed native authority retired", c, err)
	}
}
func TestPartialOpenRetainsActualDirectoryAndFirstError(t *testing.T) {
	path := nativeFixture(t, 0644)
	i, err := NewServePending(path)
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, i)
	original := i.OpenOriginal()
	if original == nil {
		t.Fatal("nonprivate lock accepted")
	}
	state, err := i.InspectOriginal()
	if err != nil || state.OpenComplete || state.LiveDescriptors == 0 || state.FirstFailure == nil || !errors.Is(state.FirstFailure, original) {
		t.Fatal(state, err)
	}
	// A repaired pathname cannot clear the first failed original attempt or make
	// the same holder open again. No fixture pretends this error is native Closed.
	if err = os.Chmod(filepath.Join(path, "daemon.lock"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = i.OpenOriginal(); err == nil {
		t.Fatal("failed original open retried")
	}
	if _, err = i.ServerView(); !errors.Is(err, original) {
		t.Fatal("first error was lost", err)
	}
}
func TestOriginalViewsClaimsAndStopTickets(t *testing.T) {
	i, err := NewServePending(nativeFixture(t, 0600))
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, i)
	if err = i.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	manager, err := i.ManagerView()
	if err != nil {
		t.Fatal(err)
	}
	server, err := i.ServerView()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = i.ManagerView(); err == nil {
		t.Fatal("second Manager view issued")
	}
	copied := *manager
	if _, err = copied.ValidateOriginal(); err == nil {
		t.Fatal("copied view accepted")
	}
	claim, err := manager.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	other, err := server.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ClaimOriginal(); err == nil {
		t.Fatal("second Manager holder claimed")
	}
	copiedClaim := *claim
	if _, err = copiedClaim.ValidateOriginal(); err == nil {
		t.Fatal("copied claim accepted")
	}
	first, second, err := i.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = claim.ValidateOriginal(); err == nil {
		t.Fatal("sealed admission accepted")
	}
	if err = first.ValidateOriginalClaim(claim); err != nil {
		t.Fatal(err)
	}
	if err = second.ValidateOriginalClaim(other); err != nil {
		t.Fatal(err)
	}
	copiedStop := *first
	if err = copiedStop.ValidateOriginalClaim(claim); err == nil {
		t.Fatal("copied stop accepted")
	}
	replayA, replayB, err := i.Seal()
	if err != nil || replayA != first || replayB != second {
		t.Fatal("original stop tickets replaced", err)
	}
}
func TestForeignIssuerAndStickyPathChange(t *testing.T) {
	path := nativeFixture(t, 0600)
	i, err := NewServePending(path)
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, i)
	if err = i.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	view, err := i.ManagerView()
	if err != nil {
		t.Fatal(err)
	}
	copied := *i
	if _, err = copied.InspectOriginal(); err == nil {
		t.Fatal("copied issuer accepted")
	}
	// An independent actual original cannot acquire the same already-held flock.
	other, err := NewServePending(path)
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, other)
	if err = other.OpenOriginal(); err == nil {
		t.Fatal("independent issuer bypassed original lock")
	}
	if err = os.Rename(filepath.Join(path, "daemon.lock"), filepath.Join(path, "old.lock")); err != nil {
		t.Fatal(err)
	}
	failure := func() error { _, err := view.ValidateOriginal(); return err }()
	if failure == nil {
		t.Fatal("missing original pathname accepted")
	}
	if err = os.Rename(filepath.Join(path, "old.lock"), filepath.Join(path, "daemon.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err = view.ValidateOriginal(); !errors.Is(err, failure) {
		t.Fatal("restored path cleared original failure", err)
	}
}

func TestPendingBorrowerClaimRetainsNativeValidationFailure(t *testing.T) {
	path := nativeFixture(t, 0600)
	issuer, err := NewServePending(path)
	if err != nil {
		t.Fatal(err)
	}
	retainFixtureIssuer(t, issuer)
	if err = issuer.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	view, err := issuer.ManagerView()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(path, "daemon.lock"), filepath.Join(path, "old.lock")); err != nil {
		t.Fatal(err)
	}
	claim, original := view.ClaimOriginal()
	if original == nil || claim == nil {
		t.Fatal("failed native claim was discarded", claim, original)
	}
	snapshot, err := issuer.InspectOriginal()
	if err != nil || !snapshot.ManagerClaimed || !errors.Is(snapshot.FirstFailure, original) {
		t.Fatal(snapshot, err)
	}
	if _, err = view.ClaimOriginal(); err == nil {
		t.Fatal("failed original holder was replaced")
	}
	stop, _, err := issuer.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if err = stop.ValidateOriginalClaim(claim); err != nil {
		t.Fatal("original cleanup holder lost on path failure", err)
	}
}
