package residentbirth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"getarm.org/internal/server/internal/residentservice"
	"testing"
)

func TestWorkerCurrentLaunchDataRequiresExactOriginalIntentAndFooter(t *testing.T) {
	intent := []byte("synthetic worker intent DATA only")
	footer := workerFooterDataOnly(t)
	h := sha256.Sum256(intent)
	footer.IntentSHA256 = hex.EncodeToString(h[:])
	frame, err := EncodeWorkerRootBoundFooterData(footer)
	if err != nil {
		t.Fatal(err)
	}
	journal := append(append([]byte(nil), intent...), frame...)
	f := sha256.Sum256(frame)
	j := sha256.Sum256(journal)
	expected := WorkerRootBoundFooterResultData{Footer: footer, FooterBytes: len(frame), JournalBytes: len(journal), FooterSHA256: hex.EncodeToString(f[:]), JournalSHA256: hex.EncodeToString(j[:])}
	if _, err = validateWorkerCurrentLaunchJournalData(journal, intent, frame, expected); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), journal...)
	bad[0] ^= 1
	if _, err = validateWorkerCurrentLaunchJournalData(bad, intent, frame, expected); err == nil {
		t.Fatal("changed original prefix accepted")
	}
	if _, err = validateWorkerCurrentLaunchJournalData(journal[:len(journal)-1], intent, frame, expected); err == nil {
		t.Fatal("truncation accepted")
	}
}

func TestWorkerFooterAndCurrentLaunchRequireIssuedOriginalOwners(t *testing.T) {
	var p *WorkerInheritedFencePending
	if err := p.AppendWorkerRootBoundFooterOriginal(context.Background(), &residentservice.WorkerChildPending{}, "bad"); err == nil {
		t.Fatal("unissued footer owner accepted")
	}
	if _, err := p.ValidateWorkerCurrentOriginal(context.Background(), &residentservice.WorkerChildPending{}, &residentservice.WorkerCurrentServiceObservation{}, &residentservice.WorkerCurrentChallengeView{}, &residentservice.WorkerManagerCharge{}); err == nil {
		t.Fatal("unissued current owner accepted")
	}
	if _, err := p.WorkerCurrentOriginalData(nil, nil, nil, nil); err == nil {
		t.Fatal("unissued current observation accepted")
	}
}
