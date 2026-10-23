package providerproto

import (
	"bytes"
	"embed"
	"encoding/json"
	"reflect"
	"testing"
)

// Generated independently with Python canonical JSON and SHA-256 from the reviewed
// eight-method examples. Every request retains the actual configuration; queries
// retain their original mutation's null attribution and exact intent digest.
//
//go:embed schema/examples-owner-unavailable-v1.json
var unavailableOwnerExamples embed.FS

func TestUnavailableOwnerNameAllMethods(t *testing.T) {
	raw, err := unavailableOwnerExamples.ReadFile("schema/examples-owner-unavailable-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples struct {
		Requests map[Method]json.RawMessage `json:"requests"`
	}
	if err = json.Unmarshal(raw, &examples); err != nil {
		t.Fatal(err)
	}
	if len(examples.Requests) != 8 {
		t.Fatal("method roster")
	}
	for method, raw := range examples.Requests {
		t.Run(string(method), func(t *testing.T) {
			d, err := DecodeRequest(raw)
			if err != nil {
				t.Fatal(err)
			}
			q := d.Request()
			if q.Context.OwnerName != nil || q.Context.OwnerUID == "" {
				t.Fatal("unavailable username lost UID")
			}
			out, err := EncodeRequest(q)
			if err != nil || !bytes.Contains(out, []byte(`"owner_name":null`)) {
				t.Fatal("required null field", err)
			}
			origMethod := method
			if method == Lookup {
				origMethod = Create
			}
			if method == Reconcile {
				origMethod = Release
			}
			origDecoded, err := DecodeRequest(examples.Requests[origMethod])
			if err != nil {
				t.Fatal(err)
			}
			orig := origDecoded.Request()
			reg, original, target := trust(t, orig)
			matched, err := MatchRequest(d, reg, original, target)
			if err != nil {
				t.Fatal(err)
			}
			if matched.Request().Context.OwnerName != nil {
				t.Fatal("matching invented a username")
			}
			changed := q
			changed.Context.OwnerName = username("operator")
			if _, err = MatchRequest(dec(t, changed), reg, original, target); err == nil {
				t.Fatal("query/original attribution was silently updated")
			}
		})
	}
}

func TestOwnerNameRequiredNullableAndDetached(t *testing.T) {
	q := base(t, Create)
	raw, err := EncodeRequest(q)
	if err != nil {
		t.Fatal(err)
	}
	for name, replacement := range map[string][]byte{
		"missing": nil, "empty": []byte(`"owner_name":"",`), "wrong_type": []byte(`"owner_name":false,`),
	} {
		t.Run(name, func(t *testing.T) {
			bad := bytes.Replace(raw, []byte(`"owner_name":"owner",`), replacement, 1)
			if bytes.Equal(raw, bad) {
				t.Fatal("fixture did not change")
			}
			if _, err := DecodeRequest(bad); err == nil {
				t.Fatal("invalid owner name accepted")
			}
		})
	}
	name := Username("rémy")
	q.Context.OwnerName = &name
	q = seal(t, q)
	reg, original, target := trust(t, q)
	d := dec(t, q)
	matched, err := MatchRequest(d, reg, original, target)
	if err != nil {
		t.Fatal(err)
	}
	savedDigest := q.Context.IntentSHA256
	cloned := cloneRequest(q)
	if cloned.Context.OwnerName == q.Context.OwnerName {
		t.Fatal("clone retained caller pointer")
	}
	name = "changed caller"
	if *cloned.Context.OwnerName != "rémy" {
		t.Fatal("clone changed with caller")
	}
	for label, snapshot := range map[string]func() Request{"decoded": d.Request, "original": original.Request, "matched": matched.Request} {
		t.Run(label, func(t *testing.T) {
			first := snapshot()
			if first.Context.OwnerName == nil || *first.Context.OwnerName != "rémy" {
				t.Fatal("stored value changed")
			}
			*first.Context.OwnerName = "changed returned pointer"
			second := snapshot()
			if second.Context.OwnerName == first.Context.OwnerName || *second.Context.OwnerName != "rémy" || second.Context.IntentSHA256 != savedDigest {
				t.Fatal("returned pointer aliases retained snapshot")
			}
		})
	}
	nilQ := base(t, Create)
	nilQ.Context.OwnerName = nil
	nilQ = seal(t, nilQ)
	if nilQ.Context.IntentSHA256 == savedDigest {
		t.Fatal("null erased immutable intent distinction")
	}
	if !reflect.DeepEqual(cloneRequest(nilQ), nilQ) {
		t.Fatal("nil clone")
	}
}

func TestOwnerNameSchemaRequiredNullable(t *testing.T) {
	raw, err := schemaFiles.ReadFile("schema/provider-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Definitions map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	context := schema.Definitions["context"]
	found := false
	for _, s := range context.Required {
		found = found || s == "owner_name"
	}
	if !found {
		t.Fatal("owner_name must remain required")
	}
	var prop struct {
		OneOf []struct {
			Ref  string `json:"$ref"`
			Type string `json:"type"`
		} `json:"oneOf"`
	}
	if err = json.Unmarshal(context.Properties["owner_name"], &prop); err != nil {
		t.Fatal(err)
	}
	if len(prop.OneOf) != 2 || prop.OneOf[0].Ref != "#/$defs/username" || prop.OneOf[1].Type != "null" {
		t.Fatal("schema must distinguish bounded present username and null")
	}
}
