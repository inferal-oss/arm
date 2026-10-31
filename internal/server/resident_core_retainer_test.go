package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const residentRetainerHelperMarker = "arm-test-pure-retainer"
const residentRetainerObserved = "pure-retainer-observed\n"

// These helpers use the SAME real deferred retainer, never Run's genuine role.
// Only the actual PURE static entry/Root is claimed/published: no fixed13 claim,
// initialization, seal, control, grant, native issuer, SQL or Manager is invoked.
// Production receives no callback, test escape, environment check or finite wait.
func TestResidentCoreRetainerOriginalUnwind(t *testing.T) {
	if !filepath.IsAbs(os.Args[0]) {
		t.Fatal("absolute owned Go-test executable required")
	}
	for _, scenario := range []string{"normal", "error", "panic", "nilpanic-default", "nilpanic-legacy", "goexit"} {
		if !t.Run(scenario, func(t *testing.T) {
			runResidentRetainerHelper(t, scenario)
		}) {
			return // no later helper after any failed/unknown original outcome
		}
	}
}

type residentRetainerHelperOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

// One shared bounded stdout/stderr sink; Write never blocks on an observer.
func (o *residentRetainerHelperOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	remaining := 4096 - len(o.data)
	if len(p) > remaining {
		o.data = append(o.data, p[:remaining]...)
		o.overflow = true
	} else {
		o.data = append(o.data, p...)
	}
	return len(p), nil
}

type residentRetainerHelperRun struct {
	command                           *exec.Cmd
	output                            residentRetainerHelperOutput
	context                           context.Context
	cancel                            context.CancelFunc
	timeoutAttempted, timeoutReturned bool
	startAttempted, startReturned     bool
	waitAttempted, waitReturned       bool
	killAttempted, killReturned       bool
	cancelAttempted, cancelReturned   bool
	startError, waitError, killError  error
}

// Retain all six original helper owners through parent-test unwind, including
// unknown Start/Wait outcomes. No record replacement, lookup reconstruction or
// second helper launch follows an uncertain original attempt.
var residentRetainerHelperOwners struct {
	mu       sync.Mutex
	original [6]*residentRetainerHelperRun
	used     int
}

func runResidentRetainerHelper(t *testing.T, scenario string) {
	t.Helper()
	run := &residentRetainerHelperRun{} // exact original test-process owner
	defer run.finishOriginal(t)         // installed BEFORE timer/Start attempts
	residentRetainerHelperOwners.mu.Lock()
	if residentRetainerHelperOwners.used == len(residentRetainerHelperOwners.original) {
		residentRetainerHelperOwners.mu.Unlock()
		t.Fatal("bounded original helper roster exhausted")
	}
	residentRetainerHelperOwners.original[residentRetainerHelperOwners.used] = run
	residentRetainerHelperOwners.used++
	residentRetainerHelperOwners.mu.Unlock()
	run.timeoutAttempted = true
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	run.context, run.cancel, run.timeoutReturned = ctx, cancel, true
	run.command = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResidentCoreRetainerHelper$", "-test.count=1", "-test.timeout=3s", "--", residentRetainerHelperMarker, scenario)
	command := run.command // retained before actual Start; no shell or role argv
	command.WaitDelay = 250 * time.Millisecond
	command.Env = []string{"GOMAXPROCS=1", "GOMEMLIMIT=128MiB", "GODEBUG=panicnil=0"}
	if scenario == "nilpanic-legacy" {
		command.Env[2] = "GODEBUG=panicnil=1" // Go-test fault semantics only; no production escape
	}
	command.Stdout, command.Stderr = &run.output, &run.output
	// ExtraFiles/Stdin are nil. The helper never launches any child process;
	// its one exact Process owns all helper threads/goroutines. Context cancellation
	// kills only this original Process, and its sole Wait joins exec's pipe tasks.
	run.startAttempted = true
	run.startError = command.Start()
	run.startReturned = true
	if run.startError != nil {
		t.Fatal("owned retainer helper Start failed")
	}
	run.waitAttempted = true
	run.waitError = command.Wait()
	run.waitReturned = true
	if run.waitError != nil || ctx.Err() != nil || command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("owned retainer helper did not join successfully within its deadline")
	}
	run.output.mu.Lock()
	valid := !run.output.overflow && string(run.output.data) == residentRetainerObserved
	run.output.mu.Unlock()
	if !valid {
		t.Fatal("helper did not observe the actual retained original frame")
	}
}

