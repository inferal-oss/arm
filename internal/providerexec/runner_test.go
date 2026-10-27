package providerexec

import (
	"context"
	"fmt"
	"github.com/inferal-oss/arm/internal/providerproto"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualDiagnosticsHeldAccountedAndCompensated(t *testing.T) {
	for _, method := range []providerproto.Method{providerproto.Describe, providerproto.Validate, providerproto.Plan} {
		t.Run(string(method), func(t *testing.T) {
			f := newFixture(t)
			p := f.prepared(t, "attempt", "registration", method, "")
			before, err := f.m.Status()
			if err != nil || before.Reserved.CPU != 1 || before.Reserved.Memory != 128<<20 || before.Reserved.Disk < 64<<20 {
				t.Fatal("ordinary held admission not charged", before.Reserved, err)
			}
			h := f.begin(t, p)
			f.grant(t, h)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			o, err := h.WaitObservation(ctx)
			if err != nil || !o.ProtocolMatched || !o.ProtocolSuccess || !o.ScopedNativeClosed || !o.HelperWaitObserved || !o.GroupAbsentObserved || !o.PumpsJoined || o.ProviderExit == nil || o.ProviderExit.Code != 0 {
				t.Fatalf("actual diagnostic not confirmed: %+v %v", o, err)
			}
			f.owner.mu.Lock()
			retained := len(f.owner.handles)
			f.owner.mu.Unlock()
			if retained != 1 {
				t.Fatal("native closure prematurely dropped compensation slot")
			}
			f.settle(t, h)
			if _, err := f.owner.BeginStart(context.Background(), p, h.marker); err == nil {
				t.Fatal("settled live-owner native key replayed")
			}
			after, err := f.m.Status()
			if err != nil || after.Reserved.CPU != 0 || after.Reserved.Memory != 0 || after.Reserved.Disk != 0 {
				t.Fatal("positive compensation not reflected", after.Reserved, err)
			}
		})
	}
}
func TestRetainedLateStartCancellationNeverGrants(t *testing.T) {
	f := newFixture(t)
	gate := f.gate()
	f.owner.beforeStart = gate
	p := f.prepared(t, "late-start", "registration", providerproto.Validate, "")
	h := f.begin(t, p)
	h.Cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	o, err := h.WaitObservation(ctx)
	cancel()
	if err == nil || !o.StartPending || o.KnownNotStarted || o.ScopedNativeClosed {
		t.Fatal("timeout forged no-start/join", o, err)
	}
	if _, err = f.journal.startMarker(p, f.owner.OwnerToken()); err == nil {
		t.Fatal("durable original marker replayed")
	}
	close(gate)
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err = h.WaitObservation(ctx)
	if err != nil || o.GrantPublished || !o.ScopedNativeClosed || o.ProtocolSuccess {
		t.Fatal("late cancelled native start escaped closure", o, err)
	}
	f.settle(t, h)
}
func TestInvalidOutputAndDeadlineRemainDiagnosticFailure(t *testing.T) {
	for _, mode := range []string{"extra", "nonzero", "stderr", "slow"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			p := f.prepared(t, mode, "registration", providerproto.Validate, mode)
			h := f.begin(t, p)
			f.grant(t, h)
			if mode == "slow" {
				h.Cancel()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			o, err := h.WaitObservation(ctx)
			if err != nil || o.ProtocolSuccess || !o.ScopedNativeClosed {
				t.Fatal("failure forged success or lost native closure", o, err)
			}
			if len(o.StderrTail) > MaxStderrTail {
				t.Fatal("stderr retention unbounded")
			}
			f.settle(t, h)
		})
	}
}
func TestReplacedPinnedImageRefusesBeforeStart(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "replacement", "registration", providerproto.Validate, "")
	if err := os.Rename(f.provider.path, f.provider.path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.provider.path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	h := f.begin(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.KnownNotStarted || o.GrantPublished {
		t.Fatal("replacement inherited native selection", o, err)
	}
	f.settle(t, h)
}

func TestDurableFourAuthorityAndOneRegistrationAcrossProcesses(t *testing.T) {
	f := newFixture(t)
	prepared := []Prepared{}
	for i := 0; i < 5; i++ {
		prepared = append(prepared, f.prepared(t, fmt.Sprintf("slot-%d", i), fmt.Sprintf("registration-%d", i), providerproto.Validate, ""))
	}
	if successes := runReserveChildren(t, f, prepared); successes != 4 {
		t.Fatal("cross-process authoritative slot cap", successes)
	}
	var active int
	if err := f.journal.db.QueryRow(`SELECT COUNT(*) FROM worker_attempts WHERE phase<>'settled'`).Scan(&active); err != nil || active != 4 {
		t.Fatal(active, err)
	}
	// A separate retained authority is independent; a repeated logical provider
	// still cannot create a concurrent new generation/attempt on that authority.
	other := newFixture(t)
	same := []Prepared{other.prepared(t, "same-a", "logical-provider", providerproto.Validate, ""), other.prepared(t, "same-b", "logical-provider", providerproto.Validate, "")}
	if n := runReserveChildren(t, other, same); n != 1 {
		t.Fatal("per-registration cap did not span processes", n)
	}
}
func TestOriginalMarkerSurvivesReopenAndUnknown(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "original", "registration", providerproto.Validate, "")
	if err := f.journal.reserve(p); err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.startMarker(p, f.owner.OwnerToken()); err != nil {
		t.Fatal(err)
	}
	terminal, old, err := f.journal.terminal(p.attempt)
	if err != nil {
		t.Fatal(err)
	}
	milestone(&terminal, 5, "unknown")
	if err = f.journal.publish(p.attempt, terminal, old); err != nil {
		t.Fatal(err)
	}
	fresh, err := openFixtureJournal(filepath.Join(f.root, "worker-fixture.db"), f.journal.authority)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.db.Close()
	if _, err = fresh.startMarker(p, f.owner.OwnerToken()); err == nil {
		t.Fatal("restart replayed unknown launch")
	}
	next, raw, err := fresh.terminal(p.attempt)
	if err != nil {
		t.Fatal(err)
	}
	unknown := next.Transitions[5]
	milestone(&next, 5, "unknown")
	if next.Transitions[5] != unknown {
		t.Fatal("second uncertainty replaced first")
	}
	next.Transitions[1].At = "changed"
	if fresh.publish(p.attempt, next, raw) == nil {
		t.Fatal("write-once launch stamp rewritten")
	}
	var bytes, remaining int
	if err = fresh.db.QueryRow(`SELECT length(terminal),remaining FROM worker_attempts WHERE id=?`, p.attempt).Scan(&bytes, &remaining); err != nil || bytes+remaining != TerminalCeiling {
		t.Fatal("unknown released future finalization bytes", bytes, remaining, err)
	}
}
func TestActualWaitRemainsRetainedUntilObserved(t *testing.T) {
	f := newFixture(t)
	gate := f.gate()
	f.owner.beforeWait = gate
	p := f.prepared(t, "wait-pending", "registration", providerproto.Validate, "")
	h := f.begin(t, p)
	f.grant(t, h)
	until := time.Now().Add(7 * time.Second)
	for h.Snapshot().Failure != "native_wait_pending" && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	o := h.Snapshot()
	if o.Failure != "native_wait_pending" || o.HelperWaitObserved || o.ScopedNativeClosed {
		t.Fatal("unobserved wait forged join", o)
	}
	if f.owner.RetireFromTrusted(h, modelLeaseReceipt{p.held.worker, p.held.authority, "released", "digest"}, modelLeaseReceipt{p.held.temp, p.held.authority, "released", "digest"}) == nil {
		t.Fatal("pending native ownership retired")
	}
	close(gate)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ScopedNativeClosed {
		t.Fatal("actual late wait observation unavailable", o, err)
	}
	f.settle(t, h)
}

