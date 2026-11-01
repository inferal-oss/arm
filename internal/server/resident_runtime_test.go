package server

import (
	"sync"
	"testing"
)

func TestResidentRuntimeRequiresActualEntryAndCompletedOriginalProtocol(t *testing.T) {
	r := &residentChildPending{mu: &sync.Mutex{}}
	r.self = r
	copied := *r
	for name, root := range map[string]*residentChildPending{"nil": nil, "zero": {}, "unissued": r, "copied": &copied} {
		t.Run(name, func(t *testing.T) {
			if err := root.startOriginalRuntime(); err != errResidentChildOriginal {
				t.Fatalf("runtime refusal = %v", err)
			}
		})
	}
	if r.active != nil || r.runtimeCall != nil || r.firstError != nil {
		t.Fatal("refusal changed original root")
	}
}

func TestResidentIngressUnissuedCopiedStartRefusesBeforeEffects(t *testing.T) {
	p := &residentIngressPending{}
	p.self = p
	// Copy identity DATA without copying a live synchronization primitive.
	copied := residentIngressPending{self: p}
	for name, owner := range map[string]*residentIngressPending{"nil": nil, "zero": {}, "unissued": p, "copied": &copied} {
		t.Run(name, func(t *testing.T) {
			if err := owner.StartOriginal(); err != errResidentServerOriginal {
				t.Fatalf("ingress refusal = %v", err)
			}
		})
	}
	if p.start != nil || p.listener != nil || p.publication != nil || p.httpServer != nil || p.serve != nil {
		t.Fatal("unissued ingress reached effects")
	}
}

func TestResidentIngressKnownLogicalBoundsData(t *testing.T) {
	b := residentIngressRequiredBounds()
	if b.Tasks != 91 || b.Descriptors != 23 || b.Children != 0 || b.Sockets != 10 || b.NativePending != 91 {
		t.Fatalf("logical owners = %+v", b)
	}
	// Two64KiB copies for64calls plus8readers; queued/writing/projection/encoding
	// response reservations; three16KiB publication copies. DATA only, never RSS.
	want := uint64(2*(64+8)*65536 + (8*65+2*64)*16384 + 3*16384)
	if b.IOBytes != want || b.RetainedReceiptBytes != want {
		t.Fatalf("payload reservation = %+v, want %d", b, want)
	}
}
