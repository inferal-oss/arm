package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"getarm.org/internal/residentrefs"
)

func TestP1CurrentServiceJournalDataRequiresExactBirthAndThirdFrameChain(t *testing.T) {
	records := dataRecords(t) // serialized DATA, no actual grant or native holder
	birth := encodeData(t, records[:2])
	current := encodeData(t, records)
	third := current[len(birth):]
	h := sha256.Sum256(third)
	expected := P1RootBoundData{Record: *records[2].Root, JournalBytes: int64(len(current)), JournalSHA256: hex.EncodeToString(h[:])}
	if got, err := validateP1CurrentServiceJournalData(current, birth, third, expected); err != nil || len(got.records) != 3 || got.last != h {
		t.Fatal("valid DATA chain refused", err)
	}
	for _, name := range []string{"empty", "overflow", "truncated", "extended", "birth", "third", "count", "whole-hash", "record"} {
		data := append([]byte(nil), current...)
		prefix := append([]byte(nil), birth...)
		suffix := append([]byte(nil), third...)
		want := expected
		switch name {
		case "empty":
			data = nil
		case "overflow":
			data = make([]byte, MaxJournalBytes+1)
		case "truncated":
			data = data[:len(data)-1]
		case "extended":
			data = append(data, 'x')
		case "birth":
			prefix[0] ^= 1
		case "third":
			suffix[len(suffix)-1] ^= 1
		case "count":
			want.JournalBytes++
		case "whole-hash":
			all := sha256.Sum256(data)
			want.JournalSHA256 = hex.EncodeToString(all[:])
		case "record":
			want.Record.Generation = "wrong"
		}
		if _, err := validateP1CurrentServiceJournalData(data, prefix, suffix, want); err != ErrInvalid {
			t.Fatal("mutated current journal DATA accepted", name, err)
		}
	}
	if !bytes.Equal(birth, current[:len(birth)]) {
		t.Fatal("DATA helper changed original birth bytes")
	}
}

func TestP1CurrentServiceRequiresActualPreparedAuthBeforeEffects(t *testing.T) {
	child := &ChildPending{}
	o := &owner{child: child}
	child.owner = o
	copyChild := *child
	for _, c := range []*ChildPending{nil, {}, &copyChild, child} {
		var observation *P1CurrentServiceObservation
		if data, err := c.ValidateP1CurrentOriginal(&observation, context.Background(), &CurrentChallengeView{}, &ManagerCharge{}); err != ErrInvalid || observation != nil || data != (P1CurrentServiceData{}) {
			t.Fatal("unprepared current service accepted", err)
		}
	}
	if o.active != nil || o.p1CurrentTried || o.p1Current != nil || o.p1CurrentObservation != nil || o.firstError != nil || len(o.reads) != 0 || o.append != nil {
		t.Fatal("refusal changed birth/read/current state")
	}
	view := &CurrentChallengeView{owner: o}
	view.self = view
	o.active = &operation{owner: o, kind: "p1-current-service"}
	if _, err := view.ValidateOriginal(&ManagerCharge{}); err != ErrInvalid {
		t.Fatal("public active-operation refusal relaxed", err)
	}
}

func TestP1CurrentServiceObservationRejectsJSONAndUnissuedCopies(t *testing.T) {
	child := &ChildPending{}
	o := &owner{child: child}
	child.owner = o
	v := &P1CurrentServiceObservation{owner: o, child: child}
	v.self = v
	copyView := *v
	for _, value := range []any{*v, v, struct{ Observation P1CurrentServiceObservation }{*v}} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("opaque current observation serialized")
		}
	}
	var decoded P1CurrentServiceObservation
	if err := json.Unmarshal([]byte(`{}`), &decoded); err != ErrInvalid || decoded.self != nil || decoded.call != nil {
		t.Fatal("JSON constructed a current observation", err)
	}
	for _, observation := range []*P1CurrentServiceObservation{nil, {}, v, &copyView} {
		if data, err := observation.ValidateOriginalForFence(child, &residentrefs.FenceOriginal{}, &CurrentChallengeView{}, &ManagerCharge{}); err != ErrInvalid || data != (P1CurrentServiceData{}) {
			t.Fatal("unissued/copied current observation accepted", err)
		}
	}
	if o.active != nil || o.firstError != nil || o.p1CurrentTried || len(o.reads) != 0 {
		t.Fatal("pure observation changed original state")
	}
}
