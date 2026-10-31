package residentservice

import "testing"

// Pure stage DATA. No owner/group/grant/native holder is fabricated or passed to
// an issuer; no read/open/append/Start is invoked and no buffer is called funded.
func TestP1PreStartJournalBudgetDataExactStagesAndRefusals(t *testing.T) {
	stages := []string{"open", "debit", "p1-funding", "p1-binding", "p1-before-start"}
	for i, stage := range stages {
		if err := journalReadBudgetData(true, stages[:i], stage); err != nil {
			t.Fatal(i, err)
		}
		if err := journalReadBudgetData(true, stages[:i], "unlisted-stage"); err != ErrInvalid {
			t.Fatal("unlisted read", i, err)
		}
		if i > 0 {
			if err := journalReadBudgetData(true, stages[:i], stages[i-1]); err != ErrInvalid {
				t.Fatal("replayed read", i, err)
			}
		}
	}
	if err := journalReadBudgetData(true, stages, "p1-before-start"); err != ErrInvalid {
		t.Fatal("sixth read", err)
	}
	if err := journalReadBudgetData(true, []string{"open", "p1-funding"}, "p1-funding"); err != ErrInvalid {
		t.Fatal("skipped debit", err)
	}
	if err := journalReadBudgetData(true, []string{"debit"}, "debit"); err != ErrInvalid {
		t.Fatal("foreign prefix", err)
	}
	if err := journalReadBudgetData(true, stages, "p1-ACK-current-authority"); err != ErrInvalid {
		t.Fatal("unimplemented later read", err)
	}
}
func TestLegacyJournalBudgetDataRetainsFourReadLimit(t *testing.T) {
	prior := []string{"legacy-one", "legacy-two", "legacy-three", "legacy-four"}
	for i := 0; i < 4; i++ {
		if err := journalReadBudgetData(false, prior[:i], "existing-stage"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := journalReadBudgetData(false, prior, "existing-stage"); err != ErrInvalid {
		t.Fatal("legacy bound changed", err)
	}
}
