package nodestop

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Restart is a distinct explicit review. It neither replays the old stop nor
// claims its obligations/completion settled. Automatic starters have no permit.
type Restart struct {
	Schema   string    `json:"schema"`
	Expected State     `json:"stopped_intent"`
	SHA256   string    `json:"stopped_intent_sha256"`
	Reason   string    `json:"reason"`
	Observed time.Time `json:"observed_at"`
	Expires  time.Time `json:"expires_at"`
}

func ReviewRestart(state State, record Record, reason string, now time.Time) (Restart, error) {
	q := Restart{Schema: "arm.node-stop.restart/v1", Expected: state, Reason: reason, Observed: now.UTC(), Expires: now.UTC().Add(ReviewTTL)}
	hash, err := state.StateDigest()
	if err != nil {
		return q, err
	}
	q.SHA256 = hash
	return q, q.Validate(state, record, state.Target.Owner, now)
}

func (q Restart) Validate(current State, record Record, owner string, now time.Time) error {
	if err := current.Validate(record); err != nil {
		return err
	}
	hash, err := current.StateDigest()
	if err != nil || q.Schema != "arm.node-stop.restart/v1" || q.Expected != current || q.SHA256 != hash || current.Target.Owner != owner || current.Running || current.Revision >= MaxRevision || strings.TrimSpace(q.Reason) == "" || len(q.Reason) > 4096 || !utf8.ValidString(q.Reason) || q.Observed.IsZero() || !q.Expires.Equal(q.Observed.Add(ReviewTTL)) || now.Before(q.Observed) || !now.Before(q.Expires) {
		return fmt.Errorf("exact current stopped revision, native owner and fresh explicit restart review required")
	}
	return nil
}
