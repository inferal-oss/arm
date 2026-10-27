package nodestop

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/model"
)

// Pure typed shape/boundary controls, not native closure observations. The
// separate server lifecycle test supplies real joined/marker/absence evidence.
func TestStopFrameBudgetRefusesBeforePermitAndRetainsCanonicalBoundary(t *testing.T) {
	_, p, _, _ := contractFixture(t)
	p.Reason = "a" + strings.Repeat("\x00", 4095)
	makePlan := func(n int) Plan { copy := p; copy.Target.Home = "/" + strings.Repeat("<", n); return copy }
	low, high := 0, 4095
	for low < high {
		mid := (low + high + 1) / 2
		q := makePlan(mid)
		raw, _ := json.Marshal(q)
		if len(raw) <= MaxPlanBytes && planFrameBudget(q) == nil {
			low = mid
		} else {
			high = mid - 1
		}
	}
	good, bad := makePlan(low), makePlan(low+1)
	raw, _ := json.Marshal(bad)
	if len(raw) > MaxPlanBytes || planFrameBudget(bad) == nil {
		t.Fatal("control did not isolate later-envelope overflow below unchanged Plan bound", len(raw), low)
	}
	boundary, err := NewBoundary(bad.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = boundary.Prepare(bad.Key, bad.Actor, bad.DesiredRevision, bad.Policy, bad.Reason, bad.Impact, bad.Observed); err == nil {
		t.Fatal("unrepresentable completion received a permit")
	}
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "node_stop_scope_incomplete" || len(boundary.elapsed.issued) != 0 || boundary.elapsed.have {
		t.Fatal("frame refusal occurred after permit/clock issuance", err)
	}
	boundary, err = NewBoundary(good.Target)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := boundary.Prepare(good.Key, good.Actor, good.DesiredRevision, good.Policy, good.Reason, good.Impact, good.Observed)
	if err != nil {
		t.Fatal("near-boundary valid shape refused", err)
	}
	hash, err := prepared.Digest()
	if err != nil {
		t.Fatal(err)
	}
	admission, err := boundary.Admit(Apply{Key: prepared.Key, Plan: prepared, ReviewSHA256: hash}, Current{Target: prepared.Target, Actor: prepared.Actor, NodeStopGranted: true, DesiredRevision: prepared.DesiredRevision, DesiredRunning: true, Impact: prepared.Impact}, prepared.Observed)
	if err != nil || !boundary.Owns(admission) {
		t.Fatal("valid near-boundary shape did not pass real admission", err)
	}
	admitted, err := admission.Plan()
	if err != nil {
		t.Fatal(err)
	}
	prepared = admitted
	record := Record{Schema: RecordSchema, Plan: prepared, Digest: hash, Shutdown: strings.Repeat("f", 32), DesiredRevision: prepared.DesiredRevision + 1, Phase: "drain-ready", Created: prepared.Observed, Updated: prepared.Expires}
	state := State{Schema: StateSchema, Revision: record.DesiredRevision, Running: false, Target: prepared.Target, Key: prepared.Key, Digest: hash}
	observation := Observation{Record: record, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}
	review := SourceReview{Operation: "stop-arm-node", Key: prepared.Key, Plan: prepared, SHA256: hash, OwnerUsername: strings.Repeat("\x00", 256), HistoryCapacity: MaxRecords, CompletionCheck: CompletionCheck{Available: true, Detail: strings.Repeat("\x00", MaxDiagnosticBytes)}}
	result := Result{Operation: "stop-arm-node", Key: prepared.Key, Status: "stopped", Observation: &observation, Detail: strings.Repeat("\x00", MaxDiagnosticBytes)}
	if err = result.Validate(review); err != nil {
		t.Fatal("typed maximal diagnostic composition refused", err)
	}
	for _, frame := range []struct {
		value  any
		target any
	}{{prepared, &Plan{}}, {Prepared{Plan: prepared, SHA256: hash, OwnerUsername: review.OwnerUsername, HistoryCapacity: MaxRecords}, &Prepared{}}, {Apply{Key: prepared.Key, ReviewSHA256: hash, Plan: prepared}, &Apply{}}, {record, &Record{}}, {state, &State{}}, {observation, &Observation{}}, {review, &SourceReview{}}, {result, &Result{}}} {
		raw, err := json.Marshal(frame.value)
		if err != nil || len(raw) > MaxFrameBytes {
			t.Fatal("later typed envelope does not fit", len(raw), err)
		}
		if err = Decode(raw, frame.target); err != nil {
			t.Fatal("complete later envelope cannot round trip", err)
		}
	}
	storedReview, err := StoredReviewFrame(review)
	if err != nil {
		t.Fatal(err)
	}
	var decodedReview SourceReview
	if err = Decode(storedReview, &decodedReview); err != nil || decodedReview.Validate() != nil {
		t.Fatal("exact stored review cannot round trip", len(storedReview), err)
	}
	storedResult, err := StoredResultFrame(result)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResult Result
	if err = Decode(storedResult, &decodedResult); err != nil || decodedResult.Validate(decodedReview) != nil {
		t.Fatal("exact stored completion cannot round trip", len(storedResult), err)
	}
	if len(storedResult) < MaxFrameBytes-256 || len(storedResult) > MaxFrameBytes {
		t.Fatal("typed result did not reach intended actual stored-envelope boundary", len(storedResult), low)
	}
	ambiguous := result
	ambiguous.Record = &record
	if ambiguous.Validate(review) == nil {
		t.Fatal("duplicate completed record accepted")
	}
	result.Detail += "x"
	if result.Validate(review) == nil {
		t.Fatal("unbudgeted diagnostic extension accepted")
	}
}
