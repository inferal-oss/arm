package server

import (
	"errors"
	"testing"

	"getarm.org/internal/meshsshrelay"
)

func TestForwardSourceNativeCloseDebtStopsCandidateRetry(t *testing.T) {
	fault := errors.New("first candidate original raw close failed")
	secondCalled := false
	_, _, err := forwardSourceDialCandidates([]graphRouteCandidate{{path: []string{"first"}}, {path: []string{"second"}}}, func(candidate graphRouteCandidate) forwardCandidateOutcome {
		if candidate.path[0] == "first" {
			return forwardCandidateOutcome{err: errors.Join(errors.New("first route refused"), &meshsshrelay.NativeCloseUncertain{Cause: fault})}
		}
		secondCalled = true
		return forwardCandidateOutcome{err: errors.New("second route sentinel must not run")}
	})
	if secondCalled || !errors.Is(err, fault) {
		t.Fatalf("production candidate loop retried after native debt: second=%t err=%v", secondCalled, err)
	}
}
