package pressure

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStateJSONRoundTripPreservesControlFence(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, restored) {
		t.Fatalf("journal changed state: %#v %#v", s, restored)
	}
	again, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatal("journal encoding is nondeterministic")
	}
	a := pauseFromState(restored, j)
	if !ControlAllowed(restored, a, input(15, 10, j), p) {
		t.Fatal("journal round trip lost exact control fence")
	}
}

func TestStateJSONRejectsDuplicateRecords(t *testing.T) {
	var s State
	if err := json.Unmarshal([]byte(`{"pauses":[{"Job":{"LeaseID":"x"}},{"Job":{"LeaseID":"x"}}]}`), &s); err == nil {
		t.Fatal("duplicate pause accepted")
	}
}

func TestJournalPreservesOldEpochObligation(t *testing.T) {
	p, j := policy(), owned()
	s := reachPause(t, p, j)
	in := input(21, 50, j)
	in.Epoch = "boot-2"
	s, _ = advance(t, s, in, p)
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, restored) || len(restored.Unresolved) != 1 {
		t.Fatal("journal dropped old-epoch obligation")
	}
}

func TestJournalRejectsIdentityNormalizationAndOversize(t *testing.T) {
	for name, data := range map[string][]byte{
		"invalid raw UTF8":        []byte{'{', '"', 0xff, '"', '}'},
		"isolated high surrogate": []byte(`{"epoch":"\ud800"}`),
		"isolated low surrogate":  []byte(`{"epoch":"\udc00"}`),
		"oversized JSON":          []byte(`{"epoch":"` + strings.Repeat("e", maxJournalBytes) + `"}`),
		"too many tiny pauses":    []byte(`{"pauses":[` + strings.TrimSuffix(strings.Repeat(`{},`, maxJournalJobs+1), ",") + `]}`),
	} {
		t.Run(name, func(t *testing.T) {
			var state State
			if err := json.Unmarshal(data, &state); err == nil {
				t.Fatal("invalid journal accepted")
			}
		})
	}
	bad := State{Epoch: string([]byte{0xff})}
	if _, err := json.Marshal(bad); err == nil {
		t.Fatal("invalid UTF8 state marshaled")
	}
}
