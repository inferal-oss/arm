package sharelease

import (
	"bytes"
	"strings"
	"testing"
)

func TestJournalFinalizationSpaceReservedBeforeWork(t *testing.T) {
	empty := JournalCapacity{}
	initial, err := empty.Change(500, 0, PublicationFinalSlots)
	if err != nil {
		t.Fatal(err)
	}
	if initial.ReservedSlots != 8 || initial.Rows != 1 {
		t.Fatal(initial)
	}
	// Fill ALL unreserved byte space. Finalization entries can still each take
	// their declared maximum; a new preparation/consumer must be refused first.
	initial.Bytes = MaxJournalBytes - int64(initial.ReservedSlots)*MaxJournalRecordBytes
	if _, err = initial.Change(1, 0, ConsumerFinalSlots); err == nil {
		t.Fatal("admitted work without future finalization bytes")
	}
	next := initial
	for slots := PublicationFinalSlots; slots > 0; slots-- {
		next, err = next.Change(MaxJournalRecordBytes, slots, slots-1)
		if err != nil {
			t.Fatalf("promised finalization%d blocked: %v", slots, err)
		}
	}
	if next.ReservedSlots != 0 || next.Bytes != MaxJournalBytes {
		t.Fatal(next)
	}
	if _, err = next.Change(1, 0, 0); err == nil {
		t.Fatal("overflow after exact full finalization accepted")
	}
}
func TestJournalRowCapacityCannotAdmitUnfinishableWork(t *testing.T) {
	current := JournalCapacity{Rows: MaxJournalRows - 4, ReservedSlots: 4}
	if _, err := current.Change(1, 0, 1); err == nil {
		t.Fatal("admitted beyond exact future row capacity")
	}
	for remaining := 4; remaining > 0; remaining-- {
		next, err := current.Change(1, remaining, remaining-1)
		if err != nil {
			t.Fatal(err)
		}
		current = next
	}
	if current.Rows != MaxJournalRows {
		t.Fatal(current)
	}
}
func TestCapacityRefusalDoesNotAlterCallerSnapshot(t *testing.T) {
	original := JournalCapacity{Rows: 1, Bytes: MaxJournalBytes}
	next, err := original.Change(1, 0, ConsumerFinalSlots)
	if err == nil || next != original {
		t.Fatal("refusal changed existing capacity", next, err)
	}
	if _, err = (JournalCapacity{ReservedSlots: -1}).Change(1, 0, 1); err == nil {
		t.Fatal("accepted corrupt reserved count")
	}
}
func TestJournalIsCanonicalAndBindsExactEntityHash(t *testing.T) {
	p, _, _ := evidenceFixture()
	h, err := hash(p)
	if err != nil {
		t.Fatal(err)
	}
	j := Journal{Schema: 1, Authority: p.Authority, Owner: p.Owner, At: p.Created, Actor: "uid:" + p.Owner, Reason: p.Reason, Entity: "publication", ID: p.ID, Revision: 1, Transition: "reserved", AfterSHA256: h}
	raw, err := EncodeJournal(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeJournal(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"revision":1`), []byte(`"Revision":1`), 1), bytes.Replace(raw, []byte(`"at":`), []byte(`"at":null,"ignored":`), 1)} {
		if _, err := DecodeJournal(bad); err == nil {
			t.Fatal("accepted alias/null journal")
		}
	}
	j.Revision = 2
	if _, err := EncodeJournal(j); err == nil {
		t.Fatal("revision omitted before commitment")
	}
	j.BeforeSHA256 = strings.Repeat("4", 64)
	if _, err := EncodeJournal(j); err != nil {
		t.Fatal(err)
	}
}
func TestManifestReservationDoesNotUsePeerAssertedSmallCapacity(t *testing.T) {
	p, _, _ := evidenceFixture()
	raw, stored, err := ManifestReservation(p)
	if err != nil {
		t.Fatal(err)
	}
	if raw != 8<<20 || stored <= raw {
		t.Fatal("preparation under-reserved complete manifest and base64", raw, stored)
	}
	p.State = "failed"
	p.WorkerState = "joined"
	p.JournalSlots = 0
	at := p.Created
	p.Retired = &at
	p.Failure = "preparation cancelled before native walk"
	raw, stored, err = ManifestReservation(p)
	if err != nil || raw != 0 || stored != 0 {
		t.Fatal(raw, stored, err)
	}
}
