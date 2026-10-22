package database_test

import (
	"testing"
	"time"
)

func TestSettledProofReviewCaseAliasesCannotHideActiveLease(t *testing.T) {
	home := proofFixture(t, true)
	before, err := prove(t, home)
	if err != nil || before.Settled {
		t.Fatalf("fixture not active: %+v %v", before, err)
	}
	edit(t, home, `UPDATE leases SET data=json_set(data,'$.State','terminal','$.Outcome','released','$.Ended_at',?)`, time.Now().UTC().Format(time.RFC3339Nano))
	after, err := prove(t, home)
	if err == nil && after.Settled {
		t.Fatalf("case aliases erased active obligations: %+v", after)
	}
}
