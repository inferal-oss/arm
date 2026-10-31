package server

import (
	"context"
	"sync"
	"time"

	"getarm.org/internal/mesh"
)

// ResidentCoreRole is the exact private same-binary role, selected before any
// legacy CLI, bundle validation or companion/helper construction.
const ResidentCoreRole = mesh.ResidentP1CoreRole

// This is the ONE original entry owner, retained independently of stack unwind.
// Neither serialized observations nor another entry can reconstruct it.
var processResidentCoreEntry residentCoreEntry

type residentCoreEntry struct {
	mu   sync.Mutex
	call *residentCoreEntryCall
	root *residentChildPending
}
type residentCoreEntryCall struct {
	claimAttempted, claimReturned, claimAccepted    bool
	initializationAttempted, initializationReturned bool
	prepareAttempted, bodyReturned, abnormal        bool
	validationAttempted, validationReturned         bool
	rootBoundAttempted, rootBoundReturned           bool
	returnedError                                   error
	cause                                           any // private, never formatted or logged
	keeperRegistered, keeperEntered, waitPending    bool
	waitAttempts, waitReturns                       uint64 // saturating metadata; no closure witness
}

func originalResidentCoreArgs(args []string) bool {
	return len(args) == 1 && args[0] == ResidentCoreRole
}
func (e *residentCoreEntry) claimOriginal(call *residentCoreEntryCall) bool {
	if e != &processResidentCoreEntry || call == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.call != nil {
		return false
	}
	e.call = call
	return true
}

// Pure publication in the original entry. Defer unlock BEFORE allocation so
// abnormal unwind cannot strand the outer retainer behind this observation lock.
func (e *residentCoreEntry) publishOriginalRoot(call *residentCoreEntryCall) (*residentChildPending, error) {
	if e != &processResidentCoreEntry || call == nil {
		return nil, errResidentChildOriginal
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.call != call || e.root != nil || call.initializationAttempted {
		return nil, errResidentChildOriginal
	}
	call.initializationAttempted = true
	e.root = &residentChildPending{entry: e} // actual field BEFORE called initialization
	return e.root, nil
}

// Pure actual-entry linkage only. A caller flag, copied Root or another owner
// cannot select the genuine entry's additional bootstrap timeout construction.
func (e *residentCoreEntry) ownsPreparingRoot(root *residentChildPending) bool {
	if e != &processResidentCoreEntry || root == nil || root.self != root || root.entry != e {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.root == root && e.call != nil && e.call.initializationAttempted && e.call.initializationReturned && e.call.prepareAttempted && !e.call.bodyReturned && !e.call.abnormal && !e.call.keeperEntered
}

// Directly deferred by the actual caller; recover must stay HERE. This is the
// same original caller's deferred frame, never a caretaker or callback. Tests
// can exercise it with a pure entry without constructing inherited authority.
func (e *residentCoreEntry) retainOriginal(call *residentCoreEntryCall, normal *bool, resultErr *error) {
	cause := recover()
	e.mu.Lock()
	if e.call != call && (!call.claimAttempted || (*normal && call.claimReturned && !call.claimAccepted)) {
		// Only a known pure rejection may return. An abnormal/unreturned
		// actual claim attempt retains this same frame even with nil fields.
		e.mu.Unlock()
		return
	}
	call.bodyReturned, call.returnedError = *normal, *resultErr
	call.abnormal, call.cause = !*normal, cause
	call.keeperEntered = true
	e.mu.Unlock()
	// Stay INSIDE the original outer defer: Goexit cannot finish unwinding
	// into main/os.Exit. No caretaker, callback, operation retry, cleanup or
	// fake success. The runtime timer internals remain opaque/funded; a dead
	// process or irreducible runtime failure remains UNKNOWN, never Closed.
	for {
		e.mu.Lock()
		call.waitPending = true // retained BEFORE entering the runtime wait
		if call.waitAttempts != ^uint64(0) {
			call.waitAttempts++
		}
		e.mu.Unlock()
		time.Sleep(24 * time.Hour)
		e.mu.Lock()
		call.waitPending = false
		if call.waitReturns != ^uint64(0) {
			call.waitReturns++
		}
		e.mu.Unlock()
	}
}

// RunOriginalResidentCore returns ONLY for pure arity/duplicate-entry rejection.
// Once the actual original call begins, its outermost deferred frame retains
// this SAME entry/Root and parks even on ordinary error, panic(nil) or Goexit.
// This private role does not ACK, start loops/listeners/workers or report public
// arm-start success. It must only be launched under its original funded parent.
func RunOriginalResidentCore(args []string) (resultCode int) {
	resultCode = 1
	if !originalResidentCoreArgs(args) {
		return 1
	}
	e := &processResidentCoreEntry
	call := &residentCoreEntryCall{keeperRegistered: true}
	normal := false
	var resultErr error
	// Install BEFORE the claim/initializer. recover stays directly inside the
	// deferred method, not a helper callback. Missing normal distinguishes
	// Goexit/nil-panic even when recover returns nil; component sticky state stays.
	defer e.retainOriginal(call, &normal, &resultErr)
	call.claimAttempted = true
	claimed := e.claimOriginal(call)
	e.mu.Lock()
	call.claimReturned, call.claimAccepted = true, claimed
	e.mu.Unlock()
	if !claimed {
		normal = true
		return 1
	}
	root, err := e.publishOriginalRoot(call)
	resultErr = err
	if resultErr == nil {
		resultErr = root.initializeOriginal()
		e.mu.Lock()
		call.initializationReturned = true
		e.mu.Unlock()
	}
	if resultErr == nil {
		e.mu.Lock()
		call.prepareAttempted = true
		e.mu.Unlock()
		resultErr = root.prepareOriginalBindings(context.Background())
	}
	if resultErr == nil {
		e.mu.Lock()
		call.validationAttempted = true
		e.mu.Unlock()
		resultErr = root.validateOriginalPreparedBindings()
		e.mu.Lock()
		call.validationReturned = true
		e.mu.Unlock()
	}
	if resultErr == nil {
		e.mu.Lock()
		call.rootBoundAttempted = true
		e.mu.Unlock()
		resultErr = root.commitOriginalRootBound()
		e.mu.Lock()
		call.rootBoundReturned = true
		e.mu.Unlock()
	}
	normal = true // set ONLY after the actual body returned, including errors
	return 1      // owned outer retainer never returns this as public readiness
}
