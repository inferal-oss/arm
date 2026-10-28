package kubeprovision

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// These are controlled factory/transport witnesses, not native admission or a
// helper process. The native Manager constructor is tested separately.
type managedFactoryFixture struct {
	t                  *testing.T
	payload            *managedPayloadFixture
	deny, closeErr     error
	unknown, loseReply bool
	installed          bool
	payloads, ships    int
	calls              []reviewedExecKind
}

type managedPayloadFixture struct {
	*os.File
	closes int
	err    error
}

func (f *managedPayloadFixture) Close() error {
	f.closes++
	return errors.Join(f.File.Close(), f.err)
}

func (f *managedFactoryFixture) NewPayload(context.Context) (HeldSourceFile, error) {
	if f.deny != nil {
		return nil, f.deny
	}
	f.payloads++
	file, err := os.Create(filepath.Join(f.t.TempDir(), "controlled-payload"))
	if err != nil {
		return nil, err
	}
	f.payload = &managedPayloadFixture{File: file, err: f.closeErr}
	f.t.Cleanup(func() {
		if f.payload.closes == 0 {
			_ = f.payload.Close()
		}
	})
	return f.payload, nil
}

func (f *managedFactoryFixture) RunFixedExec(_ context.Context, call *NativeExecCall, payload HeldSourceFile) (OriginalExecJoin, error) {
	f.calls = append(f.calls, call.kind)
	join := OriginalExecJoin{call: call, returned: true, closed: !f.unknown}
	switch call.kind {
	case execShip:
		if payload != f.payload {
			return join, errors.New("wrong original payload loan")
		}
		b, err := io.ReadAll(payload)
		if err != nil || string(b) != "payload" {
			return join, errors.New("wrong controlled payload bytes")
		}
		f.ships++
		f.installed = true
		if f.loseReply {
			return join, context.DeadlineExceeded
		}
	case execLookupBinary:
		if payload != nil || !f.installed {
			return join, errors.New("no exact installed payload")
		}
		join.output = "ARM_BINARY_VERIFIED\n"
	default:
		return join, errors.New("unexpected controlled helper purpose")
	}
	return join, nil
}

func TestManagedSourceShipmentUsesOriginalFactoryAndNoReplay(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "lost-reply"}[lost], func(t *testing.T) {
			p, plan := sourcePayloadFixture(t)
			files := &managedFactoryFixture{t: t, loseReply: lost}
			source, err := NewManagedSourceExec(plan, files)
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, strings.NewReader("payload"), source)
			if lost {
				if err == nil || r.BinaryPhase != "ship_attempted" {
					t.Fatal("lost reply became success", r, err)
				}
				// An original-key second call must only look up the existing
				// payload; even the caller's reader is not consumed again.
				r, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, sourceInputFunc(func([]byte) (int, error) {
					t.Error("original-key lookup reread payload")
					return 0, errors.New("unexpected payload read")
				}), source)
			}
			if err != nil || r.BinaryPhase != "binary_verified" || files.payloads != 1 || files.ships != 1 || files.payload.closes != 1 {
				t.Fatal("source factory/close/replay mismatch", r, files.payloads, files.ships, files.payload.closes, err)
			}
		})
	}
}

func TestManagedSourcePreparationRefusalBeforePayloadRead(t *testing.T) {
	p, plan := sourcePayloadFixture(t)
	denied := errors.New("original source factory refused")
	files := &managedFactoryFixture{t: t, deny: denied}
	source, err := NewManagedSourceExec(plan, files)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, sourceInputFunc(func([]byte) (int, error) {
		t.Error("payload read before original source factory accepted")
		return 0, io.EOF
	}), source)
	if !errors.Is(err, denied) || files.payloads != 0 || len(files.calls) != 0 {
		t.Fatal("source refusal reached payload/helper", err)
	}
	r, _, err := p.Journal.Read(t.Context(), plan.Review.Key)
	if err != nil || r.BinaryPhase != "" {
		t.Fatal("refused preparation advanced target intent", r, err)
	}
}

