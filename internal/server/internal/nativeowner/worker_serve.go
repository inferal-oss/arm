package nativeowner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

// WorkerServeIssuer never constructs, returns or stores an old ServeIssuer,
// old Manager/Server view or old service charge. raw holds concrete native
// effect/descriptor bookkeeping only, with every legacy issuer field zero.
type WorkerServeIssuer struct {
	funding        *residentservice.WorkerStartupFundingView
	fundingBinding *residentservice.WorkerServeFundingBinding
	self           *WorkerServeIssuer
	raw            *nativeOwner
	fence          *residentbirth.WorkerInheritedFencePending
	birth          *residentservice.WorkerServeBirthView
	expected       residentbirth.WorkerFenceData
	bound          residentservice.WorkerBirthSelectorsData
	manager        *WorkerManagerView
	server         *WorkerServerView
}

type WorkerManagerView struct {
	self  *WorkerManagerView
	owner *WorkerServeIssuer
	claim *WorkerManagerClaim
}
type WorkerServerView struct {
	self  *WorkerServerView
	owner *WorkerServeIssuer
	claim *WorkerServerClaim
}
type WorkerManagerClaim struct {
	self  *WorkerManagerClaim
	owner *WorkerServeIssuer
	view  *WorkerManagerView
}
type WorkerServerClaim struct {
	self  *WorkerServerClaim
	owner *WorkerServeIssuer
	view  *WorkerServerView
}
type WorkerSnapshot struct {
	Path, Generation string
	UID              uint32
	Device, Inode    uint64
	Fence            residentrefs.FenceIdentities
	Birth            residentservice.WorkerBirthSelectorsData
}

// Pure allocation only. Root retains destination before any later native
// validation, UID/entropy observation, open or lock. Zero/foreign inputs cannot
// pass OpenOriginal. Root must fund its complete composed ledger before Open.
func NewInheritedWorkerServePending(destination **WorkerServeIssuer, fence *residentbirth.WorkerInheritedFencePending, birth *residentservice.WorkerServeBirthView, funding *residentservice.WorkerStartupFundingView) error {
	if destination == nil || *destination != nil || fence == nil || birth == nil || funding == nil {
		return errNotOriginal
	}
	p := &WorkerServeIssuer{fence: fence, birth: birth, funding: funding}
	p.self = p
	*destination = p
	p.raw = &nativeOwner{}
	p.raw.state = &nativeState{registry: p.raw}
	return nil
}

// Caller holds raw.mu. A finite type walk refuses future authority fields too.
func (p *WorkerServeIssuer) originalLocked() bool {
	if p == nil || p.self != p || p.raw == nil || p.fence == nil || p.birth == nil || p.funding == nil || p.raw.state == nil || p.raw.state.registry != p.raw {
		return false
	}
	if active := p.raw.active; active != nil && (active.owner != p.raw || !workerNativeEffectKindData(active.kind)) {
		return false
	}
	value := reflect.ValueOf(p.raw).Elem()
	for i := 0; i < value.NumField(); i++ {
		switch value.Type().Field(i).Name {
		case "mu", "state", "active", "validationWaiters", "openTried", "openComplete", "generation", "failure", "published", "publishedDirectory":
		default:
			if !value.Field(i).IsZero() {
				return false
			}
		}
	}
	return true
}

func workerNativeEffectKindData(kind string) bool {
	switch kind {
	case "open", "worker_manager_view", "worker_server_view", "worker_manager_claim", "worker_server_claim", "worker_server_current", "worker_manager_current", "worker_database_before_connect", "worker_bound_views_current":
		return true
	default:
		return false
	}
}

func boundedWorkerNativePathData(path string) bool {
	return path != "/" && len(path) <= maxPathBytes && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsRune(path, 0) && len(strings.Split(strings.TrimPrefix(path, "/"), "/")) <= maxPathParts
}

