package residentservice

import (
	"context"
	"errors"
	"testing"
)

func workerParentRefusalData(t *testing.T) *WorkerParentPending {
	t.Helper()
	p, err := NewWorkerExistingGrantParentPending("/synthetic")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWorkerParentConstructorCopyAndPreopenDebitRefuseData(t *testing.T) {
	for _, home := range []string{"", "/", "relative", "/synthetic/../home"} {
		if p, err := NewWorkerExistingGrantParentPending(home); err == nil || p != nil {
			t.Fatal("invalid home accepted")
		}
	}
	p := workerParentRefusalData(t)
	copy := *p
	if err := copy.OpenOriginal(context.Background()); err == nil {
		t.Fatal("copied parent accepted")
	}
	if d, err := p.PrepareOriginalDebit(context.Background(), "key"); err == nil || d != nil || p.debitTried {
		t.Fatal("pre-open debit admitted")
	}
	var zero WorkerParentPending
	if err := zero.OpenOriginal(context.Background()); err == nil {
		t.Fatal("zero parent accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
}

func TestWorkerParentCancelledOpenHasNoImageOrNativeEffectsData(t *testing.T) {
	p := workerParentRefusalData(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.OpenOriginal(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertWorkerNoNativeData(t, p.custody)
	if p.opened || p.debitComplete || p.custody.grant != nil || p.custody.debit != nil {
		t.Fatal("cancelled open issued custody")
	}
	if err := p.OpenOriginal(context.Background()); err == nil {
		t.Fatal("cancelled original reopened")
	}
}

func TestWorkerParentDebitLookupCannotIssueSyncFromRecordsData(t *testing.T) {
	p := workerParentRefusalData(t)
	for _, d := range []*WorkerOriginalDebit{nil, {}, {owner: p.custody, record: &WorkerDebitRecord{ID: "synthetic"}}} {
		if _, err := p.LookupOriginalDebit(d); err == nil {
			t.Fatal("unregistered debit accepted")
		}
	}
	// Deliberately install synthetic DATA. Readback remains unknown and cannot
	// replace an original append/sync; no native call or charge factory exists.
	d := &WorkerOriginalDebit{owner: p.custody, record: &WorkerDebitRecord{ID: "synthetic", LaunchKey: "key"}}
	d.self = d
	p.custody.debit = d
	seen, err := p.LookupOriginalDebit(d)
	if err != nil || seen.OriginalSyncReturned || !seen.OutcomeUnknown || seen.NativeOutcome != "unknown" {
		t.Fatal(seen, err)
	}
	copy := *d
	if _, err = p.LookupOriginalDebit(&copy); err == nil {
		t.Fatal("copied debit accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
}
