package providerexec

import (
	"encoding/json"
	"testing"
)

func TestStrictControlNestedSlicesArraysAndNullableElements(t *testing.T) {
	type leaf struct {
		Owner uint32 `json:"owner"`
	}
	type box struct {
		Matrix   [2][]leaf       `json:"matrix"`
		Optional []*leaf         `json:"optional"`
		Opaque   json.RawMessage `json:"opaque"`
	}
	good := []byte(`{"matrix":[[{"owner":1}],[{"owner":2}]],"optional":[null,{"owner":3}],"opaque":{"provider-owned":"decoded separately"}}`)
	var decoded box
	if err := strictJSON(good, &decoded); err != nil {
		t.Fatal("complete nested collection control refused", err)
	}
	for _, body := range []string{
		`{"matrix":[[{"owner":1}]],"optional":[],"opaque":null}`,
		`{"matrix":[[{"owner":1}],[],[]],"optional":[],"opaque":null}`,
		`{"matrix":[null,[]],"optional":[],"opaque":null}`,
		`{"matrix":[[null],[]],"optional":[],"opaque":null}`,
		`{"matrix":[[{"OWNER":1}],[]],"optional":[],"opaque":null}`,
		`{"matrix":[[{"owner":null}],[]],"optional":[],"opaque":null}`,
		`{"matrix":[[{"owner":1}],[]],"optional":[{}],"opaque":null}`,
		`{"matrix":[[{"owner":1,"owner":2}],[]],"optional":[],"opaque":null}`,
	} {
		var value box
		if strictJSON([]byte(body), &value) == nil {
			t.Fatal("lossy nested control value accepted", body)
		}
	}
	var scalars [2]uint32
	if strictJSON([]byte(`[1,null]`), &scalars) == nil {
		t.Fatal("required scalar array null became zero")
	}
	if strictJSON([]byte(`[1]`), &scalars) == nil {
		t.Fatal("fixed array missing value became zero")
	}
}
