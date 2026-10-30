package database

import (
	"context"
	"database/sql/driver"
	"fmt"
)

// residentConnector wraps only the public Connector. The returned original
// driver.Conn is NEVER wrapped: every optional interface, concrete Raw method,
// Backup/Error type and registered Driver remains the actual official driver's.
// No private driver/TLS/statement/row/watcher or native pointer is inspected.
type residentConnector struct {
	owner    *ResidentOpen
	original driver.Connector
}

type residentConnectCall struct {
	owner              *ResidentOpen
	connector          *residentConnector
	finished, abnormal bool
	cause              any
	returned           driver.Conn
	resultErr          error
}

type ResidentConnectorInspection struct {
	OriginalConnectorRetained, WrapperRetained        bool
	ConnectAttempted, ConnectPending, ConnectAbnormal bool
	ReturnedConnectionRetained, AdmissionSealed       bool
	AdmissionRefusals                                 uint64
	FirstConnectError, FirstAdmissionRefusal          error
	PrivateNativeStateOpaque                          bool // always true; including error or normal return
}

var ErrResidentConnectionAttempted = fmt.Errorf("original resident physical connection already attempted; no automatic replacement is admitted")
var errResidentConnectionSealed = fmt.Errorf("original resident physical connection admission is sealed")

func (c *residentConnector) Driver() driver.Driver {
	// The pinned official connector returns the same process-registered driver;
	// no new sql.Register, shadow driver or replacement global identity is used.
	return c.original.Driver()
}

// The owner deliberately permits only ONE physical Connect attempt in total.
// max-open1/idle1/zero expiry alone would allow database/sql's ErrBadConn retry
// or discard/reconnect. That later attempt is refused, not silently re-owned.
// This is the explicit resident policy, not a claim of transparent reconnect.
func (c *residentConnector) Connect(ctx context.Context) (returned driver.Conn, resultErr error) {
	if c == nil || c.owner == nil {
		return nil, errResidentDatabaseOriginal
	}
	o := c.owner
	o.mu.Lock()
	if o.self != o || o.connector != c || o.pool == nil || c.original == nil {
		o.mu.Unlock()
		return nil, errResidentDatabaseOriginal
	}
	var refused error
	if o.connectSealed {
		refused = errResidentConnectionSealed
	} else if o.connect != nil {
		refused = ErrResidentConnectionAttempted
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
	call := &residentConnectCall{owner: o, connector: c}
	o.connect = call // original wrapper call registered BEFORE official/native Connect
	o.mu.Unlock()
	normal := false
	defer o.finishOriginalConnect(call, &normal, &returned, &resultErr)
	returned, resultErr = c.original.Connect(ctx)
	normal = true
	return returned, resultErr
}

// Directly deferred by the actual public Connect wrapper. Native failures before
// the official method returns any Conn remain OPAQUE. An error/nil return never
// proves no allocation or cleanup. A nonnil result is kept even alongside error.
func (o *ResidentOpen) finishOriginalConnect(call *residentConnectCall, normal *bool, returned *driver.Conn, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.connect != call || call.owner != o || call.connector != o.connector {
		o.mu.Unlock()
		panic("original public connector operation identity lost")
	}
	call.returned = *returned
	if !*normal {
		call.abnormal, call.cause = true, cause
		call.resultErr = &residentSQLAbnormal{cause: cause}
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
func (o *ResidentOpen) inspectConnectorLocked() ResidentConnectorInspection {
	v := ResidentConnectorInspection{OriginalConnectorRetained: o.standardConnector != nil, WrapperRetained: o.connector != nil, AdmissionSealed: o.connectSealed, AdmissionRefusals: o.connectRefusals, FirstAdmissionRefusal: o.firstConnectRefusal, PrivateNativeStateOpaque: true}
	if call := o.connect; call != nil {
		v.ConnectAttempted = true
		v.ConnectPending = !call.finished
		v.ConnectAbnormal = call.abnormal
		v.ReturnedConnectionRetained = call.returned != nil
		v.FirstConnectError = call.resultErr
	}
	return v
}

func (o *ResidentOpen) closeObservationCopy() ResidentSQLCloseObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.closeObservation
	v.NativeOutcome = "unknown"
	return v
}
