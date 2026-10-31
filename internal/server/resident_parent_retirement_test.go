package server

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
)

func TestResidentParentRetirementRequiresCompletedOriginalHandoff(t *testing.T) {
	original := &residentParentPending{mu: &sync.Mutex{}}
	original.self = original
	copied := *original
	for name, root := range map[string]*residentParentPending{"nil": nil, "zero": {}, "unissued": original, "copied": &copied} {
		t.Run(name, func(t *testing.T) {
			if err := root.retireParentReferencesOriginal(context.Background()); err != errResidentParentOriginal {
				t.Fatalf("refusal = %v", err)
			}
		})
	}
	if original.active != nil || original.retirement != nil || original.firstError != nil {
		t.Fatal("refusal changed original custody")
	}
}

func TestResidentParentRetirementStartAndProtocolFlagsCannotAuthorize(t *testing.T) {
	r := &residentParentPending{mu: &sync.Mutex{}, transferWritten: true, startAttempted: true, startReturned: true, prepared: true}
	r.self = r
	r.process = &os.Process{Pid: 1} // inert DATA; no native handle lookup
	r.command = &exec.Cmd{Process: r.process}
	r.protocol = &residentParentProtocolCall{owner: r, process: r.process, command: r.command, bodyReturned: true, normalReturned: true}
	if err := r.retireParentReferencesOriginal(context.Background()); err != errResidentParentOriginal {
		t.Fatalf("refusal = %v", err)
	}
	if r.active != nil || r.retirement != nil || r.firstError != nil || r.process.Pid != 1 {
		t.Fatal("unissued handoff reached retirement or handle disposition")
	}
}

func TestResidentParentRetirementGateRefusesUnissuedBeforeDisposition(t *testing.T) {
	r := &residentParentPending{mu: &sync.Mutex{}}
	r.self = r
	c := &residentParentRetirementCall{owner: r}
	for _, stage := range []int{-1, 0, 1, 2, 3, 4, 5} {
		if err := r.parentRetirementGate(c, stage); err != errResidentParentOriginal {
			t.Fatalf("stage %d = %v", stage, err)
		}
	}
	if c.pinClose.attempted || c.fenceClose.attempted || c.serviceClose.attempted || c.controlClose.attempted || c.release.attempted {
		t.Fatal("unissued gate attempted a disposition")
	}
}
