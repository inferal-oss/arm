package residentrefs

import "testing"

func TestWorkerFactoryRefusesNilAndOccupiedBeforeProcessClaim(t *testing.T) {
	processChildClaim.mu.Lock()
	attempted, original := processChildClaim.attempted, processChildClaim.original
	processChildClaim.mu.Unlock()
	if err := NewFixedWorkerChildPending(nil); err != ErrOriginal {
		t.Fatal(err)
	}
	occupied := &ChildPending{}
	destination := occupied
	if err := NewFixedWorkerChildPending(&destination); err != ErrOriginal || destination != occupied {
		t.Fatal("occupied original replaced", err)
	}
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	if processChildClaim.attempted != attempted || processChildClaim.original != original {
		t.Fatal("pure rejection consumed or replaced the shared process claim")
	}
}
