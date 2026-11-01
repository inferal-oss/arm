package residentservice

import (
	"context"
	"errors"
	"testing"
)

func TestWorkerJournalRequiresOriginalOperationBeforeNativeData(t *testing.T) {
	w := newWorkerCustodyData("/synthetic")
	if _, err := w.readJournalOriginal(context.Background()); err == nil {
		t.Fatal("unowned read accepted")
	}
	if err := w.appendJournalOriginal(context.Background(), workerRecordsData(t)[0]); err == nil {
		t.Fatal("unowned append accepted")
	}
	assertWorkerNoNativeData(t, w)
}

func TestWorkerJournalRefusesForeignLegacyOwnerBeforeNativeData(t *testing.T) {
	w := newWorkerCustodyData("/synthetic")
	w.raw.parent = &ParentPending{}
	if _, err := w.readJournalOriginal(context.Background()); err == nil {
		t.Fatal("legacy owner crossed worker read")
	}
	if err := w.appendJournalOriginal(context.Background(), workerRecordsData(t)[0]); err == nil {
		t.Fatal("legacy owner crossed worker append")
	}
	if w.append != nil || len(w.reads) != 0 || w.raw.files.opened != 0 {
		t.Fatal("refusal performed native effects")
	}
}

func TestWorkerJournalCancellationPrecedesStatAndAppendData(t *testing.T) {
	w := newWorkerCustodyData("/synthetic")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.readJournalOriginal(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := w.appendJournalOriginal(ctx, workerRecordsData(t)[0]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertWorkerNoNativeData(t, w)
}