// One original teardown: no reconstructed PID, second Start or second Wait.
// A nonreturned Wait remains unknown; timeout/Exit/Wait is never core/native Closed.
func (r *residentRetainerHelperRun) finishOriginal(t *testing.T) {
	t.Helper()
	if r.command != nil && r.command.Process != nil && !r.waitReturned {
		r.killAttempted = true
		r.killError = r.command.Process.Kill()
		r.killReturned = true
		if !r.waitAttempted {
			r.waitAttempted = true
			r.waitError = r.command.Wait()
			r.waitReturned = true
		} // never retry an already attempted but unreturned original Wait
	}
	if r.cancel != nil {
		r.cancelAttempted = true
		r.cancel()
		r.cancelReturned = true
	}
	if r.command != nil && r.command.Process != nil && !r.waitReturned {
		t.Error("original helper Wait outcome remains unknown")
	}
}

func TestResidentCoreRetainerHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != residentRetainerHelperMarker {
		t.Skip("owned helper subprocess only")
	}
	scenario := os.Args[len(os.Args)-1]
	switch scenario {
	case "normal", "error", "panic", "nilpanic-default", "nilpanic-legacy", "goexit":
	default:
		os.Exit(90)
	}
	e := &processResidentCoreEntry
	call := &residentCoreEntryCall{keeperRegistered: true}
	normal := false
	var resultErr error
	defer e.retainOriginal(call, &normal, &resultErr) // SAME production deferred method
	call.claimAttempted = true
	claimed := e.claimOriginal(call)
	e.mu.Lock()
	call.claimReturned, call.claimAccepted = true, claimed
	e.mu.Unlock()
	if !claimed {
		os.Exit(91)
	}
	root, err := e.publishOriginalRoot(call) // pure only; NEVER initializeOriginal
	if err != nil {
		os.Exit(91)
	}
	returnedError := errors.New("pure test returned error")
	panicCause := &struct{ value string }{value: "private-pure-test-panic"}
	go observeResidentRetainer(e, root, call, scenario, returnedError, panicCause)
	switch scenario {
	case "normal":
		normal = true
		return
	case "error":
		resultErr = returnedError
		normal = true
		return
	case "panic":
		panic(panicCause)
	case "nilpanic-default", "nilpanic-legacy":
		panic(nil)
	case "goexit":
		runtime.Goexit()
	}
}

// Test-only observer of its OWN actual objects and blocked original caller.
// Its Exit completes only this disposable pure test; no native custody/grant or
// core-ready/Closed/release observation exists. The exact parent kills/joins it
// if this bounded observer never confirms the SAME production retainer/frame.
func observeResidentRetainer(e *residentCoreEntry, root *residentChildPending, call *residentCoreEntryCall, scenario string, returnedError error, panicCause any) {
	stack := make([]byte, 64<<10)
	for attempt := 0; attempt < 100; attempt++ {
		e.mu.Lock()
		pure := e == &processResidentCoreEntry && e.call == call && e.root == root && call.claimAccepted && call.initializationAttempted && !call.initializationReturned && !call.prepareAttempted && root.entry == e && root.self == nil && root.mu == nil && root.refs == nil && root.control == nil && root.service == nil && root.fence == nil && root.image == nil && root.native == nil && root.manager == nil && root.server == nil && root.bootstrapCall == nil
		retained := call.keeperRegistered && call.keeperEntered && call.waitPending && call.waitAttempts == 1 && call.waitReturns == 0
		outcome := false
		if retained {
			switch scenario {
			case "normal":
				outcome = call.bodyReturned && !call.abnormal && call.returnedError == nil && call.cause == nil
			case "error":
				outcome = call.bodyReturned && !call.abnormal && call.returnedError == returnedError && call.cause == nil
			case "panic":
				outcome = !call.bodyReturned && call.abnormal && call.returnedError == nil && call.cause == panicCause
			case "nilpanic-default":
				_, actualNilPanic := call.cause.(*runtime.PanicNilError)
				outcome = !call.bodyReturned && call.abnormal && call.returnedError == nil && actualNilPanic
			case "nilpanic-legacy", "goexit":
				outcome = !call.bodyReturned && call.abnormal && call.returnedError == nil && call.cause == nil
			}
		}
		e.mu.Unlock()
		if !pure || (retained && !outcome) || e.ownsPreparingRoot(root) {
			os.Exit(92)
		}
		if retained {
			n := runtime.Stack(stack, true)
			if n == len(stack) {
				os.Exit(93)
			}
			for _, goroutine := range bytes.Split(stack[:n], []byte("\n\n")) {
				if bytes.Contains(goroutine, []byte("[sleep]")) && bytes.Contains(goroutine, []byte("(*residentCoreEntry).retainOriginal(")) && bytes.Contains(goroutine, []byte(".TestResidentCoreRetainerHelper(")) {
					_, err := os.Stdout.WriteString(residentRetainerObserved)
					if err != nil {
						os.Exit(94)
					}
					os.Exit(0)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(95)
}
