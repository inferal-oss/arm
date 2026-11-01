package residentservice

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"getarm.org/internal/mesh"
)

// These controls allocate DATA holders only. No native method is called.
func TestWorkerRawCustodyRejectsLegacyAuthorityData(t *testing.T) {
	for _, name := range []string{"parent", "child", "operator", "grant", "debit", "charge", "image-flavor", "secret", "journal-chain", "legacy-operation"} {
		t.Run(name, func(t *testing.T) {
			w := newWorkerCustodyData("/synthetic")
			if !w.legacyEmptyLocked() {
				t.Fatal("fresh metadata owner rejected")
			}
			switch name {
			case "parent":
				w.raw.parent = &ParentPending{}
			case "child":
				w.raw.child = &ChildPending{}
			case "operator":
				w.raw.operator = &OperatorPending{}
			case "grant":
				w.raw.grant = &GrantRecord{}
			case "debit":
				w.raw.debit = &OriginalDebit{}
			case "charge":
				w.raw.charge = &ManagerCharge{}
			case "image-flavor":
				w.raw.buildFromOriginalGrant = true
			case "secret":
				w.raw.secret[0] = 1
			case "journal-chain":
				w.raw.journal.last[0] = 1
			case "legacy-operation":
				w.raw.active = &operation{owner: w.raw, kind: "open"}
			}
			if w.legacyEmptyLocked() {
				t.Fatal("legacy authority crossed raw bookkeeping boundary")
			}
			if _, err := w.operationLocked("worker-parent-open"); err == nil {
				t.Fatal("effect operation admitted")
			}
		})
	}
}

func TestWorkerImagePendingRejectsZeroCopyForeignAndDuplicateData(t *testing.T) {
	var zero workerRuntimeImage
	if _, err := zero.proofOriginal(context.Background()); err == nil {
		t.Fatal("zero image accepted")
	}
	p := workerParentRefusalData(t)
	w := p.custody
	if _, err := p.beginOriginal("worker-parent-open"); err != nil {
		t.Fatal(err)
	}
	image, err := w.newRuntimeImagePendingOriginal()
	if err != nil {
		t.Fatal(err)
	}
	copy := *image
	if _, err = copy.proofOriginal(context.Background()); err == nil {
		t.Fatal("copied image accepted")
	}
	other := workerParentRefusalData(t)
	foreign := &workerRuntimeImage{owner: other.custody}
	foreign.self = foreign
	if _, err = foreign.proofOriginal(context.Background()); err == nil {
		t.Fatal("foreign image accepted")
	}
	if _, err = w.captureRuntimeImageOriginal(context.Background()); err == nil {
		t.Fatal("second capture admitted")
	}
	if w.image != image || image.rawFD != -1 || image.rawRecorded || image.openTried || image.file != nil || len(image.buffer) != 0 || w.raw.files.opened != 0 {
		t.Fatal("pending slot replaced or native work performed")
	}
	if _, err = image.proofOriginal(context.Background()); err == nil {
		t.Fatal("incomplete image proved")
	}
}

func TestWorkerImageSizeBoundAndCancelledOriginalRetainUnknownData(t *testing.T) {
	for _, size := range []int64{-1, 0, mesh.MaxArtifactBytes + 1} {
		if workerImageSizeData(size) {
			t.Fatal("oversize/empty image accepted", size)
		}
	}
	if !workerImageSizeData(1) || !workerImageSizeData(mesh.MaxArtifactBytes) {
		t.Fatal("bounded size DATA refused")
	}
	p := workerParentRefusalData(t)
	if _, err := p.beginOriginal("worker-parent-open"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	image, err := p.custody.captureRuntimeImageOriginal(ctx)
	if !errors.Is(err, context.Canceled) || image == nil || p.custody.image != image || image.firstError != err {
		t.Fatal("original failed slot lost", err)
	}
	if image.rawFD != -1 || image.rawRecorded || image.openTried || image.file != nil || image.complete {
		t.Fatal("cancelled capture performed native work")
	}
	if _, next := image.proofOriginal(context.Background()); next != err {
		t.Fatal("original image error replaced", next)
	}
	if next := image.captureBodyOriginal(context.Background()); next == nil {
		t.Fatal("cancelled capture retried")
	}
	if _, next := p.custody.captureRuntimeImageOriginal(context.Background()); next == nil || p.custody.image != image {
		t.Fatal("unknown slot replaced")
	}
	if p.ObserveOriginal().NativeOutcome != "unknown" {
		t.Fatal("failure credited closure")
	}
}

func TestWorkerRawCustodyRefusesCopiedAndUnknownKindsData(t *testing.T) {
	w := newWorkerCustodyData("/synthetic")
	copy := *w
	if copy.legacyEmptyLocked() {
		t.Fatal("copied custody accepted")
	}
	for _, kind := range []string{"open", "debit", "p1-funding", "worker-future", ""} {
		if _, err := w.operationLocked(kind); err == nil || w.raw.active != nil {
			t.Fatal("unreviewed raw operation accepted", kind)
		}
	}
	seen := w.observeData()
	if seen.NativeOutcome != "unknown" || seen.LastPublishedRawFileReferences != 0 || seen.FirstError != nil {
		t.Fatal(seen)
	}
}

func TestWorkerRawOriginalDeferRetainsErrorPanicAndGoexitData(t *testing.T) {
	for _, name := range []string{"error", "panic", "goexit"} {
		t.Run(name, func(t *testing.T) {
			w := workerParentRefusalData(t).custody
			op, err := w.operationLocked("worker-parent-open")
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("synthetic worker failure")
			run := func() {
				normal := false
				var result error
				defer w.raw.finish(op, &normal, &result)
				switch name {
				case "error":
					result = cause
					normal = true
				case "panic":
					panic(cause)
				case "goexit":
					runtime.Goexit()
				}
			}
			if name == "goexit" {
				done := make(chan struct{})
				go func() { defer close(done); run() }()
				<-done
			} else if name == "panic" {
				func() {
					defer func() {
						if recover() != cause {
							t.Fatal("panic identity lost")
						}
					}()
					run()
				}()
			} else {
				run()
			}
			seen := w.observeData()
			if seen.FirstError == nil || seen.NativeOutcome != "unknown" {
				t.Fatal(seen)
			}
			if name == "error" {
				if seen.OperationPending || seen.FirstError != cause {
					t.Fatal(seen)
				}
			} else if !seen.OperationPending || !seen.OperationAbnormal {
				t.Fatal(seen)
			}
			if _, err = w.operationLocked("worker-parent-debit"); err == nil {
				t.Fatal("failed original reused")
			}
		})
	}
}
