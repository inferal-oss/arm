package residentservice

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func workerOperatorRefusalData(t *testing.T) *WorkerOperatorPending {
	t.Helper()
	p, err := NewWorkerOperatorPending("/synthetic", "/synthetic/profile3", "/synthetic/evidence3", "worker-key", "synthetic refusal only")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertWorkerNoNativeData(t *testing.T, w *workerCustody) {
	t.Helper()
	seen := w.observeData()
	if seen.LastPublishedRawFileReferences != 0 || seen.JournalReads != 0 || seen.AppendAttempted || w.image != nil || seen.NativeOutcome != "unknown" {
		t.Fatal("refusal performed native work", seen)
	}
}

func TestWorkerOperatorConstructorBoundsData(t *testing.T) {
	for _, name := range []string{"home", "profile", "evidence", "same-input", "key", "reason"} {
		t.Run(name, func(t *testing.T) {
			home, profile, evidence, key, reason := "/synthetic", "/synthetic/p", "/synthetic/e", "key", "reason"
			switch name {
			case "home":
				home = "/"
			case "profile":
				profile = "relative"
			case "evidence":
				evidence = "/synthetic/../e"
			case "same-input":
				evidence = profile
			case "key":
				key = strings.Repeat("k", 129)
			case "reason":
				reason = ""
			}
			if p, err := NewWorkerOperatorPending(home, profile, evidence, key, reason); err == nil || p != nil {
				t.Fatal("invalid constructor accepted")
			}
		})
	}
}

func TestWorkerOperatorCopiesAndSelectorsRefuseBeforeNativeData(t *testing.T) {
	p := workerOperatorRefusalData(t)
	copy := *p
	if _, err := copy.PlanOriginal(context.Background()); err == nil {
		t.Fatal("copied operator accepted")
	}
	var zero WorkerOperatorPending
	if _, err := zero.PlanOriginal(context.Background()); err == nil {
		t.Fatal("zero operator accepted")
	}
	if _, err := p.InspectOriginal(context.Background(), "other-key"); err == nil || p.inspectTried {
		t.Fatal("wrong inspection key consumed owner")
	}
	for _, expect := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		if _, err := p.ApplyOriginal(context.Background(), expect); err == nil || p.planTried || p.applyTried {
			t.Fatal("invalid review digest reached planning")
		}
	}
	assertWorkerNoNativeData(t, p.custody)
}

func TestWorkerOperatorCancelledCallsRetainOriginalFailureData(t *testing.T) {
	for _, name := range []string{"plan", "apply", "inspect"} {
		t.Run(name, func(t *testing.T) {
			p := workerOperatorRefusalData(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch name {
			case "plan":
				_, err = p.PlanOriginal(ctx)
			case "apply":
				_, err = p.ApplyOriginal(ctx, strings.Repeat("a", 64))
			case "inspect":
				_, err = p.InspectOriginal(ctx, p.key)
			}
			if !errors.Is(err, context.Canceled) || p.ObserveOriginal().FirstError != err {
				t.Fatal("original cancellation lost", err)
			}
			assertWorkerNoNativeData(t, p.custody)
			if len(p.reads) != 0 || p.mutationAttempted || p.namespaceSynced {
				t.Fatal("cancelled call mutated authorization")
			}
			if _, err = p.PlanOriginal(context.Background()); err == nil {
				t.Fatal("cancelled owner reused")
			}
		})
	}
}

func TestWorkerOperatorNilContextRefusesBeforeNativeData(t *testing.T) {
	p := workerOperatorRefusalData(t)
	if _, err := p.PlanOriginal(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	assertWorkerNoNativeData(t, p.custody)
}