func (p *WorkerServeIssuer) OpenOriginal() (resultErr error) {
	if p == nil || p.self != p || p.raw == nil {
		return errNotOriginal
	}
	o := p.raw
	o.mu.Lock()
	if !p.originalLocked() {
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
	resultErr = p.openBodyOriginal()
	normal = true
	return resultErr
}

func (p *WorkerServeIssuer) openBodyOriginal() error {
	if err := p.funding.ClaimOriginalServeBinding(&p.fundingBinding, p.birth); err != nil {
		return err
	}
	if err := p.fundingBinding.RequireOriginalBirth(p.birth); err != nil {
		return err
	}
	o := p.raw
	n := o.state
	data, err := p.fence.NativeSelectorsOriginal()
	if err != nil || !boundedWorkerNativePathData(data.Home) {
		return errNotOriginal
	}
	// Actual concrete service/fence link and native/runtime/environment checks;
	// a saved selector or a caller callback cannot stand in for these owners.
	bound, err := p.fence.ValidateOriginalBirth(p.birth)
	if err != nil {
		return err
	}
	if bound.Home != data.Home || bound.IntentSHA256 != data.IntentSHA256 {
		return errNotOriginal
	}
	o.mu.Lock()
	p.expected, p.bound = data, bound
	o.mu.Unlock()
	n.path = data.Home
	n.progress("worker_native_original_UID_pending")
	n.uid = uint32(os.Geteuid())
	n.progress("worker_native_original_generation_pending")
	var generation [16]byte
	if _, err = rand.Read(generation[:]); err != nil {
		return err
	}
	o.mu.Lock()
	o.generation = hex.EncodeToString(generation[:])
	o.mu.Unlock()
	n.directory, err = n.openDirectory()
	if err == nil {
		err = n.checkPrivate(n.directory, true)
	}
	if err == nil {
		err = p.matchOriginalBody(false)
	}
	if err == nil {
		n.lock, err = n.openLock(n.directory)
	}
	if err == nil {
		err = p.matchOriginalBody(true)
	}
	if err == nil {
		err = lockOriginalNative(n)
	}
	if err == nil {
		err = n.validateLocked()
	}
	return err
}

// Private bool selects the already registered concrete open step. It is never
// caller-supplied authority or a conditional skip of original fence validation.
func (p *WorkerServeIssuer) matchOriginalBody(requireLock bool) error {
	if p.fundingBinding == nil {
		return errNotOriginal
	}
	if err := p.fundingBinding.RequireOriginalBirth(p.birth); err != nil {
		return err
	}
	home, ids, err := p.fence.ValidateForOriginalNative()
	if err != nil {
		return err
	}
	n := p.raw.state
	if home != p.expected.Home || home != n.path || ids != p.expected.Identities || n.directory == nil || !sameInheritedIdentity(n.directory.identity, ids.Home) {
		return errNotOriginal
	}
	if requireLock && (n.lock == nil || !sameInheritedIdentity(n.lock.identity, ids.Daemon)) {
		return errNotOriginal
	}
	bound, err := p.fence.ValidateOriginalBirth(p.birth)
	if err != nil {
		return err
	}
	if bound != p.bound {
		return errNotOriginal
	}
	return nil
}

func (p *WorkerServeIssuer) snapshotLocked() WorkerSnapshot {
	o := p.raw
	return WorkerSnapshot{Path: o.state.path, Generation: o.generation, UID: o.state.uid, Device: o.publishedDirectory.device, Inode: o.publishedDirectory.inode, Fence: p.expected.Identities, Birth: p.bound}
}

func (p *WorkerServeIssuer) validateEffectOriginal(op *nativeEffect, database bool) (snapshot WorkerSnapshot, resultErr error) {
	o := p.raw
	normal := false
	defer o.finishEffect(op, &normal, &resultErr)
	o.state.progress("worker_native_current_validation_pending")
	// Refuse unissued worker metadata before even low-level native validation.
	resultErr = p.matchOriginalBody(true)
	if resultErr == nil {
		resultErr = o.state.validateLocked()
	}
	if resultErr == nil {
		resultErr = p.matchOriginalBody(true)
	}
	if resultErr == nil && database {
		resultErr = p.fence.ValidateForOriginalDatabase()
	}
	if resultErr == nil {
		o.mu.Lock()
		o.publishNativeLocked()
		snapshot = p.snapshotLocked()
		o.mu.Unlock()
	}
	normal = true
	return snapshot, resultErr
}

func (p *WorkerServeIssuer) ManagerView() (*WorkerManagerView, error) {
	if p == nil || p.self != p || p.raw == nil {
		return nil, errNotOriginal
	}
	o := p.raw
	o.mu.Lock()
	if !p.originalLocked() || p.manager != nil {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	op, err := o.beginLocked("worker_manager_view")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	v := &WorkerManagerView{owner: p}
	v.self = v
	p.manager = v
	o.mu.Unlock()
	_, err = p.validateEffectOriginal(op, false)
	return v, err
}

func (p *WorkerServeIssuer) ServerView() (*WorkerServerView, error) {
	if p == nil || p.self != p || p.raw == nil {
		return nil, errNotOriginal
	}
	o := p.raw
	o.mu.Lock()
	if !p.originalLocked() || p.server != nil {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	op, err := o.beginLocked("worker_server_view")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	v := &WorkerServerView{owner: p}
	v.self = v
	p.server = v
	o.mu.Unlock()
	_, err = p.validateEffectOriginal(op, false)
	return v, err
}

func (v *WorkerManagerView) ClaimOriginal() (*WorkerManagerClaim, error) {
	if v == nil || v.self != v || v.owner == nil || v.owner.raw == nil {
		return nil, errNotOriginal
	}
	p, o := v.owner, v.owner.raw
	o.mu.Lock()
	if !p.originalLocked() || p.manager != v || v.claim != nil {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	op, err := o.beginLocked("worker_manager_claim")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	c := &WorkerManagerClaim{owner: p, view: v}
	c.self = c
	v.claim = c
	o.mu.Unlock()
	_, err = p.validateEffectOriginal(op, false)
	return c, err
}

func (v *WorkerServerView) ClaimOriginal() (*WorkerServerClaim, error) {
	if v == nil || v.self != v || v.owner == nil || v.owner.raw == nil {
		return nil, errNotOriginal
	}
	p, o := v.owner, v.owner.raw
	o.mu.Lock()
	if !p.originalLocked() || p.server != v || v.claim != nil {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	op, err := o.beginLocked("worker_server_claim")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	c := &WorkerServerClaim{owner: p, view: v}
	c.self = c
	v.claim = c
	o.mu.Unlock()
	_, err = p.validateEffectOriginal(op, false)
	return c, err
}

// Queue cancellation changes this caller's admission only. Once a concrete
// native validation starts, its same original effect runs under the resident
// fence/birth lifetime. Unknown native effects never permit a replacement.
func (p *WorkerServeIssuer) validateClaimOriginal(ctx context.Context, manager *WorkerManagerClaim, server *WorkerServerClaim, database bool) (WorkerSnapshot, error) {
	if p == nil || p.self != p || p.raw == nil || ctx == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	o := p.raw
	waiter := &nativeValidationWait{}
	queued := false
	defer func() {
		if queued {
			o.mu.Lock()
			delete(o.validationWaiters, waiter)
			o.mu.Unlock()
		}
	}()
	for {
		o.mu.Lock()
		validManager := manager != nil && manager.self == manager && server == nil && manager.owner == p && manager.view != nil && manager.view.self == manager.view && manager.view.owner == p && p.manager == manager.view && manager.view.claim == manager
		validServer := server != nil && server.self == server && manager == nil && server.owner == p && server.view != nil && server.view.self == server.view && server.view.owner == p && p.server == server.view && server.view.claim == server
		if !p.originalLocked() || (!validManager && !validServer) || database && !validManager {
			o.mu.Unlock()
			return WorkerSnapshot{}, errNotOriginal
		}
		if err := ctx.Err(); err != nil {
			o.mu.Unlock()
			return WorkerSnapshot{}, err
		}
		if active := o.active; active != nil {
			if !queued {
				if len(o.validationWaiters) >= maxNativeValidationWaiters {
					o.mu.Unlock()
					return WorkerSnapshot{}, ErrNativeValidationBusy
				}
				if o.validationWaiters == nil {
					o.validationWaiters = make(map[*nativeValidationWait]struct{})
				}
				o.validationWaiters[waiter] = struct{}{}
				queued = true
			}
			done := active.done
			o.mu.Unlock()
			select {
			case <-ctx.Done():
				return WorkerSnapshot{}, ctx.Err()
			case <-done:
				continue
			}
		}
		kind := "worker_server_current"
		if validManager {
			kind = "worker_manager_current"
		}
		if database {
			kind = "worker_database_before_connect"
		}
		op, err := o.beginLocked(kind)
		if queued {
			delete(o.validationWaiters, waiter)
			queued = false
		}
		o.mu.Unlock()
		if err != nil {
			return WorkerSnapshot{}, err
		}
		data, err := p.validateEffectOriginal(op, database)
		if err != nil {
			return WorkerSnapshot{}, err
		}
		if err := ctx.Err(); err != nil {
			return WorkerSnapshot{}, err
		}
		return data, nil
	}
}

func (c *WorkerManagerClaim) ValidateOriginalContext(ctx context.Context) (WorkerSnapshot, error) {
	if c == nil || c.self != c || c.owner == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	return c.owner.validateClaimOriginal(ctx, c, nil, false)
}
func (c *WorkerServerClaim) ValidateOriginalContext(ctx context.Context) (WorkerSnapshot, error) {
	if c == nil || c.self != c || c.owner == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	return c.owner.validateClaimOriginal(ctx, nil, c, false)
}
func (c *WorkerManagerClaim) ValidateOriginalDatabaseFence(ctx context.Context) (WorkerSnapshot, error) {
	if c == nil || c.self != c || c.owner == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	return c.owner.validateClaimOriginal(ctx, c, nil, true)
}

func (p *WorkerServeIssuer) validateViewOriginal(manager *WorkerManagerView, server *WorkerServerView) (WorkerSnapshot, error) {
	if p == nil || p.self != p || p.raw == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	o := p.raw
	o.mu.Lock()
	validManager := manager != nil && manager.self == manager && manager.owner == p && p.manager == manager
	validServer := server != nil && server.self == server && server.owner == p && p.server == server
	if !p.originalLocked() || manager != nil && !validManager || server != nil && !validServer || manager == nil && server == nil {
		o.mu.Unlock()
		return WorkerSnapshot{}, errNotOriginal
	}
	op, err := o.beginLocked("worker_bound_views_current")
	o.mu.Unlock()
	if err != nil {
		return WorkerSnapshot{}, err
	}
	return p.validateEffectOriginal(op, false)
}

func (v *WorkerManagerView) ValidateOriginal() (WorkerSnapshot, error) {
	if v == nil || v.self != v || v.owner == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	return v.owner.validateViewOriginal(v, nil)
}
func (v *WorkerServerView) ValidateOriginal() (WorkerSnapshot, error) {
	if v == nil || v.self != v || v.owner == nil {
		return WorkerSnapshot{}, errNotOriginal
	}
	return v.owner.validateViewOriginal(nil, v)
}
func (p *WorkerServeIssuer) ValidateOriginalViews(manager *WorkerManagerView, server *WorkerServerView, fence *residentbirth.WorkerInheritedFencePending) (WorkerSnapshot, error) {
	if p == nil || p.self != p || manager == nil || server == nil || fence == nil || p.fence != fence {
		return WorkerSnapshot{}, errNotOriginal
	}
	return p.validateViewOriginal(manager, server)
}

type WorkerInspection struct {
	OpenAttempted, OpenComplete               bool
	ManagerIssued, ManagerClaimed             bool
	ServerIssued, ServerClaimed               bool
	OperationPending, OperationAbnormal       bool
	NativeCensusUncertain                     bool
	Opened, Closed                            uint64
	LiveDescriptors, ValidationWaiters        int
	Generation, OperationKind, OperationPhase string
	FirstError                                error
	NativeOutcome                             string // always unknown, never closure or credit
}

func (p *WorkerServeIssuer) InspectOriginal() (WorkerInspection, error) {
	if p == nil || p.self != p || p.raw == nil {
		return WorkerInspection{}, errNotOriginal
	}
	o := p.raw
	o.mu.Lock()
	defer o.mu.Unlock()
	if !p.originalLocked() {
		return WorkerInspection{}, errNotOriginal
	}
	v := WorkerInspection{OpenAttempted: o.openTried, OpenComplete: o.openComplete, ManagerIssued: p.manager != nil, ManagerClaimed: p.manager != nil && p.manager.claim != nil, ServerIssued: p.server != nil, ServerClaimed: p.server != nil && p.server.claim != nil, Opened: o.published.Opened, Closed: o.published.Closed, LiveDescriptors: o.published.Unknown, ValidationWaiters: len(o.validationWaiters), Generation: o.generation, FirstError: o.failure, NativeOutcome: "unknown"}
	if o.active != nil {
		v.OperationPending, v.OperationAbnormal, v.NativeCensusUncertain = true, o.active.abnormal, true
		v.OperationKind, v.OperationPhase = o.active.kind, o.active.phase
	}
	return v, nil
}
