package nativeowner

import (
	"context"
	"errors"
)

// The actual request retains its waiter on this synchronous stack, while the
// issuer bounds queued callers. This is not a native operation or service grant.
// No helper goroutine, polling, FD alias or private driver state is involved.
type nativeValidationWait struct{ original byte } // nonzero size: distinct waiter pointers must stay distinct

const maxNativeValidationWaiters = 64

var ErrNativeValidationBusy = errors.New("original native validation waiter capacity reached")

func (o *nativeOwner) validateBorrowerContext(ctx context.Context, manager *ManagerClaim, server *ServerClaim) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, errNotOriginal
	}
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
		validManager := manager != nil && server == nil && manager.owner == o && manager.view != nil && o.manager == manager.view && manager.view.claim == manager
		validServer := server != nil && manager == nil && server.owner == o && server.view != nil && o.server == server.view && server.view.claim == server
		if !validManager && !validServer {
			o.mu.Unlock()
			return Snapshot{}, errNotOriginal
		}
		if err := ctx.Err(); err != nil {
			o.mu.Unlock()
			return Snapshot{}, err
		}
		// A pending ORIGINAL effect retains custody. Its notification closes only
		// after actual normal return/publication. An abnormal syscall path keeps
		// it unresolved; wait cancellation does not interrupt or replace it.
		if active := o.active; active != nil {
			if !queued {
				if len(o.validationWaiters) >= maxNativeValidationWaiters {
					o.mu.Unlock()
					return Snapshot{}, ErrNativeValidationBusy
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
				return Snapshot{}, ctx.Err()
			case <-done:
				// This wakes a waiter, not a capability. Recheck original identity,
				// context and admission and perform THIS caller's fresh validation.
				continue
			}
		}
		kind := "validate_server_claim"
		if validManager {
			kind = "validate_manager_claim"
		}
		op, err := o.beginLocked(kind)
		if queued {
			delete(o.validationWaiters, waiter)
			queued = false
		}
		o.mu.Unlock()
		if err != nil {
			return Snapshot{}, err
		}
		// Native I/O stays outside the observation mutex. Once begun, a context
		// cannot cancel a kernel syscall; the same effect remains registered.
		value, err := o.validateEffect(op)
		if err != nil {
			return Snapshot{}, err
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		return value, nil
	}
}
