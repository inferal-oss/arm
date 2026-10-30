package nativeowner

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The maintained compiler-overlay harness enables only these explicit controls.
// Original production methods have no test hook. Each control owns a whole
// fixture child; abnormal original state is never converted into native Closed.
const expectNativeValidationPause = false

func nativeValidationControls() (string, func(), <-chan struct{}, <-chan struct{}, func(), func() int64) {
	return "", func() {}, nil, nil, func() {}, func() int64 { return 0 }
}

var validationFixtureChild = flag.Bool("native-validation-fixture-child", false, "private original validation fixture")
var validationFixtureHome = flag.String("native-validation-fixture-home", "", "parent-owned validation fixture")

func originalValidationFixture(t *testing.T, body func(*ServeIssuer, *ManagerClaim, *ServerClaim)) {
	t.Helper()
	if !expectNativeValidationPause {
		t.Skip("requires explicit owned original-validation compiler overlay")
	}
	if !*validationFixtureChild {
		// Only the parent owns directory cleanup, after waiting its exact child.
		home := nativeFixture(t, 0600)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-native-validation-fixture-child", "-native-validation-fixture-home="+home)
		output, err := command.CombinedOutput()
		if err != nil || command.ProcessState == nil || !command.ProcessState.Exited() || command.ProcessState.ExitCode() != 0 || !strings.Contains(string(output), "original native validation fixture joined\n") {
			t.Fatalf("original native validation fixture failed: %v\n%s", err, output)
		}
		return
	}
	issuer, err := NewServePending(*validationFixtureHome)
	if err != nil {
		t.Fatal(err)
	}
	if err = issuer.OpenOriginal(); err != nil {
		t.Fatal(err)
	}
	mv, err := issuer.ManagerView()
	if err != nil {
		t.Fatal(err)
	}
	mc, err := mv.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	sv, err := issuer.ServerView()
	if err != nil {
		t.Fatal(err)
	}
	sc, err := sv.ClaimOriginal()
	if err != nil {
		t.Fatal(err)
	}
	body(issuer, mc, sc)
	// Original objects survive through the fixture's own process boundary. No
	// RetireOriginal/Close/borrower shutdown or product-resource credit is issued.
	println("original native validation fixture joined")
}

