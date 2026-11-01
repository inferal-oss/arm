package residentservice

import (
	"os/exec"
	"testing"
)

// These are unissued metadata controls, not successful native birth/proof cases.
func TestWorkerAckGroupObservationRejectsUnissuedAndCopiedData(t *testing.T) {
	p := workerParentRefusalData(t)
	g := &WorkerServiceGroup{parent: p}
	g.self = g
	p.group = g
	a := &WorkerParentAckAuthentication{group: g}
	a.self = a
	copy := *a
	for _, candidate := range []*WorkerParentAckAuthentication{nil, {}, &copy, a} {
		if _, err := candidate.ValidateOriginalGroup(g, &exec.Cmd{}); err == nil {
			t.Fatal("unissued observation accepted")
		}
	}
	assertWorkerNoNativeData(t, p.custody)
}
func TestWorkerProofGroupObservationRejectsUnissuedAndCopiedData(t *testing.T) {
	p := workerParentRefusalData(t)
	g := &WorkerServiceGroup{parent: p}
	g.self = g
	p.group = g
	a := &WorkerParentAckAuthentication{group: g}
	a.self = a
	proof := &WorkerParentProofAuthentication{group: g}
	proof.self = proof
	copy := *proof
	for _, candidate := range []*WorkerParentProofAuthentication{nil, {}, &copy, proof} {
		if candidate.ValidateOriginalGroup(g, &exec.Cmd{}, a) == nil {
			t.Fatal("unissued proof became reference retirement authority")
		}
	}
	assertWorkerNoNativeData(t, p.custody)
}
