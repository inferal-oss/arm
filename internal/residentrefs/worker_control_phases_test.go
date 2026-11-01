package residentrefs

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWorkerChildControlPrefixDataRequiresFourDistinctRoles(t *testing.T) {
	want := []string{"worker-bootstrap-read", "worker-bound-ack-write", "worker-current-challenge-read", "worker-current-proof-write"}
	for next := range want {
		if !workerChildControlPrefixData(want[:next], want[next]) {
			t.Fatal("valid DATA prefix refused", next)
		}
		for other := range want {
			if other != next && workerChildControlPrefixData(want[:next], want[other]) {
				t.Fatal("phase skip/replay accepted")
			}
		}
		for changed := 0; changed < next; changed++ {
			bad := append([]string(nil), want[:next]...)
			bad[changed] = childControlKinds[changed]
			if workerChildControlPrefixData(bad, want[next]) {
				t.Fatal("legacy phase supplied a worker prefix")
			}
		}
	}
	if workerChildControlPrefixData(want, want[0]) || workerChildControlPrefixData(nil, childControlKinds[0]) {
		t.Fatal("exhausted or legacy phase admitted")
	}
}

func TestWorkerControlUnissuedScopeAndCopiesRefuseBeforeEffects(t *testing.T) {
	for _, scope := range []childCoreScope{legacyChildCore, workerChildCore, 255} {
		c := inertChildData() // deliberately unissued, never seals descriptors
		c.state.scope = scope
		service, _, _, control, err := c.NewOriginalRecipients()
		if err != nil {
			t.Fatal(err)
		}
		copyControl := *control
		for _, source := range []*ControlOriginal{nil, {}, control, &copyControl} {
			if _, err := source.ReceiveWorkerBootstrapOriginal(context.Background()); err != ErrOriginal {
				t.Fatal("unissued bootstrap admitted", err)
			}
			if err := source.SendWorkerBoundAckOriginal(context.Background(), nil); err != ErrOriginal {
				t.Fatal("unissued bound-ack admitted", err)
			}
			var read *WorkerChallengeRead
			if err := source.ReceiveWorkerCurrentChallengeOriginal(&read, context.Background()); err != ErrOriginal || read != nil {
				t.Fatal("unissued challenge read published", err)
			}
			if err := source.SendWorkerCurrentProofOriginal(context.Background(), nil); err != ErrOriginal {
				t.Fatal("unissued proof admitted", err)
			}
		}
		read := &WorkerChallengeRead{}
		read.self = read
		copyRead := *read
		for _, unissued := range []*WorkerChallengeRead{nil, {}, read, &copyRead} {
			if err := service.ValidateOriginalWorkerChallengePhase(unissued); err != ErrOriginal {
				t.Fatal("unissued challenge admitted", err)
			}
			if _, err := service.OriginalWorkerChallengePayloadData(unissued, make([]byte, 37)); err != ErrOriginal {
				t.Fatal("unissued challenge exposed data", err)
			}
		}
		if c.state.active != nil || c.state.firstError != nil {
			t.Fatal("refusal changed original operation state")
		}
		for index, phase := range control.state.workerControlPhases {
			if phase != nil || control.state.controlPhases[index] != nil {
				t.Fatal("refusal consumed old or worker phase")
			}
		}
		for _, entry := range c.state.entries {
			if entry.file != nil || entry.cloexecTried || entry.materializeTried || entry.closeTried {
				t.Fatal("unissued control reached native work")
			}
		}
	}
}

func TestWorkerChallengeReadCannotBeSerializedOrDecoded(t *testing.T) {
	read := WorkerChallengeRead{}
	for _, value := range []any{read, &read, struct{ Read WorkerChallengeRead }{read}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("opaque worker read serialized")
		}
	}
	if err := json.Unmarshal([]byte(`{}`), &read); err != ErrOriginal || read.self != nil || read.control != nil || read.call != nil {
		t.Fatal("DATA constructed an original worker read", err)
	}
}