func TestOriginalNativeValidationQueuedCancellationCapacityAndFreshWake(t *testing.T) {
	originalValidationFixture(t, func(issuer *ServeIssuer, mc *ManagerClaim, sc *ServerClaim) {
		mode, arm, reached, queued, release, count := nativeValidationControls()
		if mode != "pause" {
			t.Fatal("wrong explicit original validation control mode")
		}
		before := count()
		arm()
		originalDone := make(chan error, 1)
		var helpers sync.WaitGroup
		helpers.Add(1)
		go func() { defer helpers.Done(); _, err := mc.ValidateOriginalContext(t.Context()); originalDone <- err }()
		var helperDone <-chan struct{}
		cancelledCtx, cancelQueued := context.WithCancel(t.Context())
		cancelResults := make(chan error, maxNativeValidationWaiters)
		queuedStarted := 0
		var freshDone <-chan error
		defer func() {
			defer helpers.Wait() // join actual helpers after release/cancellation
			cancelQueued()
			release()
			select {
			case err := <-originalDone:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(2 * time.Second):
				t.Error("original validation fixture did not join")
			}
			for i := 0; i < queuedStarted; i++ {
				select {
				case <-cancelResults:
				case <-time.After(2 * time.Second):
					t.Error("original queued helper did not join")
				}
			}
			if freshDone != nil {
				select {
				case err := <-freshDone:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(2 * time.Second):
					t.Error("fresh validation helper did not join")
				}
			}
			if helperDone != nil {
				select {
				case <-helperDone:
				case <-time.After(2 * time.Second):
					t.Error("original inspector helper did not join")
				}
			}
		}()
		select {
		case <-reached:
		case <-time.After(time.Second):
			t.Fatal("actual original validation pause not reached")
		}
		inspected := make(chan Inspection, 1)
		inspectionDone := make(chan struct{})
		helperDone = inspectionDone
		helpers.Add(1)
		go func() {
			defer helpers.Done()
			defer close(inspectionDone)
			seen, _ := issuer.InspectOriginal()
			inspected <- seen
		}()
		select {
		case seen := <-inspected:
			if !seen.OperationPending || !seen.NativeCensusUncertain || seen.OriginalRetirement.Complete {
				t.Fatal("original pending validation custody hidden", seen)
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("inspection blocked behind original borrower validation")
		}
		o := issuer.owner
		o.mu.Lock()
		original := o.active
		o.mu.Unlock()
		for i := 0; i < maxNativeValidationWaiters; i++ {
			queuedStarted++
			helpers.Add(1)
			go func() { defer helpers.Done(); _, err := sc.ValidateOriginalContext(cancelledCtx); cancelResults <- err }()
			select {
			case <-queued:
			case <-time.After(time.Second):
				t.Fatal("actual bounded waiter not registered")
			}
		}
		if _, err := sc.ValidateOriginalContext(t.Context()); !errors.Is(err, ErrNativeValidationBusy) {
			t.Fatal("original waiter capacity was not enforced", err)
		}
		cancelQueued()
		for queuedStarted > 0 {
			select {
			case err := <-cancelResults:
				queuedStarted--
				if !errors.Is(err, context.Canceled) {
					t.Fatal("queued cancellation outcome differs", err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("queued cancellation waited for original native validation")
			}
		}
		o.mu.Lock()
		same := o.active == original && !original.abnormal && len(o.validationWaiters) == 0
		o.mu.Unlock()
		if !same || count() != before+1 {
			t.Fatal("queued cancellation replaced original native validation")
		}
		freshResult := make(chan error, 1)
		freshDone = freshResult
		helpers.Add(1)
		go func() { defer helpers.Done(); _, err := sc.ValidateOriginalContext(t.Context()); freshResult <- err }()
		select {
		case <-queued:
		case <-time.After(time.Second):
			t.Fatal("fresh waiter not registered")
		}
		release()
		select {
		case err := <-originalDone:
			originalDone <- err
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("original normal validation did not return")
		}
		select {
		case err := <-freshResult:
			freshResult <- err
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("fresh native validation did not return")
		}
		if count() != before+2 {
			t.Fatal("wake reused cached authority instead of actual fresh native validation")
		}
	})
}

func TestOriginalNativeValidationAbnormalRetainsOriginalAndRefusesReplacement(t *testing.T) {
	originalValidationFixture(t, func(issuer *ServeIssuer, mc *ManagerClaim, sc *ServerClaim) {
		mode, arm, reached, _, release, count := nativeValidationControls()
		if mode != "panic" && mode != "goexit" {
			t.Fatal("wrong explicit abnormal validation control mode")
		}
		before := count()
		arm()
		done := make(chan struct{})
		var cause any
		go func() {
			defer close(done)
			defer func() { cause = recover() }()
			_, _ = mc.ValidateOriginalContext(t.Context())
		}()
		defer func() {
			release()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("original abnormal fixture did not join")
			}
		}()
		select {
		case <-reached:
		case <-time.After(time.Second):
			t.Fatal("actual abnormal original pause not reached")
		}
		o := issuer.owner
		o.mu.Lock()
		original := o.active
		o.mu.Unlock()
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("original abnormal call did not return")
		}
		o.mu.Lock()
		retained := o.active == original && original.abnormal && o.failure != nil
		o.mu.Unlock()
		if !retained {
			t.Fatal("original abnormal native validation was forgotten")
		}
		if mode == "panic" && cause != "original validation fixture panic" {
			t.Fatal("original native panic cause changed", cause)
		}
		if mode == "goexit" && cause != nil {
			t.Fatal("Goexit unexpectedly acquired panic cause", cause)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if _, err := sc.ValidateOriginalContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("abnormal original admitted a replacement", err)
		}
		if count() != before+1 {
			t.Fatal("abnormal original replaced with a new actual validation")
		}
	})
}
