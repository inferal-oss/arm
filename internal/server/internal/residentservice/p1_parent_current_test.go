package residentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"getarm.org/internal/residentprotocol"
)

func parentCurrentJournalDataOnly(t *testing.T) ([]byte, []byte, *GrantRecord, *DebitRecord, residentprotocol.ContextData) {
	t.Helper()
	records := dataRecords(t) // SYNTHETIC serialized DATA only, no native holder
	birth := encodeData(t, records[:2])
	current := encodeData(t, records)
	h := sha256.Sum256(current[len(birth):])
	grant, debit, bound := records[0].Grant, records[1].Debit, records[2].Root
	hash := strings.Repeat("a", 64)
	c := residentprotocol.ContextData{Generation: bound.Generation, DebitID: debit.ID, LaunchKeySHA256: p1OriginalKeySHAData(debit.LaunchKey), ProfileSHA256: debit.ProfileSHA256, ImageSHA256: grant.Intent.Profile.Build.SHA256, ImageBytes: grant.Intent.Profile.Build.Size, SettingsSHA256: hash, EnvironmentSHA256: hash, IntentSHA256: hash, PreparedCommitmentSHA256: bound.CommitmentSHA256, ServiceJournalBytes: int64(len(current)), ServiceJournalSHA256: hex.EncodeToString(h[:]), FooterBytes: 53, FooterSHA256: hash, LaunchJournalBytes: 54, LaunchJournalSHA256: hash}
	return current, birth, grant, debit, c
}

func TestP1ParentCurrentJournalDataRequiresOwnedBirthDebitAndThirdFrame(t *testing.T) {
	current, birth, grant, debit, c := parentCurrentJournalDataOnly(t)
	j, err := validateParentCurrentJournalData(current, birth, grant, debit, c)
	if err != nil || len(j.records) != 3 || j.bytes != int64(len(current)) {
		t.Fatal("valid serialized DATA refused", err)
	}
	for _, name := range []string{"empty", "overflow", "truncated", "extended", "birth", "third", "count", "whole-hash", "generation", "commitment", "owned-grant", "owned-debit", "key", "profile", "image-size"} {
		t.Run(name, func(t *testing.T) {
			data, prefix, ownGrant, ownDebit, expected := parentCurrentJournalDataOnly(t)
			g, d := *ownGrant, *ownDebit
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
				data[len(data)-1] ^= 1
			case "count":
				expected.ServiceJournalBytes++
			case "whole-hash":
				h := sha256.Sum256(data)
				expected.ServiceJournalSHA256 = hex.EncodeToString(h[:])
			case "generation":
				expected.Generation = strings.Repeat("e", 32)
			case "commitment":
				expected.PreparedCommitmentSHA256 = strings.Repeat("b", 64)
			case "owned-grant":
				g.ID = strings.Repeat("f", 64)
			case "owned-debit":
				d.SecretCommitment = strings.Repeat("f", 64)
			case "key":
				expected.LaunchKeySHA256 = strings.Repeat("b", 64)
			case "profile":
				expected.ProfileSHA256 = strings.Repeat("b", 64)
			case "image-size":
				expected.ImageBytes++
			}
			if _, err := validateParentCurrentJournalData(data, prefix, &g, &d, expected); err != ErrInvalid {
				t.Fatal("mutated serialized DATA accepted", err)
			}
		})
	}
	if _, err := validateParentCurrentJournalData(current, birth, nil, debit, c); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := validateParentCurrentJournalData(current, birth, grant, nil, c); err != ErrInvalid {
		t.Fatal(err)
	}
}

