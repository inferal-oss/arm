package providerproto

import (
	"embed"
	"encoding/json"
	"testing"
)

// These independently generated, reviewed proposal examples bind the canonical
// intent/config digest convention, not an implementation-generated expectation.
//
//go:embed schema/provider-v1.json schema/examples-v1.json
var schemaFiles embed.FS

func TestReviewedSchemaAndAllMethodExamples(t *testing.T) {
	raw, e := schemaFiles.ReadFile("schema/provider-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Definitions map[string]json.RawMessage `json:"$defs"`
	}
	if e = json.Unmarshal(raw, &schema); e != nil {
		t.Fatal(e)
	}
	var req struct {
		Required []string          `json:"required"`
		All      []json.RawMessage `json:"allOf"`
	}
	if e = json.Unmarshal(schema.Definitions["request"], &req); e != nil {
		t.Fatal(e)
	}
	configuration := false
	for _, name := range req.Required {
		if name == "configuration" {
			configuration = true
		}
	}
	if !configuration || len(req.All) != 8 {
		t.Fatal("schema lost required configuration/method roster")
	}
	raw, e = schemaFiles.ReadFile("schema/examples-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var examples struct {
		Requests         map[Method]json.RawMessage `json:"requests"`
		LookupResponse   json.RawMessage            `json:"lookup_response"`
		ValidateResponse json.RawMessage            `json:"validate_response"`
	}
	if e = json.Unmarshal(raw, &examples); e != nil {
		t.Fatal(e)
	}
	if len(examples.Requests) != 8 {
		t.Fatal("method roster")
	}
	decoded := map[Method]DecodedRequest{}
	for m, b := range examples.Requests {
		d, e := DecodeRequest(b)
		if e != nil {
			t.Fatalf("%s: %v", m, e)
		}
		if d.request.Method != m || d.request.Configuration.SHA256() != d.request.Registration.ConfigSHA256 {
			t.Fatal("example configuration")
		}
		decoded[m] = d
	}
	for m, d := range decoded {
		om := m
		if m == Lookup {
			om = Create
		}
		if m == Reconcile {
			om = Release
		}
		q := decoded[om].Request()
		reg, e := RegistrationFromTrusted(q.Registration, q.Configuration)
		if e != nil {
			t.Fatal(e)
		}
		o, e := OriginalFromTrusted(q)
		if e != nil {
			t.Fatal("independent original digest", m, e)
		}
		var target *ExpectedTarget
		spec := decoded[Create].request.Params.(CreateParams).SpecificationSHA256
		var i Identity
		var p Digest
		var v Uint
		switch x := q.Params.(type) {
		case RenewParams:
			i, p, v = x.ExternalIdentity, x.PriorReceiptSHA256, x.ExpectedRevision
		case ReleaseParams:
			i, p, v = x.ExternalIdentity, x.PriorReceiptSHA256, x.ExpectedRevision
		}
		if v != "" {
			x, e := TargetFromTrusted(i, p, v, spec)
			if e != nil {
				t.Fatal(e)
			}
			target = &x
		}
		sent, e := MatchRequest(d, reg, o, target)
		if e != nil {
			t.Fatal(m, e)
		}
		var reply json.RawMessage
		if m == Lookup {
			reply = examples.LookupResponse
		}
		if m == Validate {
			reply = examples.ValidateResponse
		}
		if reply != nil {
			r, e := DecodeResponse(reply)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = MatchResponse(r, sent, reg, o, target); e != nil {
				t.Fatal(e)
			}
		}
	}
}
