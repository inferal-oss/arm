package database

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/nodeavailability"
)

func TestAvailabilityIndependentCosmeticProof(t *testing.T) {
	host, owner := strings.Repeat("a", 32), "42"
	update := nodeavailability.Update{Key: "availability-key", Profile: "intermittent", Reason: "cosmetic fixture"}
	d := nodeavailability.Document{Schema: 1, Host: host, Owner: owner, Revision: 1, Profile: update.Profile, Reason: update.Reason, UpdatedAt: time.Now().UTC(), LastUpdate: update}
	raw, _ := json.Marshal(d)
	if err := validateNodeAvailabilityMetadata(string(raw), host, owner); err != nil {
		t.Fatal("actual producer rejected", err)
	}
	for _, raw := range []string{
		strings.Replace(string(raw), `"schema":1`, `"schema":2`, 1),
		strings.Replace(string(raw), `"owner":"42"`, `"owner":"43"`, 1),
		strings.Replace(string(raw), `"host":"`+host+`"`, `"host":"`+strings.Repeat("b", 32)+`"`, 1),
		strings.Replace(string(raw), `"schema":1`, `"Schema":1`, 1),
		strings.Replace(string(raw), `"schema":1`, `"schema":1,"schema":1`, 1),
		strings.Replace(string(raw), `"revision":1`, `"revision":0`, 1),
		strings.Replace(string(raw), `"expected_revision":0`, `"expected_revision":1`, 1),
		strings.Replace(string(raw), `"schema":1`, `"schema":1,"settled":true`, 1),
		string(raw) + strings.Repeat(" ", 8<<10),
	} {
		if err := validateNodeAvailabilityMetadata(raw, host, owner); err == nil {
			t.Fatal("invalid cosmetic proof accepted", raw)
		}
	}
}

func TestAvailabilityProofReadBoundsBeforeDecode(t *testing.T) {
	columns := []string{"key", "value"}
	if got := nodeAvailabilityProofReadBound("metadata", "value"); !strings.Contains(got, "8192") || !strings.Contains(got, "node_availability_v1") {
		t.Fatal("profile SQL projection is not bounded", got)
	}
	if got := nodeAvailabilityProofReadBound("leases", "data"); got != "1048576" {
		t.Fatal("unrelated record bound changed", got)
	}
	for _, test := range []struct {
		name  string
		kind  string
		size  any
		raw   any
		valid bool
	}{
		{"bounded-text", "text", int64(2), "{}", true},
		{"oversize-before-read", "text", int64(8193), nil, false},
		{"blob", "blob", int64(2), []byte("{}"), false},
		{"null", "null", nil, nil, false},
		{"partial-read", "text", int64(3), "{}", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := []any{"text", int64(20), "node_availability_v1", test.kind, test.size, test.raw}
			err := guardNodeAvailabilityMetadataRow("metadata", columns, values)
			if (err == nil) != test.valid {
				t.Fatal("wrong bounded row classification", err)
			}
		})
	}
}
