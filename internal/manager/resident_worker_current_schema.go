package manager

import (
	"context"
	"reflect"
)

func sameWorkerCurrentManagerContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

// Current preworker proof owns its own fresh query observations. The original
// startup/prepared slots and validator remain unchanged. This accepts only the
// same preparing/revision0 singleton and empty controller tables; actual runtime
// controller transitions require a separately reviewed native-bound validator.
func (o *ResidentWorkerManagerPending) validateWorkerCurrentSchema(c *residentWorkerManagerCurrentCall) (resultErr error) {
	if o == nil || o.self != o || c == nil {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	if !o.currentCallLocked(c) || c.tx == nil || c.schema != nil || !sameWorkerCurrentManagerContextData(c.ctx, o.rootContext) {
		o.mu.Unlock()
		return errResidentWorkerManagerOriginal
	}
	s := &residentWorkerFreshFormatCall{owner: o, op: c.op, ctx: o.rootContext, tx: c.tx}
	c.schema = s // same actual current operation, before native or query effects
	o.mu.Unlock()
	normal := false
	defer func() {
		o.mu.Lock()
		s.normalReturned, s.resultError = normal, resultErr
		o.mu.Unlock()
	}()
	s.nativeAttempted = true
	s.native, s.nativeError = o.claim.ValidateOriginalContext(s.ctx)
	s.nativeReturned = true
	resultErr = s.nativeError
	if resultErr == nil && (s.native != o.original || s.native != c.native) {
		resultErr = errResidentWorkerManagerOriginal
	}
	if resultErr == nil {
		resultErr = readWorkerFreshWorkspace(s, s.native.Generation)
	}
	normal = true
	return resultErr
}

// Local bounded metadata under o.mu, only after the actual original helper and
// current transaction returned. Public rows/transaction closes are not native
// task or driver closure evidence and do not issue released-capacity credit.
func (o *ResidentWorkerManagerPending) completedWorkerCurrentSchemaLocked(c *residentWorkerManagerCurrentCall) bool {
	s := c.schema
	if s == nil || s.owner != o || s.op != c.op || !sameWorkerCurrentManagerContextData(s.ctx, o.rootContext) || s.tx != c.tx || !s.nativeAttempted || !s.nativeReturned || s.nativeError != nil || s.native != o.original || s.native != c.native || !s.normalReturned || s.resultError != nil {
		return false
	}
	for index, q := range s.queries {
		if q == nil || q.call != s || q.index != index || !q.queryAttempted || !q.queryReturned || q.queryError != nil || q.rows == nil || q.nextCalls != q.nextReturns || q.nextCalls > 14 || q.scanCalls < 1 || q.scanCalls > len(q.scans) || q.firstError != nil || !q.rowsErrorAttempted || !q.rowsErrorReturned || q.rowsError != nil || !q.closeAttempted || !q.closeReturned || q.closeError != nil {
			return false
		}
		for i := 0; i < q.scanCalls; i++ {
			scan := q.scans[i]
			if scan == nil || !scan.attempted || !scan.returned || scan.err != nil || len(scan.destinations) < 1 || len(scan.destinations) > 12 {
				return false
			}
		}
	}
	return true
}
