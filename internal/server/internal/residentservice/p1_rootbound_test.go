package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

func TestP1RootBoundFrameDataPreservesCanonicalChainAndExactEscapedKey(t *testing.T) {
	records := dataRecords(t) // serialized DATA only, never a native grant/fixture
	key := strings.Repeat("\x01", 128)
	records[1].Debit.LaunchKey = key
	records[2].Root.LaunchKey = key
	prefix := encodeData(t, records[:2])
	decoded, err := decodeJournal(prefix)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeP1RootBoundFrameData(*records[2].Root, hex.EncodeToString(decoded.last[:]))
	if err != nil || len(frame) > MaxP1RootBoundFrameBytes {
		t.Fatal(len(frame), err)
	}
	journal := append(append([]byte(nil), prefix...), frame...)
	current, err := decodeJournal(journal)
	if err != nil || len(current.records) != 3 || current.records[2].Root.LaunchKey != key || current.last != sha256.Sum256(frame) || !bytes.Equal(prefix, journal[:len(prefix)]) {
		t.Fatal("canonical DATA chain/key/prefix", err)
	}
	for _, name := range []string{"key-overflow", "generation", "digest", "previous"} {
		t.Run(name, func(t *testing.T) {
			bad := *records[2].Root
			previous := hex.EncodeToString(decoded.last[:])
			switch name {
			case "key-overflow":
				bad.LaunchKey = strings.Repeat("x", 129)
			case "generation":
				bad.Generation = strings.Repeat("g", 32)
			case "digest":
				bad.CommitmentSHA256 = strings.Repeat("A", 64)
			case "previous":
				previous = "short"
			}
			if _, err := EncodeP1RootBoundFrameData(bad, previous); err != ErrInvalid {
				t.Fatal(err)
			}
		})
	}
}
func TestP1RootBoundCapacityDataRejectsJournalAndKeyOverflow(t *testing.T) {
	key := strings.Repeat("\x01", 128)
	if n, err := P1RootBoundJournalCapacityData(MaxJournalBytes-8192, key); err != nil || n > MaxJournalBytes {
		t.Fatal(n, err)
	}
	for _, n := range []int64{0, -1, MaxJournalBytes, MaxJournalBytes + 1, math.MaxInt64} {
		if _, err := P1RootBoundJournalCapacityData(n, key); err != ErrInvalid {
			t.Fatal(n, err)
		}
	}
	if _, err := P1RootBoundJournalCapacityData(1, strings.Repeat("x", 129)); err != ErrInvalid {
		t.Fatal(err)
	}
}
func TestP1RootBoundRequiresActualPreparedCallBeforeEffects(t *testing.T) {
	h := strings.Repeat("a", 64)
	for _, c := range []*ChildPending{nil, {}, {owner: &owner{}}} {
		if err := c.CommitP1RootBoundOriginal(context.Background(), &residentrefs.ServiceOriginal{}, &mesh.RuntimeExecutable{}, &ManagerCharge{}, strings.Repeat("a", 32), h); err != ErrInvalid {
			t.Fatal(err)
		}
		if _, err := c.P1RootBoundOriginalData(); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	c := &ChildPending{}
	o := &owner{child: c, p1Child: &residentrefs.ServiceOriginal{}}
	c.owner = o
	if err := c.CommitRootBoundObservation(context.Background(), strings.Repeat("a", 32), h); err != ErrInvalid {
		t.Fatal("P1 entered birth-overwriting legacy append", err)
	}
	if err := c.CommitP1RootBoundOriginal(context.Background(), o.p1Child, nil, nil, strings.Repeat("a", 32), h); err != ErrInvalid {
		t.Fatal(err)
	}
	copyChild := *c
	if err := copyChild.CommitP1RootBoundOriginal(context.Background(), o.p1Child, nil, nil, "", ""); err != ErrInvalid {
		t.Fatal(err)
	}
	if o.active != nil || o.append != nil || o.p1RootBound != nil || o.p1RootBoundTried || len(o.reads) != 0 || len(o.journal.records) != 0 {
		t.Fatal("refusal changed original birth/read/append/native state")
	}
}

func TestP1LaunchCapacityRequiresActualOpenedUnusedReadBeforeSelectors(t *testing.T) {
	for _, p := range []*ParentPending{nil, {}, {owner: &owner{}}} {
		if _, err := p.P1LaunchInputsOriginalData(); err != ErrInvalid {
			t.Fatal(err)
		}
	}
	p := &ParentPending{}
	o := &owner{parent: p, buildFromOriginalGrant: true}
	p.owner = o
	if _, err := p.P1LaunchInputsOriginalData(); err != ErrInvalid {
		t.Fatal(err)
	}
	copyParent := *p
	if _, err := copyParent.P1LaunchInputsOriginalData(); err != ErrInvalid {
		t.Fatal(err)
	}
	if o.active != nil || o.openTried || o.debitTried || len(o.reads) != 0 || o.append != nil {
		t.Fatal("preflight selector performed read/open/debit work")
	}
}
