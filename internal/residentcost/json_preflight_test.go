package residentcost

import (
	"strings"
	"testing"
)

func TestPendingJSONPreflightRejectsMalformedAllocationAmplification(t *testing.T) {
	for _, raw := range []string{
		`{"Artifacts":[` + strings.Repeat(`{},`, 256) + `{}]}`,
		`{"Items":[` + strings.Repeat(`[],`, 256) + `[]]}`,
		`{"Deep":` + strings.Repeat(`[`, 17) + `0` + strings.Repeat(`]`, 17) + `}`,
		`{"N":` + strings.Repeat(`1`, 33) + `}`,
		`{"Text":"` + strings.Repeat(`x`, (128<<10)+1) + `"}`,
		`{"A":1}{"B":2}`,
		`null`,
	} {
		if PreflightEvidenceJSONData([]byte(raw)) == nil {
			t.Fatal("malformed allocation shape accepted")
		}
	}
	// Duplicate keys remain for the typed canonical check, but their aggregate
	// structure cannot bypass the PRE-decode cardinality/token/depth budget.
	raw := `{"Raw":[` + strings.Repeat(`0,`, 4096) + `0]}`
	if PreflightEvidenceJSONData([]byte(raw)) == nil {
		t.Fatal("oversized byte-array token count accepted")
	}
	if PreflightEvidenceJSONData([]byte(`{"Artifacts":[`+strings.Repeat(`null,`, 64)+`null]}`)) == nil {
		t.Fatal("null entries bypassed pre-decode element cap")
	}
	if PreflightLedgerJSONData([]byte(`{"Unknowns":[`+strings.Repeat(`{},`, 64)+`{}]}`)) == nil {
		t.Fatal("ledger object count exceeded")
	}
}

func TestPendingJSONPreflightAcceptsBoundedScalarsBeforeCanonicalChecks(t *testing.T) {
	if err := PreflightEvidenceJSONData([]byte(`{"Values":[` + strings.Repeat(`0,`, 63) + `0]}`)); err != nil {
		t.Fatal("exact preflight array bound refused", err)
	}
	for _, raw := range []string{`{}`, `{"A":[true,false,null,"text",12.5e2],"B":{"N":0}}`, `{"A":1,"A":2}`} {
		if err := PreflightEvidenceJSONData([]byte(raw)); err != nil {
			t.Fatal("bounded JSON shape refused", err)
		}
	}
}
