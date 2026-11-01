package database

import (
	"context"
	"database/sql/driver"
	"fmt"
	"getarm.org/internal/server/lifetimeview"
)

// workerResidentConnector wraps only the public Connector. The returned original
// driver.Conn is NEVER wrapped: every optional interface, concrete Raw method,
// Backup/Error type and registered Driver remains the actual official driver's.
// No private driver/TLS/statement/row/watcher or native pointer is inspected.
type workerResidentConnector struct {
	owner         *WorkerResidentOpen
	original      driver.Connector
	originalClaim *lifetimeview.WorkerManagerClaim // immutable association captured at wrapper construction
}

type workerResidentConnectCall struct {
	owner              *WorkerResidentOpen
	connector          *workerResidentConnector
	finished, abnormal bool
	cause              any
	returned           driver.Conn
	resultErr          error
}

type WorkerResidentConnectorInspection struct {
	OriginalConnectorRetained, WrapperRetained        bool
	ConnectAttempted, ConnectPending, ConnectAbnormal bool
	ReturnedConnectionRetained, AdmissionSealed       bool
	AdmissionRefusals                                 uint64
	FirstConnectError, FirstAdmissionRefusal          error
	PrivateNativeStateOpaque                          bool // always true; including error or normal return
}

var ErrWorkerResidentConnectionAttempted = fmt.Errorf("original resident physical connection already attempted; no automatic replacement is admitted")
var errWorkerResidentConnectionSealed = fmt.Errorf("original resident physical connection admission is sealed")

func (c *workerResidentConnector) Driver() driver.Driver {
	// The pinned official connector returns the same process-registered driver;
	// no new sql.Register, shadow driver or replacement global identity is used.
	return c.original.Driver()
}

// The owner deliberately permits only ONE physical Connect attempt in total.
// max-open1/idle1/zero expiry alone would allow database/sql's ErrBadConn retry
// or discard/reconnect. That later attempt is refused, not silently re-owned.
// This is the explicit resident policy, not a claim of transparent reconnect.
func (c *workerResidentConnector) Connect(ctx context.Context) (returned driver.Conn, resultErr error) {
	if c == nil || c.owner == nil || c.originalClaim == nil {
		return nil, errWorkerResidentDatabaseOriginal
	}
	o := c.owner
	o.mu.Lock()
	if o.self != o || o.connector != c || o.pool == nil || c.original == nil || o.claim != c.originalClaim {
		o.mu.Unlock()
		return nil, errWorkerResidentDatabaseOriginal
	}
	var refused error
	if o.connectSealed {
		refused = errWorkerResidentConnectionSealed
	} else if o.connect != nil {
		refused = ErrWorkerResidentConnectionAttempted
	}
	if refused != nil {
		if o.connectRefusals != ^uint64(0) {
			o.connectRefusals++
		}
		if o.firstConnectRefusal == nil {
			o.firstConnectRefusal = refused
		}
		o.mu.Unlock()
		return nil, refused
	}
	call := &workerResidentConnectCall{owner: o, connector: c}
	o.connect = call // original wrapper call registered BEFORE official/native Connect
	o.mu.Unlock()
	normal := false
	defer o.finishOriginalConnect(call, &normal, &returned, &resultErr)
	// Only the exact worker-native original claim reaches physical entry.
	// No legacy fixture/claim/marker branch can bypass this native validation.
	snapshot, err := c.originalClaim.ValidateOriginalDatabaseFence(ctx)
	resultErr = err
	if resultErr == nil {
		resultErr = o.matchOriginalPreparedFiles(ctx, snapshot)
	}
	if resultErr == nil {
		returned, resultErr = c.original.Connect(ctx)
	}
	normal = true
	return returned, resultErr
}

// Directly deferred by the actual public Connect wrapper. Native failures before
// the official method returns any Conn remain OPAQUE. An error/nil return never
// proves no allocation or cleanup. A nonnil result is kept even alongside error.
func (o *WorkerResidentOpen) finishOriginalConnect(call *workerResidentConnectCall, normal *bool, returned *driver.Conn, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.connect != call || call.owner != o || call.connector != o.connector {
		o.mu.Unlock()
		panic("original public connector operation identity lost")
	}
	call.returned = *returned
	if !*normal {
		call.abnormal, call.cause = true, cause
		call.resultErr = &workerResidentSQLAbnormal{cause: cause}
	} else {
		call.finished = true
		call.resultErr = *resultErr
	}
	o.closeObservation.OriginalConnectPending = !call.finished
	if o.firstError == nil && call.resultErr != nil {
		o.firstError = call.resultErr
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

// Called only with the owner observation mutex held; it invokes no driver API.
func (o *WorkerResidentOpen) inspectConnectorLocked() WorkerResidentConnectorInspection {
	v := WorkerResidentConnectorInspection{OriginalConnectorRetained: o.standardConnector != nil, WrapperRetained: o.connector != nil, AdmissionSealed: o.connectSealed, AdmissionRefusals: o.connectRefusals, FirstAdmissionRefusal: o.firstConnectRefusal, PrivateNativeStateOpaque: true}
	if call := o.connect; call != nil {
		v.ConnectAttempted = true
		v.ConnectPending = !call.finished
		v.ConnectAbnormal = call.abnormal
		v.ReturnedConnectionRetained = call.returned != nil
		v.FirstConnectError = call.resultErr
	}
	return v
}

func (o *WorkerResidentOpen) closeObservationCopy() WorkerResidentSQLCloseObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.closeObservation
	v.NativeOutcome = "unknown"
	return v
}