func TestVerifiedHelloDoesNotStartProviderBeforeDurableGrant(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "before-grant", "registration", providerproto.Validate, "witness")
	h := f.begin(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "before-grant-provider-entered")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("provider ran before grant", err)
	}
	if err := h.Execute(ctx, DispatchMarker{}); err == nil {
		t.Fatal("missing durable receipt allowed grant")
	}
	f.grant(t, h)
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess {
		t.Fatal(o, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "entered" {
		t.Fatal("actual provider did not record entry", err)
	}
	f.settle(t, h)
	other, err := NewOwner(f.owner.authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.BeginStart(context.Background(), p, h.marker); err == nil {
		t.Fatal("new owner adopted old native marker")
	}
}

func TestCanonicalRequestSurvivesPrivateJSONEscaping(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "escaped", "registration", providerproto.Validate, "")
	q := p.original.Request()
	q.Context.Reason = " <>& quoted \" exact\x01\n"
	digest, err := providerproto.IntentDigest(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Context.IntentSHA256 = digest
	original, err := providerproto.OriginalFromTrusted(q)
	if err != nil {
		t.Fatal(err)
	}
	p, err = Prepare(p.attempt, p.registration, p.selected, p.held, q, original, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	h := f.begin(t, p)
	f.grant(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess || !o.ScopedNativeClosed {
		t.Fatal("canonical semantic request changed in control framing", o, err)
	}
	f.settle(t, h)
}

func TestWrittenTempUnverifiedReclamationRetainsAllCompensation(t *testing.T) {
	f := newFixture(t)
	p := f.prepared(t, "written-temp", "registration", providerproto.Validate, "temp-write")
	h := f.begin(t, p)
	f.grant(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess || !o.ScopedNativeClosed {
		t.Fatal(o, err)
	}
	if f.compensate(t, h, false) {
		t.Fatal("written temp silently declared reclaimed")
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 128<<20 || status.Reserved.Disk < 64<<20 {
		t.Fatal("unknown compensation released ordinary promises", status.Reserved, err)
	}
	terminal, raw, err := f.journal.terminal(p.attempt)
	if err != nil || terminal.Phase != "unknown" || terminal.BudgetState != "unknown" || terminal.Transitions[6].Phase == "" || terminal.Transitions[7].Phase != "" {
		t.Fatal("native closure became fully settled despite temp responsibility", terminal, err)
	}
	var remaining int
	if err = f.journal.db.QueryRow(`SELECT remaining FROM worker_attempts WHERE id=?`, p.attempt).Scan(&remaining); err != nil || remaining+len(raw) != TerminalCeiling {
		t.Fatal("unknown temp released finalization reserve", remaining, err)
	}
	f.owner.mu.Lock()
	retained := len(f.owner.handles)
	f.owner.mu.Unlock()
	if retained != 1 {
		t.Fatal("unknown temp lost concurrency/native ownership slot")
	}
}
