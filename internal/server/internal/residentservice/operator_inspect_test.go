package residentservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResidentOperatorReadOnlyInspectionDoesNotCreateOrAdopt(t *testing.T) {
	for _, name := range []string{"absent-files", "partial-lock", "missing-home"} {
		t.Run(name, func(t *testing.T) {
			home := negativeHome(t)
			state := "authority_files_not_observed"
			if name == "partial-lock" {
				// Negative primitive file ONLY; no valid grant/journal is created.
				if err := os.WriteFile(filepath.Join(home, lockName), nil, 0600); err != nil {
					t.Fatal(err)
				}
				state = "partial_authority"
			}
			if name == "missing-home" {
				home = filepath.Join(home, "absent")
				state = "home_not_observed"
			}
			pending, err := NewOperatorPending(home, filepath.Join(home, "missing-profile"), filepath.Join(home, "missing-evidence"), "original-negative", "read-only negative inspection")
			if err != nil {
				t.Fatal(err)
			}
			view, err := pending.InspectAuthorizationOriginal(t.Context(), strings.Repeat("a", 64))
			if err != nil || view.State != state || !view.ReadOnly || view.OriginalCommit != "unknown_from_readonly_inspection" || view.ExactRecordMatched || view.GrantID != "" || view.Inputs != nil || view.Resources != nil {
				t.Fatal(view, err)
			}
			seen := pending.InspectOriginal()
			if !seen.InspectionCompleted || seen.PlanAttempted || seen.ApplyAttempted || seen.AuthorityMutationAttempted || seen.GrantCommitKnown || seen.ExistingGrantRead {
				t.Fatal(seen)
			}
			if err = pending.CloseCompletedReferences(); err != nil {
				t.Fatal(err)
			}
			if pending.InspectOriginal().Original.NativeOutcome != "unknown" {
				t.Fatal("reference close became native closure")
			}
			if name == "missing-home" {
				if _, err = os.Stat(home); !os.IsNotExist(err) {
					t.Fatal("inspection created home", err)
				}
			} else {
				entries, err := os.ReadDir(home)
				want := 0
				if name == "partial-lock" {
					want = 1
				}
				if err != nil || len(entries) != want || want == 1 && entries[0].Name() != lockName {
					t.Fatal("inspection mutated authority", entries, err)
				}
			}
		})
	}
}
