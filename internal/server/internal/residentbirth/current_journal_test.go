package residentbirth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"getarm.org/internal/server/internal/residentservice"
)

func TestP1CurrentLaunchJournalDataRequiresExactIntentFooterAndHashes(t *testing.T) {
	intent := []byte("synthetic-original-intent")
	footer, err := EncodeP1RootBoundFooterData(footerDataOnly(t))
	if err != nil {
		t.Fatal(err)
	}
	current := append(append([]byte(nil), intent...), footer...)
	fh, jh := sha256.Sum256(footer), sha256.Sum256(current)
	expected := P1RootBoundFooterResultData{Footer: footerDataOnly(t), FooterBytes: len(footer), JournalBytes: len(current), FooterSHA256: hex.EncodeToString(fh[:]), JournalSHA256: hex.EncodeToString(jh[:])}
	if got, err := validateP1CurrentLaunchJournalData(current, intent, footer, expected); err != nil || got != expected {
		t.Fatal("valid DATA refused", err)
	}
	for _, name := range []string{"empty", "overflow", "truncated", "extended", "intent", "footer", "footer-count", "journal-count", "footer-hash", "journal-hash", "footer-data"} {
		data := append([]byte(nil), current...)
		prefix := append([]byte(nil), intent...)
		suffix := append([]byte(nil), footer...)
		want := expected
		switch name {
		case "empty":
			data = nil
		case "overflow":
			data = make([]byte, MaxP1LaunchJournalBytes+1)
		case "truncated":
			data = data[:len(data)-1]
		case "extended":
			data = append(data, 'x')
		case "intent":
			prefix[0] ^= 1
		case "footer":
			suffix[len(suffix)-1] ^= 1
		case "footer-count":
			want.FooterBytes++
		case "journal-count":
			want.JournalBytes++
		case "footer-hash":
			want.FooterSHA256 = want.JournalSHA256
		case "journal-hash":
			want.JournalSHA256 = want.FooterSHA256
		case "footer-data":
			want.Footer.Generation = "wrong"
		}
		if _, err := validateP1CurrentLaunchJournalData(data, prefix, suffix, want); err != errOriginalFence {
			t.Fatal("mutated current launch DATA accepted", name, err)
		}
	}
	if !bytes.Equal(current[:len(intent)], intent) {
		t.Fatal("DATA helper changed intent")
	}
}

func TestP1CurrentLaunchRequiresActualFooterAndServiceObservationBeforeEffects(t *testing.T) {
	p := &InheritedFencePending{}
	s := &inheritedFenceState{self: p, rootContext: context.Background()}
	p.state = s
	copyFence := *p
	service := &residentservice.ChildPending{}
	observation := &residentservice.P1CurrentServiceObservation{}
	view := &residentservice.CurrentChallengeView{}
	charge := &residentservice.ManagerCharge{}
	for _, f := range []*InheritedFencePending{nil, {}, &copyFence, p} {
		if data, err := f.ValidateP1CurrentOriginal(context.Background(), service, observation, view, charge); err != errOriginalFence || data != (P1CurrentLaunchData{}) {
			t.Fatal("unprepared current launch accepted", err)
		}
		if data, err := f.P1CurrentOriginalData(service, observation, view, charge); err != errOriginalFence || data != (P1CurrentLaunchData{}) {
			t.Fatal("unprepared current launch selector accepted", err)
		}
	}
	if s.active != nil || s.currentTried || s.current != nil || s.firstError != nil || s.verifyTried || s.footerTried || s.footer != nil || s.intent != nil || s.bytesRead != 0 {
		t.Fatal("refusal altered original intent/footer/current state")
	}
}
