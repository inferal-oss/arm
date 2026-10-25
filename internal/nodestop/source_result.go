package nodestop

import (
	"fmt"
	"unicode/utf8"
)

// SourceReview is the exact operator-facing destination plan plus observed
// presentation facts. It conveys no grant or elapsed capability of its own.
type SourceReview struct {
	Operation       string          `json:"operation"`
	Key             string          `json:"key"`
	Plan            Plan            `json:"plan"`
	SHA256          string          `json:"review_sha256"`
	OwnerUsername   string          `json:"owner_username"`
	HistoryUsed     int             `json:"history_used"`
	HistoryCapacity int             `json:"history_capacity"`
	CompletionCheck CompletionCheck `json:"completion_check"`
}
type CompletionCheck struct {
	Available bool   `json:"available"`
	Detail    string `json:"detail"`
}

// Result is tagged by Status: accepted/draining carry Record; stopped carries
// only Observation.Record. Dual representations are ambiguous and rejected.
type Result struct {
	Operation   string       `json:"operation"`
	Key         string       `json:"key"`
	Status      string       `json:"status"`
	Record      *Record      `json:"record,omitempty"`
	Observation *Observation `json:"observation,omitempty"`
	Detail      string       `json:"detail"`
}

func (r SourceReview) Validate() error {
	hash, err := r.Plan.Digest()
	stored, storedErr := StoredReviewFrame(r)
	if err != nil || r.Operation != "stop-arm-node" || r.Key != r.Plan.Key || r.SHA256 != hash || len(r.OwnerUsername) > 256 || r.HistoryUsed < 0 || r.HistoryCapacity != MaxRecords || r.HistoryUsed > r.HistoryCapacity || len(r.CompletionCheck.Detail) > MaxDiagnosticBytes || !utf8.ValidString(r.OwnerUsername+r.CompletionCheck.Detail) || !encodedFrameFits(r) || storedErr != nil || len(stored) > MaxFrameBytes {
		return fmt.Errorf("invalid exact source Stop review")
	}
	return nil
}
func (r Result) Validate(review SourceReview) error {
	if err := review.Validate(); err != nil {
		return err
	}
	stored, storedErr := StoredResultFrame(r)
	if r.Operation != "stop-arm-node" || r.Key != review.Key || len(r.Detail) > MaxDiagnosticBytes || !utf8.ValidString(r.Detail) || !encodedFrameFits(r) || storedErr != nil || len(stored) > MaxFrameBytes {
		return fmt.Errorf("Stop result belongs to another review")
	}
	switch r.Status {
	case "unknown", "blocked":
		if r.Record != nil || r.Observation != nil {
			return fmt.Errorf("unconfirmed result cannot carry a completion/receipt payload")
		}
	case "accepted", "draining":
		if r.Record == nil || r.Observation != nil {
			return fmt.Errorf("accepted result requires original destination record")
		}
	case "stopped":
		if r.Record != nil || r.Observation == nil || !r.Observation.ServerJoined || !r.Observation.OriginalGenerationAbsent {
			return fmt.Errorf("stopped requires positive original generation evidence")
		}
	default:
		return fmt.Errorf("unknown Stop result state")
	}
	check := func(record Record) error {
		if err := record.Validate(); err != nil {
			return err
		}
		if record.Digest != review.SHA256 || record.Plan.Target != review.Plan.Target || record.Plan.Actor != review.Plan.Actor || record.Plan.Key != review.Key {
			return fmt.Errorf("foreign destination stop record")
		}
		return nil
	}
	if r.Record != nil {
		if err := check(*r.Record); err != nil {
			return err
		}
	}
	if r.Observation != nil {
		o := r.Observation
		if err := check(o.Record); err != nil {
			return err
		}
		if err := o.Desired.Validate(o.Record); err != nil {
			return err
		}
		if o.Record.Phase != "drain-ready" || !o.ServerJoined || !o.OriginalGenerationAbsent {
			return fmt.Errorf("incomplete native generation observation")
		}
	}
	return nil
}
