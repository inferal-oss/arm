package database

import (
	"testing"
)

type proofNestedState struct {
	State    string `json:"state"`
	Optional string `json:"optional,omitempty"`
}
type ProofEmbeddedState struct {
	State    string `json:"state"`
	Optional string `json:"optional,omitempty"`
}
type proofCanonicalEnvelope struct {
	ProofEmbeddedState
	Rows   []proofNestedState          `json:"rows"`
	ByName map[string]proofNestedState `json:"by_name"`
	Labels map[string]string           `json:"labels"`
}

func TestStrictEvidenceCanonicalKeysRecursivelyPreserveFreeMapKeys(t *testing.T) {
	valid := `{"state":"active","rows":[{"state":"active"}],"by_name":{"State":{"state":"active"}},"labels":{"State":"terminal","OPTIONAL":"permitted data"}}`
	var out proofCanonicalEnvelope
	if err := strictEvidence(valid, &out); err != nil {
		t.Fatal("valid exact schema and free map keys refused", err)
	}
	if out.Labels["State"] != "terminal" || out.ByName["State"].State != "active" {
		t.Fatal("free map keys changed")
	}
	for _, raw := range []string{
		`{"state":"active","State":"terminal","rows":[],"by_name":{},"labels":{}}`,
		`{"state":"active","OPTIONAL":"terminal","rows":[],"by_name":{},"labels":{}}`,
		`{"state":"active","rows":[{"state":"active","STATE":"terminal"}],"by_name":{},"labels":{}}`,
		`{"state":"active","rows":[],"by_name":{"arbitrary":{"state":"active","Optional":"terminal"}},"labels":{}}`,
		`{"state":"active","rows":[],"by_name":{},"labels":{},"LABELS":{"bad":"replacement"}}`,
	} {
		if err := strictEvidence(raw, &out); err == nil {
			t.Fatalf("noncanonical struct alias accepted: %s", raw)
		}
	}
}
