package nativeowner

import (
	"context"
	"fmt"
)

type Snapshot struct {
	Path, Generation string
	UID              uint32
	Device, Inode    uint64
}
type ManagerView struct {
	owner *nativeOwner
	claim *ManagerClaim
}
type ServerView struct {
	owner *nativeOwner
	claim *ServerClaim
}
type ManagerClaim struct {
	owner *nativeOwner
	view  *ManagerView
}
type ServerClaim struct {
	owner *nativeOwner
	view  *ServerView
}
type ManagerStop struct {
	owner *nativeOwner
	view  *ManagerView
	claim *ManagerClaim
}
type ServerStop struct {
	owner *nativeOwner
	view  *ServerView
	claim *ServerClaim
}

func (o *nativeOwner) snapshotLocked() Snapshot {
	return Snapshot{Path: o.state.path, Generation: o.generation, UID: o.state.uid, Device: o.publishedDirectory.device, Inode: o.publishedDirectory.inode}
}

func (h *ServeIssuer) ManagerView() (*ManagerView, error) {
	if h == nil || h.owner == nil {
		return nil, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	if o.original != h {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	if o.manager != nil {
		o.mu.Unlock()
		return nil, fmt.Errorf("original Manager role already issued")
	}
	op, err := o.beginLocked("validate_manager_view")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	v := &ManagerView{owner: o}
	o.manager = v
	o.mu.Unlock()
	_, err = o.validateEffect(op)
	return v, err
}
func (v *ManagerView) ValidateOriginal() (Snapshot, error) {
	if v == nil || v.owner == nil {
		return Snapshot{}, errNotOriginal
	}
	o := v.owner
	o.mu.Lock()
	if o.manager != v {
		o.mu.Unlock()
		return Snapshot{}, errNotOriginal
	}
	op, err := o.beginLocked("validate_manager_view")
	o.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	return o.validateEffect(op)
}
func (v *ManagerView) ClaimOriginal() (*ManagerClaim, error) {
	if v == nil || v.owner == nil {
		return nil, errNotOriginal
	}
	o := v.owner
	o.mu.Lock()
	if o.manager != v {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	if v.claim != nil {
		o.mu.Unlock()
		return nil, fmt.Errorf("original Manager holder already claimed")
	}
	if o.retired || o.sealed {
		o.mu.Unlock()
		return nil, errSealed
	}
	c := &ManagerClaim{owner: o, view: v}
	v.claim = c
	op, err := o.beginLocked("validate_manager_claim")
	o.mu.Unlock()
	if err != nil {
		return c, err
	}
	_, err = o.validateEffect(op)
	return c, err
}
func (c *ManagerClaim) ValidateOriginal() (Snapshot, error) {
	return c.ValidateOriginalContext(context.Background())
}

// Cancellation can end a queued wait, never an already-started native syscall.
// That original effect remains registered until actual normal return, or remains
// unresolved after panic/Goexit. No replacement effect or goroutine is spawned.
func (c *ManagerClaim) ValidateOriginalContext(ctx context.Context) (Snapshot, error) {
	if c == nil || c.owner == nil || c.view == nil {
		return Snapshot{}, errNotOriginal
	}
	return c.owner.validateBorrowerContext(ctx, c, nil)
}

func (stop *ManagerStop) ValidateOriginalClaim(c *ManagerClaim) error {
	if stop == nil || stop.owner == nil || c == nil {
		return errNotOriginal
	}
	o := stop.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.retired || !o.sealed || o.managerStop != stop || o.manager != stop.view || stop.claim != c || c.owner != o || c.view != stop.view || stop.view.claim != c {
		return errNotOriginal
	}
	return nil
}

func (h *ServeIssuer) ServerView() (*ServerView, error) {
	if h == nil || h.owner == nil {
		return nil, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	if o.original != h {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	if o.server != nil {
		o.mu.Unlock()
		return nil, fmt.Errorf("original Server role already issued")
	}
	op, err := o.beginLocked("validate_server_view")
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
	v := &ServerView{owner: o}
	o.server = v
	o.mu.Unlock()
	_, err = o.validateEffect(op)
	return v, err
}
func (v *ServerView) ValidateOriginal() (Snapshot, error) {
	if v == nil || v.owner == nil {
		return Snapshot{}, errNotOriginal
	}
	o := v.owner
	o.mu.Lock()
	if o.server != v {
		o.mu.Unlock()
		return Snapshot{}, errNotOriginal
	}
	op, err := o.beginLocked("validate_server_view")
	o.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	return o.validateEffect(op)
}
func (v *ServerView) ClaimOriginal() (*ServerClaim, error) {
	if v == nil || v.owner == nil {
		return nil, errNotOriginal
	}
	o := v.owner
	o.mu.Lock()
	if o.server != v {
		o.mu.Unlock()
		return nil, errNotOriginal
	}
	if v.claim != nil {
		o.mu.Unlock()
		return nil, fmt.Errorf("original Server holder already claimed")
	}
	if o.retired || o.sealed {
		o.mu.Unlock()
		return nil, errSealed
	}
	c := &ServerClaim{owner: o, view: v}
	v.claim = c
	op, err := o.beginLocked("validate_server_claim")
	o.mu.Unlock()
	if err != nil {
		return c, err
	}
	_, err = o.validateEffect(op)
	return c, err
}
func (c *ServerClaim) ValidateOriginal() (Snapshot, error) {
	return c.ValidateOriginalContext(context.Background())
}

// Cancellation can end a queued wait, never an already-started native syscall.
// That original effect remains registered until actual normal return, or remains
// unresolved after panic/Goexit. No replacement effect or goroutine is spawned.
func (c *ServerClaim) ValidateOriginalContext(ctx context.Context) (Snapshot, error) {
	if c == nil || c.owner == nil || c.view == nil {
		return Snapshot{}, errNotOriginal
	}
	return c.owner.validateBorrowerContext(ctx, nil, c)
}

func (stop *ServerStop) ValidateOriginalClaim(c *ServerClaim) error {
	if stop == nil || stop.owner == nil || c == nil {
		return errNotOriginal
	}
	o := stop.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.retired || !o.sealed || o.serverStop != stop || o.server != stop.view || stop.claim != c || c.owner != o || c.view != stop.view || stop.view.claim != c {
		return errNotOriginal
	}
	return nil
}

func (h *ServeIssuer) Seal() (*ManagerStop, *ServerStop, error) {
	if h == nil || h.owner == nil {
		return nil, nil, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.original != h || o.retired {
		return nil, nil, errNotOriginal
	}
	if !o.sealed {
		o.sealed = true
		if o.manager != nil {
			o.managerStop = &ManagerStop{owner: o, view: o.manager, claim: o.manager.claim}
		}
		if o.server != nil {
			o.serverStop = &ServerStop{owner: o, view: o.server, claim: o.server.claim}
		}
	}
	return o.managerStop, o.serverStop, nil
}
func (h *ServeIssuer) ValidateOriginalViews(manager *ManagerView, server *ServerView) (Snapshot, error) {
	if h == nil || h.owner == nil {
		return Snapshot{}, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	if o.original != h || manager == nil || server == nil || o.manager != manager || o.server != server {
		o.mu.Unlock()
		return Snapshot{}, errNotOriginal
	}
	op, err := o.beginLocked("validate_bound_views")
	o.mu.Unlock()
	if err != nil {
		return Snapshot{}, err
	}
	return o.validateEffect(op)
}

type Inspection struct {
	Generation                                                 string
	OpenAttempted, OpenComplete, Sealed, Retired               bool
	ManagerIssued, ManagerClaimed, ServerIssued, ServerClaimed bool
	Opened, Closed                                             uint64
	ValidationWaiters                                          int
	LiveDescriptors                                            int // last published census; NEVER exhaustive while an effect is pending
	FirstFailure                                               error
	OriginalRetirement                                         NativeClosure
	OperationPending, OperationAbnormal                        bool
	OperationKind, OperationPhase                              string
	NativeCensusUncertain                                      bool
}

func (h *ServeIssuer) InspectOriginal() (Inspection, error) {
	if h == nil || h.owner == nil {
		return Inspection{}, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.original != h {
		return Inspection{}, errNotOriginal
	}
	result := Inspection{ValidationWaiters: len(o.validationWaiters), Generation: o.generation, OpenAttempted: o.openTried, OpenComplete: o.openComplete, Sealed: o.sealed, Retired: o.retired, ManagerIssued: o.manager != nil, ManagerClaimed: o.manager != nil && o.manager.claim != nil, ServerIssued: o.server != nil, ServerClaimed: o.server != nil && o.server.claim != nil, Opened: o.published.Opened, Closed: o.published.Closed, LiveDescriptors: o.published.Unknown, FirstFailure: o.failure, OriginalRetirement: o.published}
	// An active original may be inside the kernel BEFORE its handle/result can
	// be registered. Published counters are observations, never zero-effect proof.
	if o.active != nil {
		result.OperationPending = true
		result.OperationAbnormal = o.active.abnormal
		result.OperationKind = o.active.kind
		result.OperationPhase = o.active.phase
		result.NativeCensusUncertain = true
		result.OriginalRetirement.Complete = false
	}
	return result, nil
}
