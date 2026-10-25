package nodestop

import (
	"encoding/json"
	"strings"
	"testing"
)

func sourceResultFixture(t *testing.T) (SourceReview, Result) {
	t.Helper()
	_, p, _, _ := contractFixture(t)
	hash, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Schema: RecordSchema, Plan: p, Digest: hash, Shutdown: strings.Repeat("6", 32), DesiredRevision: p.DesiredRevision + 1, Phase: "drain-ready", Created: p.Observed, Updated: p.Observed}
	state := State{Schema: StateSchema, Revision: record.DesiredRevision, Target: p.Target, Key: p.Key, Digest: hash, Running: false}
	review := SourceReview{Operation: "stop-arm-node", Key: p.Key, Plan: p, SHA256: hash, HistoryCapacity: MaxRecords}
	return review, Result{Operation: "stop-arm-node", Key: p.Key, Status: "stopped", Observation: &Observation{Record: record, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}, Detail: "test projection only; no native absence inferred"}
}
func TestSourceStopResultRequiresCompleteOriginalGenerationObservation(t *testing.T) {
	review, result := sourceResultFixture(t)
	if err := result.Validate(review); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"receipt-only", "not-joined", "not-absent", "foreign-key", "foreign-target", "accepted-without-record", "ambiguous-dual-record", "unknown-with-observation"} {
		t.Run(kind, func(t *testing.T) {
			_, r := sourceResultFixture(t)
			switch kind {
			case "ambiguous-dual-record":
				copy := r.Observation.Record
				r.Record = &copy
			case "unknown-with-observation":
				r.Status = "unknown"
			case "receipt-only":
				r.Observation = nil
			case "not-joined":
				r.Observation.ServerJoined = false
			case "not-absent":
				r.Observation.OriginalGenerationAbsent = false
			case "foreign-key":
				r.Key = "other"
			case "foreign-target":
				r.Observation.Desired.Target.Home = "/private/other"
			case "accepted-without-record":
				r.Status = "accepted"
				r.Record = nil
				r.Observation = nil
			}
			if err := r.Validate(review); err == nil {
				t.Fatal("incomplete/foreign result accepted")
			}
		})
	}
}
func TestSourceStopObservationRetainsSeparateReviewedRestartIntent(t *testing.T) {
	review, result := sourceResultFixture(t)
	result.Observation.Desired.Running = true
	result.Observation.Desired.Revision++
	if err := result.Validate(review); err != nil {
		t.Fatal("old generation observation wrongly erased valid separate restart intent", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	malformed := strings.Replace(string(raw), `"server_joined":true`, `"server_joined":true,"server_joined":false`, 1)
	var decoded Result
	if err = Decode([]byte(malformed), &decoded); err == nil {
		t.Fatal("duplicate completion claim accepted")
	}
}
