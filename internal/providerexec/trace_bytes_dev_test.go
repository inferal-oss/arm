//go:build arm_devtrace

package providerexec

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestObservedSQLByteEnvelopeIsExactAndDetached(t *testing.T) {
	initial := []byte("{\n  \"exact\": \"<&>\\u2028\\u0001\"\n}")
	terminal := []byte("{\"phase\":\"unknown\",\"bytes\": \"\\u003c <\"}")
	expectedI, expectedT := bytes.Clone(initial), bytes.Clone(terminal)
	r := &traceRecorder{}
	o := observer{r}
	o.sql("durable_unknown_ack", "attempt", initial, terminal, TerminalCeiling-len(terminal))
	initial[0] = '['
	terminal[0] = '['
	events, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	pretty, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var restored []traceEvent
	if err = json.Unmarshal(pretty, &restored); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Initial   []byte `json:"initial"`
		Terminal  []byte `json:"terminal"`
		Remaining int    `json:"remaining"`
	}
	if err = json.Unmarshal(restored[0].Raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(evidence.Initial, expectedI) || !bytes.Equal(evidence.Terminal, expectedT) || evidence.Remaining+len(evidence.Terminal) != TerminalCeiling {
		t.Fatal("pretty output or source reuse changed exact acknowledged SQL bytes")
	}
}
func TestObservedSQLByteEnvelopeFitsWorstFixedRecordBounds(t *testing.T) {
	r := &traceRecorder{}
	o := observer{r}
	initial, terminal := bytes.Repeat([]byte{'x'}, InitialCeiling), bytes.Repeat([]byte{'y'}, TerminalCeiling)
	o.sql("durable_reserve_ack", "attempt", initial, terminal, 0)
	events, err := r.snapshot()
	if err != nil || len(events) != 1 || len(events[0].Raw) > 160<<10 {
		t.Fatal("base64 envelope exceeded declared bounded trace event", len(events), err)
	}
	// This is the observer byte-codec bound, not a claim that arbitrary x/y bytes
	// would be valid SQL state or that the native lifecycle reached this superset.
}