func TestP1ParentServiceProtocolRefusesBeforeUnpreparedEffects(t *testing.T) {
	command := &exec.Cmd{} // pure inert object; never Start, Process, Wait or IO
	parent := &ParentPending{}
	o := &owner{parent: parent}
	parent.owner = o
	p := &P1ServiceGroup{p1ServiceState: &p1ServiceState{parent: parent, command: command}}
	p.self = p
	o.p1 = p
	copyGroup := *p
	read := &P1ParentRead{group: p}
	read.self = read
	read.call = &p1ParentControlCall{owner: p, index: parentBoundAck, read: read}
	p.controlPhases[parentBoundAck] = read.call // pending, no op/endpoint/result
	proofRead := &P1ParentRead{group: p}
	proofRead.self = proofRead
	proofRead.call = &p1ParentControlCall{owner: p, index: parentProof, read: proofRead}
	p.controlPhases[parentProof] = proofRead.call
	for _, group := range []*P1ServiceGroup{nil, {}, &copyGroup, p} {
		var ack *P1ParentAckObservation
		if err := group.AuthenticateP1BoundAckOriginal(&ack, context.Background(), command, read); err != ErrInvalid || ack != nil {
			t.Fatal("unprepared Ack auth accepted", err)
		}
		var current *P1ParentCurrentObservation
		if data, err := group.ValidateP1ParentCurrentOriginal(&current, context.Background(), command, &P1ParentAckObservation{}); err != ErrInvalid || current != nil || data != (P1ParentCurrentServiceData{}) {
			t.Fatal("unprepared read6 accepted", err)
		}
		if frame, err := group.SignP1CurrentChallengeOriginal(context.Background(), command, &P1ParentAckObservation{}, &P1ParentCurrentObservation{}, [32]byte{}); err != ErrInvalid || frame != nil {
			t.Fatal("unprepared challenge accepted", err)
		}
		if data, err := group.AuthenticateP1CurrentProofOriginal(context.Background(), command, proofRead); err != ErrInvalid || data != (residentprotocol.CurrentProofBodyData{}) {
			t.Fatal("unprepared proof auth accepted", err)
		}
	}
	if o.active != nil || o.firstError != nil || len(o.reads) != 0 || o.append != nil || p.parentAckAuthTried || p.parentCurrentTried || p.parentChallengeSignTried || p.parentProofAuthTried || p.parentAckAuth != nil || p.parentCurrent != nil || p.parentChallengeSign != nil || p.parentProofAuth != nil {
		t.Fatal("pre-effect refusal mutated original state")
	}
}

func TestP1ParentServiceObservationsRefuseCopiesAndJSON(t *testing.T) {
	command := &exec.Cmd{}
	parent := &ParentPending{}
	o := &owner{parent: parent}
	parent.owner = o
	p := &P1ServiceGroup{p1ServiceState: &p1ServiceState{parent: parent, command: command}}
	p.self = p
	o.p1 = p
	a := &P1ParentAckObservation{group: p}
	a.self = a
	c := &P1ParentCurrentObservation{group: p}
	c.self = c
	copyAck, copyCurrent := *a, *c
	for _, v := range []any{*a, a, *c, c, struct{ Ack P1ParentAckObservation }{*a}, struct{ Current P1ParentCurrentObservation }{*c}} {
		if _, err := json.Marshal(v); err == nil {
			t.Fatal("opaque observation serialized")
		}
	}
	var decodedAck P1ParentAckObservation
	var decodedCurrent P1ParentCurrentObservation
	if err := json.Unmarshal([]byte(`{}`), &decodedAck); err != ErrInvalid || decodedAck.self != nil || decodedAck.call != nil {
		t.Fatal("JSON constructed Ack", err)
	}
	if err := json.Unmarshal([]byte(`{}`), &decodedCurrent); err != ErrInvalid || decodedCurrent.self != nil || decodedCurrent.call != nil {
		t.Fatal("JSON constructed current", err)
	}
	for _, v := range []*P1ParentAckObservation{nil, {}, a, &copyAck} {
		if data, err := v.OriginalData(p, command); err != ErrInvalid || data != (residentprotocol.ContextData{}) {
			t.Fatal("unissued/copied Ack accepted", err)
		}
	}
	for _, v := range []*P1ParentCurrentObservation{nil, {}, c, &copyCurrent} {
		if data, err := v.ValidateOriginalGroup(p, command); err != ErrInvalid || data != (P1ParentCurrentServiceData{}) {
			t.Fatal("unissued/copied current accepted", err)
		}
	}
}

func TestP1ParentServiceAdmissionDoesNotExtendOldReadStages(t *testing.T) {
	parent := &ParentPending{}
	o := &owner{parent: parent}
	parent.owner = o
	p := &P1ServiceGroup{p1ServiceState: &p1ServiceState{parent: parent, command: &exec.Cmd{}}}
	p.self = p
	o.p1 = p
	for _, kind := range []string{"p1-parent-ack-auth", "p1-parent-current", "p1-parent-challenge-sign", "p1-parent-proof-auth"} {
		if _, err := parent.begin(kind); err != ErrInvalid || o.active != nil {
			t.Fatal("unprepared admission accepted", err)
		}
	}
	old := []string{"open", "debit", "p1-funding", "p1-binding", "p1-before-start"}
	if err := journalReadBudgetData(true, old, "p1-parent-current"); err != ErrInvalid {
		t.Fatal("read6 enlarged old five stages", err)
	}
	if p.parentAckAuthTried || p.parentCurrentTried || p.parentChallengeSignTried || p.parentProofAuthTried || len(o.reads) != 0 {
		t.Fatal("refusal consumed own slots/old reads")
	}
}