func TestManagedSourceUncertaintyRetainsJournalAndRefusesReplay(t *testing.T) {
	for _, mode := range []string{"close", "join"} {
		t.Run(mode, func(t *testing.T) {
			p, plan := sourcePayloadFixture(t)
			files := &managedFactoryFixture{t: t, unknown: mode == "join"}
			if mode == "close" {
				files.closeErr = errors.New("original payload close uncertain")
			}
			source, err := NewManagedSourceExec(plan, files)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, strings.NewReader("payload"), source)
			if !errors.Is(err, ErrHelperCleanupUnknown) {
				t.Fatal("original uncertainty lost its class", err)
			}
			r, _, err := p.Journal.Read(t.Context(), plan.Review.Key)
			if err != nil || !r.SourceCleanupUnknown || r.BinaryPhase != "ship_attempted" || files.payload.closes != 1 {
				t.Fatal("target proof erased source uncertainty", r, files.payload.closes, err)
			}
			before := len(files.calls)
			_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, strings.NewReader("payload"), source)
			if !errors.Is(err, ErrSourceOwnerUnavailable) || len(files.calls) != before || files.payloads != 1 {
				t.Fatal("unknown original allowed another helper or payload", err)
			}
		})
	}
}

func TestManagedSourceRejectsUnreviewedCommandAndForeignPayload(t *testing.T) {
	p, plan := sourcePayloadFixture(t)
	pod, err := p.ExactPod(t.Context(), plan.Review.Key)
	if err != nil {
		t.Fatal(err)
	}
	files := &managedFactoryFixture{t: t}
	source, err := NewManagedSourceExec(plan, files)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Exec(t.Context(), pod, []string{"/bin/sh", "-c", "arbitrary"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("arbitrary native command admitted")
	}
	command, err := fixedExecCommand(plan, pod, execShip)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Exec(t.Context(), pod, command, strings.NewReader("payload"), io.Discard, io.Discard); err == nil || len(files.calls) != 0 {
		t.Fatal("foreign payload reached original factory", err)
	}
}

type managedRefusalJournal struct {
	FileJournal
	refuse bool
}

func (j *managedRefusalJournal) Update(ctx context.Context, key string, revision uint64, change func(*Record) error) (Record, error) {
	if j.refuse {
		j.refuse = false // known refusal, no durable target mutation
		return Record{}, ErrJournalConflict
	}
	return j.FileJournal.Update(ctx, key, revision, change)
}

func TestManagedPreparationCleanupFenceSurvivesFreshFactory(t *testing.T) {
	for _, point := range []string{"spool", "pod", "target-cas"} {
		for _, uncertain := range []bool{false, true} {
			name := point + "/closed"
			if uncertain {
				name = point + "/uncertain"
			}
			t.Run(name, func(t *testing.T) {
				p, plan := sourcePayloadFixture(t)
				if point == "pod" {
					gets := 0
					client := p.API.(*ClientGo).client.(*fake.Clientset)
					client.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
						gets++
						if gets == 2 {
							return true, nil, errors.New("controlled post-spool exact Pod refusal")
						}
						return false, nil, nil
					})
				}
				if point == "target-cas" {
					p.Journal = &managedRefusalJournal{FileJournal: p.Journal.(FileJournal), refuse: true}
				}
				original := &managedFactoryFixture{t: t}
				if uncertain {
					original.closeErr = errors.New("controlled original payload close uncertainty")
				}
				source, err := NewManagedSourceExec(plan, original)
				if err != nil {
					t.Fatal(err)
				}
				payload := "payload"
				if point == "spool" {
					payload = "changed" // same size; actual digest refusal
				}
				_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, strings.NewReader(payload), source)
				if err == nil || errors.Is(err, ErrHelperCleanupUnknown) != uncertain || original.payloads != 1 || original.payload.closes != 1 || len(original.calls) != 0 {
					t.Fatal("pre-exec refusal did not preserve original closure class", err, original.payloads, original.payload.closes, original.calls)
				}
				r, ok, err := p.Journal.Read(t.Context(), plan.Review.Key)
				if err != nil || !ok || r.SourceCleanupUnknown != uncertain || r.BinaryPhase != "" {
					t.Fatal("durable preparation custody differs", r, ok, err)
				}
				fresh := &managedFactoryFixture{t: t}
				next, err := NewManagedSourceExec(plan, fresh)
				if err != nil {
					t.Fatal(err)
				}
				read := false
				input := strings.NewReader("payload")
				_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, sourceInputFunc(func(b []byte) (int, error) {
					read = true
					return input.Read(b)
				}), next)
				if uncertain {
					if !errors.Is(err, ErrSourceOwnerUnavailable) || read || fresh.payloads != 0 || len(fresh.calls) != 0 {
						t.Fatal("new factory bypassed original preparation uncertainty", err, read, fresh.payloads, fresh.calls)
					}
				} else if err != nil || !read || fresh.payloads != 1 || fresh.ships != 1 {
					t.Fatal("known closed preparation lost normal retry", err, read, fresh.payloads, fresh.ships)
				}
			})
		}
	}
}

