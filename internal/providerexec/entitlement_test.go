package providerexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/providerproto"
)

func controlledHeld(t *testing.T, lifetime context.Context, check EntitlementCheck) Held {
	t.Helper()
	// These are controlled admission inputs, not current Manager/clock proof.
	audit := time.Now().Add(-time.Hour)
	worker := model.Lease{ID: "worker", Host: strings.Repeat("a", 32), Owner: "1000", Revision: 1, State: "active", Expires: &audit, Request: model.Request{Resources: model.Resources{CPU: 1, Memory: 128 << 20}}}
	temp := model.Lease{ID: "temp", Host: worker.Host, Owner: worker.Owner, Revision: 2, State: "active", Expires: &audit, Request: model.Request{Kind: "storage", Resources: model.Resources{Disk: 64 << 20}}, Artifact: &model.Artifact{Path: "/controlled-temp", Phase: "owned", Identity: &model.FileIdentity{Device: 1, Inode: 2, Owner: 1000, BirthKnown: true, BornSeconds: 3}}}
	eligibility, err := EntitlementFromTrusted(lifetime, "controlled-boot", check)
	if err != nil {
		t.Fatal(err)
	}
	held, err := HeldFromTrusted(worker, temp, eligibility)
	if err != nil {
		t.Fatal(err)
	}
	// Later mutations of constructor inputs cannot change the frozen binding.
	temp.Artifact.Identity.Inode = 99
	if held.Binding().TempIdentity.Inode != 2 {
		t.Fatal("caller mutated held identity")
	}
	return held
}
func controlledPrepared(t *testing.T, h Held) (Prepared, providerproto.Request, providerproto.Original) {
	t.Helper()
	config := providerproto.NullData()
	selected := Selection{runtime: Image{valid: true, size: 1}, provider: Image{valid: true, size: 1, sha: strings.Repeat("1", 64)}}
	q := providerproto.Request{Protocol: providerproto.Protocol, Version: 1, Kind: "request", RequestID: "wire", Registration: providerproto.Registration{ID: "registration", Generation: "1", CodeSHA256: providerproto.Digest(selected.provider.sha), ImplementationVersion: "fixture-1", ResourceType: "object", ConfigSHA256: config.SHA256(), ExternalScope: providerproto.ExternalScope{Account: "account", Project: "project", Namespace: "namespace"}}, Configuration: config, CredentialRefs: []string{}, Method: providerproto.Validate, Context: providerproto.Context{Authority: providerproto.ID(h.authority), Scope: providerproto.Scope{Kind: "host", HostID: providerproto.ID(h.authority)}, OwnerUID: providerproto.Uint(h.owner), Reason: "controlled eligibility component", OperationKey: "attempt"}, Params: providerproto.ValidateParams{}}
	digest, err := providerproto.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Context.IntentSHA256 = digest
	original, err := providerproto.OriginalFromTrusted(q)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare("attempt", "registration", selected, h, q, original, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return prepared, q, original
}
func TestEntitlementConstructionAndExhaustion(t *testing.T) {
	positive := func(context.Context, HeldBinding) (time.Duration, error) { return time.Minute, nil }
	if _, err := EntitlementFromTrusted(nil, "boot", positive); err == nil {
		t.Fatal("nil lifetime")
	}
	if _, err := EntitlementFromTrusted(context.Background(), "", positive); err == nil {
		t.Fatal("missing boot")
	}
	if _, err := EntitlementFromTrusted(context.Background(), "boot", nil); err == nil {
		t.Fatal("missing current checker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EntitlementFromTrusted(ctx, "boot", positive); err == nil {
		t.Fatal("cancelled lifetime")
	}
	for _, remaining := range []time.Duration{0, -time.Second} {
		held := controlledHeld(t, context.Background(), func(context.Context, HeldBinding) (time.Duration, error) { return remaining, nil })
		if _, err := held.remaining(context.Background()); err == nil {
			t.Fatal("exhaustion became entitlement")
		}
	}
	held := controlledHeld(t, context.Background(), func(context.Context, HeldBinding) (time.Duration, error) {
		return time.Hour, errors.New("uncertain native clock")
	})
	if _, err := held.remaining(context.Background()); err == nil {
		t.Fatal("error became entitlement")
	}
}
func TestPreparationUsesCurrentRemainingAndOriginalBinding(t *testing.T) {
	var remaining atomic.Int64
	remaining.Store(int64(time.Minute))
	held := controlledHeld(t, context.Background(), func(ctx context.Context, b HeldBinding) (time.Duration, error) {
		if b.Worker != "worker" || b.Temp != "temp" || b.WorkerRevision != 1 || b.TempRevision != 2 || b.Boot != "controlled-boot" || b.TempIdentity.Inode != 2 {
			return 0, errors.New("wrong binding")
		}
		return time.Duration(remaining.Load()), nil
	})
	prepared, q, original := controlledPrepared(t, held)
	// Audit wall time was already in the past; only trusted remaining is used.
	remaining.Store(int64(time.Second))
	if _, err := Prepare(prepared.attempt, prepared.registration, prepared.selected, held, q, original, time.Second); err == nil {
		t.Fatal("timeout did not fit strictly")
	}
	remaining.Store(0)
	owner := &Owner{authority: held.authority, ownerToken: "original-owner", handles: map[string]*Handle{}, retired: map[string]string{}}
	marker := StartMarker{prepared.attempt, prepared.requestDigest, held.authority, held.worker, held.temp, owner.ownerToken}
	if _, err := owner.BeginStart(context.Background(), prepared, marker); err == nil || len(owner.handles) != 0 {
		t.Fatal("expired preparation started new work", err)
	}
	old := &Handle{prepared: prepared}
	owner.handles[prepared.attempt] = old
	if got, err := owner.BeginStart(context.Background(), prepared, marker); err != nil || got != old {
		t.Fatal("eligibility loss discarded original lookup", err)
	}
}
func TestEntitlementCancellationDuringCurrentRead(t *testing.T) {
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	held := controlledHeld(t, lifetime, func(ctx context.Context, _ HeldBinding) (time.Duration, error) {
		close(entered)
		<-ctx.Done()
		return time.Hour, nil
	})
	done := make(chan error, 1)
	go func() { _, err := held.remaining(context.Background()); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled read restored eligibility")
		}
	case <-time.After(time.Second):
		t.Fatal("checker did not receive lifetime cancellation")
	}
}
func controlledInvalidate(p *Prepared) *atomic.Bool {
	allowed := &atomic.Bool{}
	allowed.Store(true)
	previous := p.held.entitlement.current
	p.held.entitlement.current = func(ctx context.Context, b HeldBinding) (time.Duration, error) {
		if !allowed.Load() {
			return 0, errors.New("controlled native deadline exhausted")
		}
		return previous(ctx, b)
	}
	return allowed
}
func TestCurrentEntitlementRefusesDelayedNativeEntry(t *testing.T) {
	f := newFixture(t)
	gate := f.gate()
	f.owner.beforeStart = gate
	p := f.prepared(t, "late-eligibility", "registration", providerproto.Validate, "witness")
	allowed := controlledInvalidate(&p)
	h := f.begin(t, p)
	allowed.Store(false)
	close(gate)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observation, err := h.WaitObservation(ctx)
	if err != nil || !observation.KnownNotStarted || !observation.ScopedNativeClosed || observation.GrantPublished {
		t.Fatal(observation, err)
	}
	if _, err = os.Stat(f.root + "/late-eligibility-provider-entered"); !os.IsNotExist(err) {
		t.Fatal("provider entered after eligibility loss", err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 128<<20 {
		t.Fatal("eligibility refusal reclaimed ordinary holds", status.Reserved, err)
	}
	f.settle(t, h)
}
func TestCurrentEntitlementRefusesGrantAfterHello(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "late-grant", "registration", providerproto.Validate, "witness")
	allowed := controlledInvalidate(&p)
	h := f.begin(t, p)
	marker := f.grantMarker(t, h)
	allowed.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Execute(ctx, marker); err == nil {
		t.Fatal("exhausted entitlement published a grant")
	}
	observation, err := h.WaitObservation(ctx)
	if err != nil || observation.GrantPublished || observation.ProtocolSuccess || !observation.ScopedNativeClosed || !observation.HelperWaitObserved {
		t.Fatal(observation, err)
	}
	if _, err = os.Stat(f.root + "/late-grant-provider-entered"); !os.IsNotExist(err) {
		t.Fatal("provider entered after eligibility loss", err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 128<<20 {
		t.Fatal("grant refusal reclaimed ordinary holds", status.Reserved, err)
	}
	f.settle(t, h)
}

func TestEntitlementCallRespectsEveryOriginalContext(t *testing.T) {
	for _, which := range []string{"call", "native", "admissions"} {
		t.Run(which, func(t *testing.T) {
			call, cancelCall := context.WithCancel(context.Background())
			defer cancelCall()
			native, cancelNative := context.WithCancel(context.Background())
			defer cancelNative()
			admissions, cancelAdmissions := context.WithCancel(context.Background())
			defer cancelAdmissions()
			entered := make(chan struct{})
			held := controlledHeld(t, admissions, func(ctx context.Context, _ HeldBinding) (time.Duration, error) {
				close(entered)
				<-ctx.Done()
				return time.Hour, nil
			})
			h := &Handle{context: native, prepared: Prepared{held: held}}
			done := make(chan error, 1)
			go func() { _, err := h.currentRemaining(call); done <- err }()
			<-entered
			switch which {
			case "call":
				cancelCall()
			case "native":
				cancelNative()
			case "admissions":
				cancelAdmissions()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled original context restored eligibility")
				}
			case <-time.After(time.Second):
				t.Fatal("current checker was not cancelled")
			}
		})
	}
}

func TestEntitlementConsumedGrantDoesNotRecheckOrCancelCompletedDuty(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "active_caller"
		if cancelled {
			name = "cancelled_caller"
		}
		t.Run(name, func(t *testing.T) {
			var checks atomic.Int64
			held := controlledHeld(t, context.Background(), func(context.Context, HeldBinding) (time.Duration, error) {
				checks.Add(1)
				return 0, errors.New("must not inspect consumed authority")
			})
			native, cancel := context.WithCancel(context.Background())
			cancel()
			hello := make(chan struct{})
			close(hello)
			h := &Handle{owner: &Owner{}, context: native, prepared: Prepared{held: held}, helloDone: hello, cancelCh: make(chan struct{}), grantPublished: true, observation: Observation{ProtocolSuccess: true}}
			ctx, cancelCall := context.WithCancel(context.Background())
			defer cancelCall()
			if cancelled {
				cancelCall()
			}
			raw, _ := json.Marshal(h.helloProcess)
			if err := h.Execute(ctx, DispatchMarker{Start: h.marker, WorkerProcessSHA256: hash(raw), PublicationSHA256: strings.Repeat("0", 64)}); err == nil {
				t.Fatal("duplicate grant accepted")
			}
			if checks.Load() != 0 || h.observation.CancelAccepted || !h.observation.ProtocolSuccess {
				t.Fatal("duplicate changed original completion")
			}
		})
	}
}

func TestCurrentEntitlementOwnerCancellationRetainsOriginalDuty(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "owner-cancelled", "registration", providerproto.Validate, "witness")
	lifetime, cancelLifetime := context.WithCancel(context.Background())
	defer cancelLifetime()
	p.held.entitlement.lifetime = lifetime
	h := f.begin(t, p)
	f.grantMarker(t, h)
	cancelLifetime()
	if original, err := f.owner.BeginStart(lifetime, p, h.marker); err != nil || original != h {
		t.Fatal("cancelled ownership lost original lookup", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.CancelAccepted || o.GrantPublished || !o.HelperWaitObserved || !o.ScopedNativeClosed {
		t.Fatal("owner cancellation lost original native closure", o, err)
	}
	if _, err = os.Stat(f.root + "/owner-cancelled-provider-entered"); !os.IsNotExist(err) {
		t.Fatal("provider entered after owner cancellation", err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 128<<20 {
		t.Fatal("owner cancellation reclaimed ordinary holds", status.Reserved, err)
	}
	f.settle(t, h)
}

type retryBarrierContext struct {
	context.Context
	entered chan struct{}
	resume  <-chan struct{}
	once    sync.Once
}

func (c *retryBarrierContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	<-c.resume
	return c.Context.Err()
}

func TestEntitlementConcurrentCancelledRetryPublicationCut(t *testing.T) {
	for _, order := range []string{"cancellation_first", "publication_first"} {
		t.Run(order, func(t *testing.T) {
			f := newFixture(t)
			attempt := "retry-" + strings.ReplaceAll(order, "_", "-")
			p := f.prepared(t, attempt, "registration", providerproto.Validate, "witness")
			h := f.begin(t, p)
			marker := f.grantMarker(t, h)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			retry, cancelRetry := context.WithCancel(context.Background())
			cancelRetry()
			resume := f.gate()
			release := func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			}
			entered := make(chan struct{})
			if order == "cancellation_first" {
				h.afterRetryCancelDecision = func() {
					// Snapshot also proves this barrier runs outside h.mu.
					h.Snapshot()
					close(entered)
					<-resume
				}
			} else {
				retry = &retryBarrierContext{Context: retry, entered: entered, resume: resume}
			}
			finished := make(chan struct{})
			var retryErr error
			go func() { retryErr = h.Execute(retry, marker); close(finished) }()
			defer func() {
				release()
				select {
				case <-finished:
				case <-time.After(10 * time.Second):
					t.Error("original retry did not join")
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("retry did not reach its barrier")
			}
			grantErr := h.Execute(ctx, marker)
			var completed Observation
			if grantErr == nil {
				var err error
				completed, err = h.WaitObservation(ctx)
				if err != nil || !completed.ProtocolSuccess {
					t.Fatal("published duty did not complete before retry resumed", completed, err)
				}
			}
			release()
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("cancelled retry did not return")
			}
			if !errors.Is(retryErr, context.Canceled) {
				t.Fatal("retry lost original cancellation", retryErr)
			}
			o, err := h.WaitObservation(ctx)
			if err != nil || !o.ScopedNativeClosed || !o.HelperWaitObserved {
				t.Fatal("original duty did not close", o, err)
			}
			if order == "cancellation_first" {
				if grantErr == nil || o.GrantPublished || !o.CancelAccepted || o.ProtocolSuccess {
					t.Error("cancelled retry did not fence publication before completion", o)
				}
			} else {
				before, _ := json.Marshal(completed)
				after, _ := json.Marshal(o)
				if grantErr != nil || o.CancelAccepted || !o.ProtocolSuccess || string(before) != string(after) {
					t.Error("published history changed during cancelled retry", o, grantErr)
				}
			}
			f.settle(t, h)
		})
	}
}
