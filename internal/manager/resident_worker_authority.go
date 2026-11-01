package manager

import (
	"context"
	"reflect"
)

// Only this exact original started Manager can issue its controller authority.
// No serialized row, generation, view observation or caller success bit can.
type ResidentWorkerAuthority struct {
	self           *ResidentWorkerAuthority
	owner          *ResidentWorkerManagerPending
	manager        *Manager
	op             *residentWorkerManagerOperation
	normalReturned bool
	resultError    error
}

func (o *ResidentWorkerManagerPending) ClaimOriginalWorkerAuthority() (authority *ResidentWorkerAuthority, resultErr error) {
	op, err := o.beginOriginal("worker_authority")
	if err != nil {
		return nil, err
	}
	a := &ResidentWorkerAuthority{owner: o, manager: o.manager, op: op}
	a.self = a
	o.mu.Lock()
	o.authority = a
	o.mu.Unlock()
	normal := false
	defer func() {
		o.mu.Lock()
		a.normalReturned = normal && op.normalFinished && !op.abnormal
		a.resultError = resultErr
		o.mu.Unlock()
	}()
	defer o.finishOriginal(op, &normal, &resultErr)
	if a.manager == nil || a.manager.residentWorker != o || a.manager.resident != nil || a.manager.residentService != nil || o.claim == nil || o.rootContext == nil {
		resultErr = errResidentWorkerManagerOriginal
	} else {
		snapshot, err := o.claim.ValidateOriginalContext(o.rootContext)
		resultErr = err
		if resultErr == nil && snapshot != o.original {
			resultErr = errResidentWorkerManagerOriginal
		}
	}
	normal = true
	return a, resultErr
}

// Bounded original lifetime check for the trusted core Server constructor.
// This returns neither a context nor its cancellation function.
func (o *ResidentWorkerManagerPending) CheckOriginalRootContext(ctx context.Context) error {
	if o == nil || o.self != o || ctx == nil {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	original := o.rootContext
	charge := o.charge
	valid := o.active == nil && o.firstError == nil && o.ready && o.manager != nil && o.manager.residentWorker == o && o.manager.resident == nil && o.manager.residentService == nil && charge != nil && original != nil
	o.mu.Unlock()
	if !valid || !reflect.ValueOf(ctx).Comparable() || !reflect.ValueOf(original).Comparable() || ctx != original {
		return errResidentWorkerManagerOriginal
	}
	return charge.RequireOriginalRootContext(ctx)
}