// The interposition is before the locked owner acquisition, after ShipBinary
// read its candidate target. It changes the actual durable target revision.
type managedPreparationRaceJournal struct {
	FileJournal
	before func()
}

func (j managedPreparationRaceJournal) beginSourcePreparation(ctx context.Context, key, plan, pod string, revision uint64) (*sourceExecGuard, error) {
	j.before()
	return j.FileJournal.beginSourcePreparation(ctx, key, plan, pod, revision)
}

func TestManagedPreparationBindsTargetBeforePayloadAllocation(t *testing.T) {
	for _, point := range []string{"target-attempt", "revision-only"} {
		t.Run(point, func(t *testing.T) {
			p, plan := sourcePayloadFixture(t)
			base := p.Journal.(FileJournal)
			before, _, err := base.Read(t.Context(), plan.Review.Key)
			if err != nil {
				t.Fatal(err)
			}
			p.Journal = managedPreparationRaceJournal{FileJournal: base, before: func() {
				_, err := base.Update(t.Context(), plan.Review.Key, before.Revision, func(next *Record) error {
					if point == "target-attempt" {
						next.BinaryPhase = "ship_attempted"
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}}
			files := &managedFactoryFixture{t: t}
			source, err := NewManagedSourceExec(plan, files)
			if err != nil {
				t.Fatal(err)
			}
			read := false
			_, err = p.ShipBinaryWithManagedSource(t.Context(), plan.Review.Key, sourceInputFunc(func([]byte) (int, error) {
				read = true
				return 0, io.EOF
			}), source)
			if !errors.Is(err, ErrJournalConflict) || read || files.payloads != 0 || len(files.calls) != 0 {
				t.Fatal("stale target consumed a new original payload/helper", err, read, files.payloads, files.calls)
			}
			after, _, err := base.Read(t.Context(), plan.Review.Key)
			want := ""
			if point == "target-attempt" {
				want = "ship_attempted"
			}
			if err != nil || after.SourceCleanupUnknown || after.Revision != before.Revision+1 || after.BinaryPhase != want {
				t.Fatal("refused preparation altered the concurrent target or retained a nonexistent source", after, err)
			}
		})
	}
}

// The clock below is a controlled native-entitlement seam, not a physical
// capacity/suspend claim. RunOriginal and both actual Start/G gates are real.
func TestManagedProviderWaitResamplesNativeAuthority(t *testing.T) {
	t.Setenv("ARM_KUBE_SOURCE_TEST_HELPER", "1")
	for _, point := range []string{"before-start", "before-grant"} {
		t.Run(point, func(t *testing.T) {
			var effects atomic.Int32
			plan, pod, closeServer := ownedExecServer(t, func(w http.ResponseWriter, r *http.Request) {
				effects.Add(1)
				execServerEffect(t, "ARM_BINARY_VERIFIED\n", nil, false)(w, r)
			})
			defer closeServer()
			owned, err := NewOwnedExec(plan)
			if err != nil {
				t.Fatal(err)
			}
			call, err := owned.NewSourceCall(pod, "lookup_binary")
			if err != nil {
				t.Fatal(err)
			}
			request := sourceCallFile(t, call)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			var now, providerCalls atomic.Int64
			const expiry = int64(100)
			expired := errors.New("controlled original native deadline passed")
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			call.providerCurrent = func(ctx context.Context) error {
				n := providerCalls.Add(1)
				if point == "before-start" && n == 2 || point == "before-grant" && n == 3 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}
			type outcome struct {
				join OriginalExecJoin
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				join, err := call.RunOriginal(ctx, request, nil, func(context.Context) error {
					if now.Load() >= expiry {
						return expired
					}
					return nil
				})
				done <- outcome{join, err}
			}()
			joined := false
			defer func() {
				unblock()
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("original controlled worker did not return after cancellation")
					}
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider guard did not reach selected actual native boundary")
			}
			if ctx.Err() != nil {
				t.Fatal("fixture context expired before controlled native clock")
			}
			now.Store(expiry + 1)
			unblock()
			var result outcome
			select {
			case result = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("original native worker did not join after expired provider wait")
			}
			if !errors.Is(result.err, expired) || !result.join.Matches(call) || !result.join.Closed() || effects.Load() != 0 || ctx.Err() != nil {
				t.Fatal("provider wait spent stale native authority", result.join, result.err, effects.Load(), ctx.Err())
			}
			call.mu.Lock()
			started := call.cmd != nil && call.cmd.Process != nil
			call.mu.Unlock()
			if started != (point == "before-grant") {
				t.Fatal("fixture did not exercise the intended Start/hello boundary", point, started)
			}
		})
	}
}
