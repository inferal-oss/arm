package server

import (
	"context"
	"os/exec"
	"sync"
	"testing"
)

func TestResidentParentProtocolUnissuedAndCopiedRefuseBeforePhaseEffects(t *testing.T) {
	root := &residentParentPending{mu: &sync.Mutex{}}
	root.self = root
	copied := *root
	for _, tc := range []struct {
		name string
		root *residentParentPending
	}{
		{"nil", nil}, {"zero", &residentParentPending{}}, {"unissued", root}, {"copied", &copied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.root.beginParentProtocolOriginal(); err != errResidentParentOriginal {
				t.Fatal("unissued parent entered protocol", err)
			}
			if _, err := tc.root.beginParentProtocolPhase(nil, 0); err != errResidentParentOriginal {
				t.Fatal("unissued parent constructed a phase", err)
			}
			if err := tc.root.exchangeParentProtocolOriginal(); err != errResidentParentOriginal {
				t.Fatal("unissued parent exchanged", err)
			}
		})
	}
	if root.protocol != nil || root.active != nil || root.protocolPhases != ([4]*residentProtocolPhase{}) || root.startCall != nil || root.startAttempted {
		t.Fatal("refusal changed parent launch state")
	}
}

func TestResidentParentProtocolFullWriteAndStartFlagsCannotAuthorizePhase(t *testing.T) {
	for _, name := range []string{"full-write", "start-returned", "detached-call"} {
		t.Run(name, func(t *testing.T) {
			root := &residentParentPending{mu: &sync.Mutex{}, transferWritten: true}
			root.self = root
			op := &residentParentOperation{ctx: context.Background(), kind: "launch-and-transfer"}
			root.active, root.launchOperation = op, op
			command := &exec.Cmd{}
			call := &residentParentProtocolCall{owner: root, op: op, ctx: op.ctx, command: command}
			if name != "full-write" {
				root.startAttempted, root.startReturned = true, true
				root.command = command
			}
			if name != "detached-call" {
				root.protocol = call
			}
			if _, err := root.beginParentProtocolPhase(call, 0); err != errResidentParentOriginal {
				t.Fatal("flags replaced actual original Start/owners", err)
			}
			if root.protocolPhases != ([4]*residentProtocolPhase{}) || call.entropy.attempted || call.ackAuth.attempted || call.serviceCurrent.attempted {
				t.Fatal("weak DATA reached protocol effects")
			}
		})
	}
}
