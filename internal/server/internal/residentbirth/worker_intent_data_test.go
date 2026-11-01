package residentbirth

import (
	"context"
	"getarm.org/internal/residentrefs"
	"testing"
)

func TestWorkerIntentDataRefusesUnissuedCopiedAndMetadataOnlyFence(t *testing.T) {
	p := &WorkerInheritedFencePending{state: &workerInheritedFenceState{rootContext: context.Background(), custody: &residentrefs.FenceOriginal{}, verified: true, verifyTried: true, intent: []byte("synthetic DATA")}}
	p.state.self = p
	copy := *p
	for _, value := range []*WorkerInheritedFencePending{nil, {}, p, &copy} {
		if raw, err := value.IntentOriginalData(); err == nil || raw != nil {
			t.Fatal("unissued fence supplied original intent DATA")
		}
	}
	if p.state.intentCopies != 0 || p.state.active != nil || p.state.journal != nil {
		t.Fatal("refusal entered native work or allocated intent copy")
	}
}

func TestWorkerIntentSnapshotDataIsBoundedAndMutationIsolated(t *testing.T) {
	original := []byte("synthetic immutable birth intent DATA")
	first, err := workerIntentSnapshotData(original)
	if err != nil {
		t.Fatal(err)
	}
	second, err := workerIntentSnapshotData(original)
	if err != nil {
		t.Fatal(err)
	}
	first[0] ^= 1
	if original[0] != second[0] || first[0] == original[0] {
		t.Fatal("caller mutation altered original or another snapshot")
	}
	original[1] ^= 1
	if first[1] != second[1] || first[1] == original[1] {
		t.Fatal("snapshot borrowed mutable original backing")
	}
	for _, raw := range [][]byte{nil, {}, make([]byte, residentrefs.MaxWireBytes-35)} {
		if got, err := workerIntentSnapshotData(raw); err == nil || got != nil {
			t.Fatal("empty/oversized DATA copy accepted")
		}
	}
	if got, err := workerIntentSnapshotData(make([]byte, residentrefs.MaxWireBytes-36)); err != nil || len(got) != residentrefs.MaxWireBytes-36 {
		t.Fatal("exact copy bound refused", err)
	}
}
