package database

import "testing"

type reviewNestedEvidence struct {
	State     string `json:"state"`
	Uncertain bool   `json:"uncertain"`
	Settled   bool   `json:"settled,omitempty"`
}
type reviewEmbeddedEvidence struct {
	Name string `json:"name"`
}
type reviewEnvelopeEvidence struct {
	reviewEmbeddedEvidence
	Nested reviewNestedEvidence `json:"nested"`
	Labels map[string]string    `json:"labels"`
}

func TestSettledProofReviewNestedCaseAliasesAndFreeFormKeys(t *testing.T) {
	for _, raw := range []string{
		`{"name":"scope","Name":"replacement","nested":{"state":"active","uncertain":true},"labels":{}}`,
		`{"name":"scope","nested":{"state":"active","State":"terminal","uncertain":true},"labels":{}}`,
		`{"name":"scope","nested":{"state":"active","ſtate":"terminal","uncertain":true},"labels":{}}`,
		`{"name":"scope","nested":{"state":"active","uncertain":true,"Uncertain":false},"labels":{}}`,
		`{"name":"scope","nested":{"state":"active","uncertain":true,"Settled":true},"labels":{}}`,
	} {
		var decoded reviewEnvelopeEvidence
		if err := strictEvidence(raw, &decoded); err == nil {
			t.Fatalf("case alias accepted as structured proof: %s => %+v", raw, decoded)
		}
	}
	var valid reviewEnvelopeEvidence
	if err := strictEvidence(`{"name":"scope","nested":{"state":"active","uncertain":true},"labels":{"state":"one","State":"two","ſtate":"three"}}`, &valid); err != nil || len(valid.Labels) != 3 || valid.Labels["State"] != "two" {
		t.Fatalf("free-form map keys changed/rejected: %+v %v", valid, err)
	}
	var opaque any
	if err := strictEvidence(`{"state":"one","State":"two"}`, &opaque); err != nil {
		t.Fatal("opaque historical audit keys must remain case-sensitive data", err)
	}
}
