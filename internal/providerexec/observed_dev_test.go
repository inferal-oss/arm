//go:build arm_devtrace

package providerexec

import (
	"context"
	"encoding/json"
	"github.com/inferal-oss/arm/internal/providerproto"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObservedActualDiagnosticAndLedgerCuts(t *testing.T) {
	f := newFixture(t)
	r := &traceRecorder{}
	f.owner.trace = observer{r}
	f.journal.trace = observer{r}
	p := f.prepared(t, "observed", "registration", providerproto.Validate, "")
	h := f.begin(t, p)
	f.grant(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	o, err := h.WaitObservation(ctx)
	if err != nil || !o.ProtocolSuccess || !o.ScopedNativeClosed {
		t.Fatal(o, err)
	}
	raw, _ := json.Marshal(o)
	f.owner.trace.raw("actual_native_observation", p.attempt, raw)
	f.settle(t, h)
	events, err := r.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	required := []string{"ordinary_worker_granted", "managed_temp_granted", "durable_reserve_ack", "durable_start_marker_ack", "handle_retained", "native_start_begun", "native_start_returned", "hello_verified", "durable_helper_ready_ack", "durable_dispatching_ack", "grant_published", "provider_exit_observed", "group_signal_before_reap", "helper_reaped", "actual_native_observation", "positive_worker_release", "positive_temp_cleanup", "durable_settled_ack", "fully_settled"}
	for _, kind := range required {
		found := false
		for _, e := range events {
			found = found || e.Kind == kind
		}
		if !found {
			t.Fatal("missing actual source cut", kind)
		}
	}
	if path := os.Getenv("ARM_PROVIDER_WORKER_TRACE_OUT"); path != "" {
		if err := workerFormalWrite(path, events); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual worker trace: events=%d owned_bytes=%d", len(events), r.bytes)
}

// These are actual OS/SQLite/manager prefixes, not reducer-produced model paths.
// Gates hold wrapper entry only; no claim is made that a kernel call was stuck.
func TestObservedWorkerOwnershipScenarios(t *testing.T) {
	for _, target := range []string{"success", "cancel_before_grant", "cancel_during_start", "wait_pending", "group_pending", "invalid_output", "pin_replaced", "written_temp"} {
		t.Run(target, func(t *testing.T) {
			f := newFixture(t)
			r := &traceRecorder{}
			f.owner.trace, f.journal.trace = observer{r}, observer{r}
			mode := ""
			if target == "invalid_output" {
				mode = "extra"
			}
			if target == "written_temp" {
				mode = "temp-write"
			}
			p := f.prepared(t, "observed", "registration", providerproto.Validate, mode)
			var gate chan struct{}
			if target == "cancel_during_start" {
				gate = f.gate()
				f.owner.beforeStart = gate
			}
			if target == "wait_pending" {
				gate = f.gate()
				f.owner.beforeWait = gate
			}
			if target == "group_pending" {
				gate = f.gate()
				f.owner.beforeGroupAbsence = gate
			}
			if target == "pin_replaced" {
				if err := os.Rename(f.provider.path, f.provider.path+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.provider.path, []byte("replacement"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			h := f.begin(t, p)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			switch target {
			case "cancel_during_start":
				h.Cancel()
				short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
				pending, waitErr := h.WaitObservation(short)
				stop()
				if waitErr == nil || !pending.StartPending || pending.KnownNotStarted || pending.ScopedNativeClosed {
					t.Fatal("retained Start uncertainty forged closure", pending, waitErr)
				}
				f.observedUnknown(t, h)
				close(gate)
			case "cancel_before_grant":
				if err := h.Hello(ctx); err != nil {
					t.Fatal(err)
				}
				h.Cancel()
			case "pin_replaced":
			default:
				f.grant(t, h)
			}
			if target == "wait_pending" {
				until := time.Now().Add(6 * time.Second)
				for h.Snapshot().Failure != "native_wait_pending" && time.Now().Before(until) {
					time.Sleep(10 * time.Millisecond)
				}
				if o := h.Snapshot(); o.HelperWaitObserved || o.ScopedNativeClosed || o.Failure != "native_wait_pending" {
					t.Fatal("unobserved Wait became closure", o)
				}
				f.observedUnknown(t, h)
				close(gate)
			}
			if target == "group_pending" {
				until := time.Now().Add(8 * time.Second)
				for !h.Snapshot().HelperWaitObserved && time.Now().Before(until) {
					time.Sleep(10 * time.Millisecond)
				}
				if o := h.Snapshot(); !o.HelperWaitObserved || o.GroupAbsentObserved || o.ScopedNativeClosed {
					t.Fatal("post-reap group uncertainty forged closure", o)
				}
				short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
				_, waitErr := h.WaitObservation(short)
				stop()
				if waitErr == nil {
					t.Fatal("pending group observation returned final receipt")
				}
				f.observedUnknown(t, h)
				close(gate)
			}
			o, err := h.WaitObservation(ctx)
			if err != nil || !o.ScopedNativeClosed {
				t.Fatal(o, err)
			}
			if target == "success" || target == "written_temp" {
				if !o.ProtocolSuccess {
					t.Fatal(o)
				}
			}
			if target == "cancel_during_start" || target == "cancel_before_grant" {
				if o.GrantPublished {
					t.Fatal("cancelled prefix dispatched", o)
				}
			}
			raw, err := json.Marshal(o)
			if err != nil {
				t.Fatal(err)
			}
			f.owner.trace.raw("actual_native_observation", p.attempt, raw)
			if target == "written_temp" {
				if f.compensate(t, h, false) {
					t.Fatal("written-temp ownership dropped")
				}
			} else {
				f.settle(t, h)
			}
			events, err := r.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("ARM_PROVIDER_WORKER_TRACE_DIR"); dir != "" {
				if err := workerFormalWrite(filepath.Join(dir, target+".json"), events); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("actual target=%s events=%d owned_bytes=%d", target, len(events), r.bytes)
		})
	}
}
