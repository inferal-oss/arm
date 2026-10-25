package nodestop

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const MaxFrameBytes = 64 << 10

// Producers use fixed messages (currently under 300 bytes). This bounded
// diagnostic allowance is separate from the full 4096-byte operator reason.
const MaxDiagnosticBytes = 512

// planFrameBudget is pure encoded-size accounting, not authority or a native
// observation. No constructor, Validate, Digest or clock is invoked here. Its
// upper-bound shapes cover every later independently decoded stop envelope,
// including the target repeated in desired state and escaped diagnostic bytes.
func planFrameBudget(p Plan) error {
	hash := strings.Repeat("f", 64)
	stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("size-only", 23*3600+59*60))
	record := Record{Schema: RecordSchema, Plan: p, Digest: hash, Shutdown: strings.Repeat("f", 32), DesiredRevision: MaxRevision, Phase: "interrupted", Created: stamp, Updated: stamp}
	completed := record
	completed.Phase = "drain-ready"
	state := State{Schema: StateSchema, Revision: MaxRevision, Running: false, Target: p.Target, Key: p.Key, Digest: hash}
	observation := Observation{Record: completed, Desired: state, ServerJoined: true, OriginalGenerationAbsent: true}
	diagnostic := strings.Repeat("\x00", MaxDiagnosticBytes) // maximal JSON escaping per input byte
	name := strings.Repeat("\x00", 256)
	review := SourceReview{Operation: "stop-arm-node", Key: p.Key, Plan: p, SHA256: hash, OwnerUsername: name, HistoryUsed: MaxRecords, HistoryCapacity: MaxRecords, CompletionCheck: CompletionCheck{Detail: diagnostic}}
	receipt := Result{Operation: "stop-arm-node", Key: p.Key, Status: "draining", Record: &record, Detail: diagnostic}
	result := Result{Operation: "stop-arm-node", Key: p.Key, Status: "stopped", Observation: &observation, Detail: diagnostic}
	frames := []any{
		Apply{Key: p.Key, ReviewSHA256: hash, Plan: p},
		Prepared{Plan: p, SHA256: hash, OwnerUsername: name, HistoryUsed: MaxRecords, HistoryCapacity: MaxRecords},
		review,
		record, state, observation,
		receipt,
		result,
	}
	for _, frame := range frames {
		raw, err := json.Marshal(frame)
		if err != nil || len(raw) > MaxFrameBytes {
			return model.Err("node_stop_scope_incomplete", "Stop review cannot fit every bounded receipt/completion envelope; no permit or stop was admitted")
		}
	}
	// The lifecycle journal reindents direct RawMessage fields before saving.
	// Their exact extracted stored representation is decoded later with the same
	// 64KiB bound, so compact fit alone cannot authorize this future obligation.
	storedReview, reviewErr := StoredReviewFrame(review)
	storedReceipt, receiptErr := StoredResultFrame(receipt)
	storedResult, resultErr := StoredResultFrame(result)
	if reviewErr != nil || receiptErr != nil || resultErr != nil || len(storedReview) > MaxFrameBytes || len(storedReceipt) > MaxFrameBytes || len(storedResult) > MaxFrameBytes {
		return model.Err("node_stop_scope_incomplete", "Stop review cannot fit its exact stored review/completion frame; no permit or stop was admitted")
	}
	return nil
}

// These pure typed encoders mirror Coordination's direct top-level RawMessage
// fields and mesh.save's exact empty-prefix/two-space MarshalIndent format.
// They do not validate, issue a permit, observe work, or confer any authority.
func StoredReviewFrame(review SourceReview) (json.RawMessage, error) {
	return storedFrame(struct {
		Review SourceReview `json:"review"`
	}{review}, "review")
}
func StoredResultFrame(result Result) (json.RawMessage, error) {
	return storedFrame(struct {
		Result Result `json:"result"`
	}{result}, "result")
}
func storedFrame(wrapper any, field string) (json.RawMessage, error) {
	raw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return nil, err
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	if len(values) != 1 || len(values[field]) == 0 {
		return nil, model.Err("node_stop_scope_incomplete", "Exact stored Stop field is unavailable")
	}
	return values[field], nil
}

func encodedFrameFits(value any) bool {
	raw, err := json.Marshal(value)
	return err == nil && len(raw) <= MaxFrameBytes
}
