package nativeowner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	errNotOriginal   = errors.New("original resident native lifetime required")
	errRetired       = errors.New("native lifetime is retired")
	errSealed        = errors.New("native lifetime admission is sealed")
	ErrNativePending = errors.New("original native operation pending; retain and inspect")
)

type ServeIssuer struct{ owner *nativeOwner }
type nativeOwner struct {
	mu                                                   sync.Mutex // only bounded Go registry/value operations; NEVER native I/O
	original                                             *ServeIssuer
	state                                                *nativeState // exclusively accessed by the active original effect
	active                                               *nativeEffect
	openTried, openComplete, sealed, retired, retireDone bool
	generation                                           string
	failure, retireError                                 error
	published                                            NativeClosure // most recent actually published native census values
	publishedDirectory                                   nativeIdentity
	manager                                              *ManagerView
	server                                               *ServerView
	managerStop                                          *ManagerStop
	serverStop                                           *ServerStop
}
type nativeEffect struct {
	owner       *nativeOwner
	kind, phase string
	abnormal    bool
	cause       any
}
type nativeAbnormal struct{ cause any }

func (e *nativeAbnormal) Error() string {
	return "original native operation ended abnormally; custody unresolved"
}
func (e *nativeAbnormal) Unwrap() error { value, _ := e.cause.(error); return value }

type NativeClosure struct {
	Opened, Closed         uint64
	Unknown                int
	LockAcquired, Complete bool
}

func publicClosure(c nativeClosure) NativeClosure {
	return NativeClosure{c.opened, c.closed, c.unknown, c.lockAcquired, c.complete}
}
func NewServePending(path string) (*ServeIssuer, error) {
	if len(path) > maxPathBytes || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsRune(path, 0) || len(strings.Split(strings.TrimPrefix(path, "/"), "/")) > maxPathParts {
		return nil, fmt.Errorf("bounded canonical absolute existing directory required")
	}
	h := &ServeIssuer{}
	o := &nativeOwner{original: h}
	h.owner = o
	o.state = &nativeState{registry: o, path: path, uid: uint32(os.Geteuid())}
	return h, nil
}

// beginLocked records the actual operation before its effects. Ordinary pending,
// sealed/duplicate admission refusal NEVER poisons original native custody.
func (o *nativeOwner) beginLocked(kind string) (*nativeEffect, error) {
	if o.active != nil {
		return nil, ErrNativePending
	}
	if o.retired {
		return nil, errRetired
	}
	if o.sealed && kind != "retire" {
		return nil, errSealed
	}
	if o.failure != nil && kind != "retire" {
		return nil, o.failure
	}
	if kind == "open" {
		if o.openTried {
			return nil, fmt.Errorf("original open already attempted")
		}
		o.openTried = true
	} else if kind != "retire" && !o.openComplete {
		return nil, ErrNativePending
	}
	op := &nativeEffect{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

// Called only by the original effect, while mutex protects publication. Every
// native slot/object remains in state even while a syscall has not returned.
func (o *nativeOwner) publishNativeLocked() {
	n := o.state
	live := 0
	for _, f := range n.slots {
		if f != nil {
			live++
		}
	}
	complete := o.retireDone && n.retirement.complete
	o.published = NativeClosure{Opened: n.opened, Closed: n.closed, Unknown: live, LockAcquired: n.lockAcquired, Complete: complete}
	if n.directory != nil {
		o.publishedDirectory = n.directory.identity
	}
}

// Directly deferred by each actual effect body; recover is intentionally HERE.
// Normal completion clears only its exact active slot. Panic/Goexit keeps that
// same original effect unresolved and prevents ALL new native work/retirement.
func (o *nativeOwner) finishEffect(op *nativeEffect, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original native effect identity lost")
	}
	if !*normal {
		op.abnormal = true
		op.cause = cause
		if o.failure == nil {
			o.failure = &nativeAbnormal{cause: cause}
		}
		o.state.fail(o.failure)
	} else {
		if *resultErr != nil {
			o.state.fail(*resultErr)
			if o.failure == nil {
				o.failure = o.state.failure
			}
		}
		if op.kind == "open" && *resultErr == nil {
			o.openComplete = true
		}
		if op.kind == "retire" {
			o.retireDone = true
			o.retireError = *resultErr
		}
		o.active = nil
	}
	o.publishNativeLocked()
	if !*normal {
		o.published.Complete = false
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (h *ServeIssuer) OpenOriginal() (resultErr error) {
	if h == nil || h.owner == nil {
		return errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	if o.original != h {
		o.mu.Unlock()
		return errNotOriginal
	}
	op, err := o.beginLocked("open")
	o.mu.Unlock()
	if err != nil {
		return err
	}
	normal := false
	defer o.finishEffect(op, &normal, &resultErr)
	n := o.state
	n.progress("random_generation")
	var generation [16]byte
	if _, resultErr = rand.Read(generation[:]); resultErr != nil {
		normal = true
		return resultErr
	}
	o.mu.Lock()
	o.generation = hex.EncodeToString(generation[:])
	o.mu.Unlock()
	n.directory, resultErr = n.openDirectory()
	if resultErr == nil {
		resultErr = n.checkPrivate(n.directory, true)
	}
	if resultErr == nil {
		n.lock, resultErr = n.openLock(n.directory)
	}
	if resultErr == nil {
		n.progress("native_lock_pending")
		resultErr = lockOriginalNative(n)
	}
	if resultErr == nil {
		resultErr = n.validateLocked()
	}
	normal = true
	return resultErr
}
func (o *nativeOwner) validateEffect(op *nativeEffect) (result Snapshot, resultErr error) {
	normal := false
	defer o.finishEffect(op, &normal, &resultErr)
	n := o.state
	n.progress("native_validation_pending")
	resultErr = n.validateLocked()
	if resultErr == nil {
		o.mu.Lock()
		o.publishNativeLocked()
		result = o.snapshotLocked()
		o.mu.Unlock()
	}
	normal = true
	return result, resultErr
}

// Low-level FD operation: the FUTURE sole trusted-root production callsite must
// first validate actual original Manager/Server/image closures into its private
// retirement permit. A seal alone is NOT that semantic permit.
func (h *ServeIssuer) RetireOriginal() (result NativeClosure, resultErr error) {
	if h == nil || h.owner == nil {
		return NativeClosure{}, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	if o.original != h {
		o.mu.Unlock()
		return NativeClosure{}, errNotOriginal
	}
	if o.active != nil {
		o.mu.Unlock()
		return NativeClosure{}, ErrNativePending
	}
	if o.retireDone {
		result = o.published
		resultErr = o.retireError
		o.mu.Unlock()
		return result, resultErr
	}
	if !o.sealed {
		o.mu.Unlock()
		return NativeClosure{}, errSealed
	}
	op, err := o.beginLocked("retire")
	if err != nil {
		o.mu.Unlock()
		return NativeClosure{}, err
	}
	o.retired = true
	o.mu.Unlock()
	normal := false
	defer o.finishEffect(op, &normal, &resultErr)
	o.state.progress("native_retirement_pending")
	closure, err := o.state.retireLocked()
	result = publicClosure(closure)
	resultErr = err
	normal = true
	return result, resultErr
}
