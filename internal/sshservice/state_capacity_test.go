package sshservice

import (
	"errors"
	"fmt"
	"testing"
)

func TestSourceCapacityCountsDistinctFutureObligations(t *testing.T) {
	makeIntent := func(n int) SourceIntent {
		return SourceIntent{Source: "source", Request: SourceRequest{Key: fmt.Sprint(n), Pin: Pin{Target: "destination"}}}
	}
	// Simulate interrupted completion publication: pending + completed copies of
	// the same original tuple reserve one slot, while another destination is a
	// distinct obligation even when its execution key happens to match.
	s := &State{}
	for n := 0; n < MaxCompletions-1; n++ {
		v := makeIntent(n)
		s.Completions = append(s.Completions, SourceCompletion{Intent: v})
	}
	paired := makeIntent(0)
	s.Intents = []SourceIntent{paired}
	if e := s.CheckSourceCapacity(); e != nil {
		t.Fatal("matching pending/completed pair double-counted", e)
	}
	paired.Request.Pin.Target = "other-destination"
	s.Seals = []SourceSealCompletion{{Intent: paired}}
	if e := s.CheckSourceCapacity(); !errors.Is(e, ErrSourceCapacity) {
		t.Fatal("distinct target tuple did not reserve last completion slot", e)
	}
	s.Seals = nil
	s.Intents = nil
	s.History = &SourceHistorySummary{Busy: []string{"original-operation"}}
	if e := s.CheckSourceCapacity(); !errors.Is(e, ErrHistoryBusy) || errors.Is(e, ErrSourceCapacity) {
		t.Fatal("unfinished archival was confused with definite capacity refusal", e)
	}
	s = &State{}
	for n := 0; n < MaxSourceIntents; n++ {
		s.Intents = append(s.Intents, makeIntent(n))
	}
	if e := s.CheckSourceCapacity(); !errors.Is(e, ErrSourceCapacity) {
		t.Fatal("full pending roster admitted another obligation", e)
	}
}
