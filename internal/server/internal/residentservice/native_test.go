package residentservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func negativeHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	return home
}
func negativeParent(t *testing.T, home string) *ParentPending {
	t.Helper()
	parent, err := NewParentPending(home, Build{SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// ONLY primitive fixture files, after synchronous original calls joined;
		// no grant/debit/child/borrower exists. This issues no product Closed or
		// resource credit and does not update the owner's unknown census.
		for _, original := range parent.owner.files.slots {
			if original != nil {
				if original.file != nil {
					if err := original.file.Close(); err != nil {
						t.Error(err)
					}
				} else {
					if err := unix.Close(original.number); err != nil {
						t.Error(err)
					}
				}
			}
		}
	})
	return parent
}

func TestResidentServicePendingMissingAuthorizationRetainsOriginalFailure(t *testing.T) {
	home := negativeHome(t)
	parent := negativeParent(t, home)
	before := parent.InspectOriginal()
	if before.OpenAttempted || before.OpenedFDs != 0 || before.DebitPrepared {
		t.Fatal(before)
	}
	first := parent.OpenExistingAuthorizedOriginal(t.Context())
	if first == nil {
		t.Fatal("missing authorization accepted")
	}
	after := parent.InspectOriginal()
	if !after.OpenAttempted || after.Opened || after.DebitAttempted || after.FirstError != first || after.LastPublishedLiveFDs == 0 || after.NativeOutcome != "unknown" {
		t.Fatal(after)
	}
	if err := parent.OpenExistingAuthorizedOriginal(t.Context()); err != first {
		t.Fatal("original failure lost or retried", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("missing policy created native grant/debit state", entries, err)
	}
}

func TestResidentServiceNativeRefusesFIFOAndDamagedJournalBeforeDebit(t *testing.T) {
	for _, kind := range []string{"fifo-lock", "fifo-journal", "damaged-journal"} {
		t.Run(kind, func(t *testing.T) {
			home := negativeHome(t)
			for _, name := range []string{lockName, journalName} {
				path := filepath.Join(home, name)
				if kind == "fifo-lock" && name == lockName || kind == "fifo-journal" && name == journalName {
					if err := unix.Mkfifo(path, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte("damaged serialization"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			parent := negativeParent(t, home)
			err := parent.OpenExistingAuthorizedOriginal(t.Context())
			if err == nil {
				t.Fatal("unsafe journal accepted")
			}
			seen := parent.InspectOriginal()
			if seen.Opened || seen.DebitAttempted || seen.DebitPrepared || seen.FirstError != err {
				t.Fatal(seen)
			}
			if kind == "damaged-journal" {
				data, readErr := os.ReadFile(filepath.Join(home, journalName))
				if readErr != nil || string(data) != "damaged serialization" {
					t.Fatal("damaged evidence repaired", readErr)
				}
			}
		})
	}
}

func TestResidentServiceCopiedAndCancelledHoldersIssueNoCharge(t *testing.T) {
	parent, err := NewParentPending("/synthetic", Build{SHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	copy := &ParentPending{owner: parent.owner}
	if err := copy.OpenExistingAuthorizedOriginal(t.Context()); !errors.Is(err, ErrInvalid) {
		t.Fatal("copied parent accepted", err)
	}
	child := NewFixedChildPending()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := child.ReceiveOriginal(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	seen := child.InspectOriginal()
	if seen.OpenedFDs != 0 || seen.DebitPrepared || seen.FirstError == nil {
		t.Fatal(seen)
	}
	if _, err := child.ClaimOriginalManagerCharge(); err == nil {
		t.Fatal("cancelled child issued standing charge")
	}
	if _, err := (&ManagerCharge{}).ObserveOriginal(); err == nil {
		t.Fatal("empty/copy charge accepted")
	}
}
