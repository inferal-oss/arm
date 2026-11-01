package residentservice

import (
	"context"
	"errors"
	"testing"

	"getarm.org/internal/residentrefs"
)

// Deliberately inert recipient/view metadata. This cannot satisfy the actual
// process-wide census claim and is never materialized by these controls.
func unissuedWorkerChildData() *WorkerChildPending {
	p := &WorkerChildPending{custody: newWorkerCustodyData(""), source: &residentrefs.ServiceOriginal{}, journal: &residentrefs.JournalView{}}
	p.self = p
	p.custody.child = p
	return p
}

func TestWorkerChildRejectsInertSourcesBeforeViewData(t *testing.T) {
	for _, source := range []*residentrefs.ServiceOriginal{nil, {}} {
		if p, err := NewWorkerChildPending(source); err == nil || p != nil {
			t.Fatal("inert source issued child")
		}
	}
}

func TestWorkerChildCopyAndCancellationBeforeNativeData(t *testing.T) {
	b, _ := workerBootstrapFixtureData(t)
	p := unissuedWorkerChildData()
	copy := *p
	if err := copy.ReceiveWorkerOriginal(context.Background(), b); err == nil {
		t.Fatal("copied child accepted")
	}
	var zero WorkerChildPending
	if err := zero.ReceiveWorkerOriginal(context.Background(), b); err == nil {
		t.Fatal("zero child accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.ReceiveWorkerOriginal(ctx, b); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if p.received || p.runtimeBound || p.charge != nil || len(p.environment) != 0 {
		t.Fatal("cancellation issued child state")
	}
	assertWorkerNoNativeData(t, p.custody)
	if err := p.ReceiveWorkerOriginal(context.Background(), b); err == nil {
		t.Fatal("failed receive repeated")
	}
}

func TestWorkerChildDataAndForeignCensusCannotReceiveData(t *testing.T) {
	for _, name := range []string{"nil-context", "old-role", "old-schema", "unissued-census"} {
		t.Run(name, func(t *testing.T) {
			p := unissuedWorkerChildData()
			b, _ := workerBootstrapFixtureData(t)
			ctx := context.Background()
			switch name {
			case "old-role":
				b.Role = "--arm-resident-core-v1"
			case "old-schema":
				b.Schema = 1
			}
			var err error
			if name == "nil-context" {
				err = p.ReceiveWorkerOriginal(nil, b)
			} else {
				err = p.ReceiveWorkerOriginal(ctx, b)
			}
			if err == nil || p.received || p.custody.grant != nil || p.custody.debit != nil {
				t.Fatal("metadata issued native receive")
			}
			assertWorkerNoNativeData(t, p.custody)
		})
	}
}

func TestWorkerChildRequiresReceivedRuntimeAndSameContextData(t *testing.T) {
	p := unissuedWorkerChildData()
	if err := p.ValidateWorkerEnvironmentOriginal(context.Background()); err == nil {
		t.Fatal("unreceived environment admitted")
	}
	if err := p.BindOriginalWorkerRuntime(context.Background(), nil); err == nil {
		t.Fatal("nil runtime admitted")
	}
	if charge, err := p.ClaimOriginalWorkerManagerCharge(); err == nil || charge != nil {
		t.Fatal("unreceived child issued charge")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if !sameWorkerContextData(ctx, ctx) || sameWorkerContextData(nil, ctx) || sameWorkerContextData(ctx, context.Background()) {
		t.Fatal("root context identity changed")
	}
	assertWorkerNoNativeData(t, p.custody)
}
