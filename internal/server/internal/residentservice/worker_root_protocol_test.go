package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

func TestWorkerChildPreparedRootBoundChallengeAndProofRefuseUnissued(t *testing.T) {
	ctx := context.Background()
	p := &WorkerChildPending{}
	p.self = p
	p.custody = newWorkerCustodyData("/synthetic")
	p.custody.child = p
	for _, child := range []*WorkerChildPending{nil, {}, p} {
		if _, err := child.ValidateWorkerPreparedOriginal(ctx, nil, nil, nil, nil, nil); err == nil {
			t.Fatal("unissued prepared accepted")
		}
		if err := child.CommitWorkerRootBoundOriginal(ctx, nil, nil, nil, "", ""); err == nil {
			t.Fatal("unissued append accepted")
		}
		if _, err := child.WorkerRootBoundOriginalData(); err == nil {
			t.Fatal("unissued append receipt accepted")
		}
		if _, err := child.SignWorkerBoundAckOriginal(ctx, residentprotocol.WorkerContextData{}); err == nil {
			t.Fatal("unissued ack accepted")
		}
		var view *WorkerCurrentChallengeView
		if err := child.AuthenticateWorkerCurrentChallengeOriginal(&view, ctx, &residentrefs.WorkerChallengeRead{}, &WorkerManagerCharge{}); err == nil || view != nil {
			t.Fatal("unissued challenge entered native work")
		}
		var observation *WorkerCurrentServiceObservation
		if _, err := child.ValidateWorkerCurrentOriginal(&observation, ctx, &WorkerCurrentChallengeView{}, &WorkerManagerCharge{}); err == nil || observation != nil {
			t.Fatal("unissued current accepted")
		}
		if _, err := child.SignWorkerCurrentProofOriginal(ctx, &WorkerCurrentServiceObservation{}, &WorkerCurrentChallengeView{}, &WorkerManagerCharge{}); err == nil {
			t.Fatal("unissued proof accepted")
		}
	}
	if p.custody.raw.active != nil || p.custody.raw.openedFDs != 0 || len(p.custody.reads) != 0 || p.custody.append != nil || p.preparedValidation != nil || p.rootBound != nil || p.ackSign != nil || p.challengeAuth != nil || p.currentValidation != nil || p.proofSign != nil {
		t.Fatal("refusal changed original custody")
	}
}

func TestWorkerRootBoundFrameMatchesActualWorkerJournalGrammarData(t *testing.T) {
	records := workerRecordsData(t)
	birth, err := encodeWorkerJournalData(records[:2])
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeWorkerJournalData(birth)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := EncodeWorkerRootBoundFrameData(*records[2].Root, hex.EncodeToString(decoded.last[:]))
	if err != nil {
		t.Fatal(err)
	}
	full := append(append([]byte(nil), birth...), frame...)
	expected, err := encodeWorkerJournalData(records)
	if err != nil || !bytes.Equal(full, expected) {
		t.Fatal("worker service3 grammar changed", err)
	}
	count, err := WorkerRootBoundJournalBytesData(int64(len(birth)), len(frame))
	if err != nil || count != int64(len(full)) {
		t.Fatal(count, err)
	}
	parsed, err := decodeWorkerJournalData(full)
	if err != nil || len(parsed.records) != 3 || *parsed.records[2].Root != *records[2].Root || parsed.last != sha256.Sum256(frame) {
		t.Fatal("canonical service3 DATA differs", err)
	}
	if _, err = WorkerRootBoundJournalBytesData(workerJournalMaxBytes, len(frame)); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err = EncodeWorkerRootBoundFrameData(*records[2].Root, "bad"); err == nil {
		t.Fatal("invalid previous commitment accepted")
	}
}

func TestWorkerChallengeAndCurrentObservationCannotBeReconstructed(t *testing.T) {
	v := &WorkerCurrentChallengeView{}
	v.self = v
	p := &WorkerChildPending{}
	p.self = p
	p.custody = newWorkerCustodyData("/synthetic")
	p.custody.child = p
	v.child = p
	if _, err := v.ValidateOriginal(&WorkerManagerCharge{}); err == nil {
		t.Fatal("unissued current view accepted")
	}
	copy := *v
	if _, err := copy.ValidateOriginal(&WorkerManagerCharge{}); err == nil {
		t.Fatal("copied current view accepted")
	}
	o := &WorkerCurrentServiceObservation{child: p}
	o.self = o
	if _, err := o.ValidateOriginalForFence(p, &residentrefs.FenceOriginal{}, v, &WorkerManagerCharge{}); err == nil {
		t.Fatal("unissued current observation accepted")
	}
	if _, err := v.MarshalJSON(); err == nil {
		t.Fatal("challenge serialized")
	}
	if _, err := o.MarshalJSON(); err == nil {
		t.Fatal("observation serialized")
	}
}
