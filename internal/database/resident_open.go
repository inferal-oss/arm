package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"getarm.org/internal/server/lifetimeview"
	sqlite "modernc.org/sqlite"
)

// ResidentOpen is the actual database startup owner. The Manager pending holder
// must retain it BEFORE OpenOriginal. It owns its actual official driver
// Connector, wrapper-visible original Connect,
// pool, file registry and original operation, including partial/unknown outcomes.
// No function here constructs a service grant or adopts an existing Manager.
type ResidentOpen struct {
	mu                    sync.Mutex
	self                  *ResidentOpen
	claim                 *lifetimeview.ManagerClaim
	files                 *residentSQLFiles
	filename              string
	active                *residentSQLOperation
	openTried, closeTried bool
	standardConnector     driver.Connector
	connector             *residentConnector
	connect               *residentConnectCall
	connectSealed         bool
	connectRefusals       uint64
	firstConnectRefusal   error
	pool                  *sql.DB
	binding               *ResidentDatabaseBinding
	firstError            error
	phase                 string
	fileObservation       residentSQLFileObservation
	closeObservation      ResidentSQLCloseObservation
	closeError            error
}
type residentSQLOperation struct {
	owner       *ResidentOpen
	kind, phase string
	abnormal    bool
	cause       any
}
type ResidentDatabaseBinding struct {
	owner      *ResidentOpen
	generation string
	pool       *sql.DB
}
type residentSQLFileObservation struct {
	opened, closed uint64
	live           int
}
type ResidentSQLCloseObservation struct {
	ConnectorSealed, OriginalConnectPending bool
	PoolCloseAttempted, PoolCloseReturned   bool
	NativeOutcome                           string // ALWAYS unknown; pool return is not task/native Closed
}

type ResidentDatabaseInspection struct {
	Phase                                               string
	OriginalOperationPending, OriginalOperationAbnormal bool
	OriginalOperationKind, OriginalOperationPhase       string
	ConnectorRetained, PoolRetained, BindingRetained    bool
	FilesOpened, FilesClosed                            uint64
	LastPublishedLiveFiles                              int
	NativeCensusUncertain                               bool
	FirstError                                          error
	Connector                                           ResidentConnectorInspection
	Close                                               ResidentSQLCloseObservation
}

var ErrResidentDatabasePending = errors.New("original resident database operation pending; retain and inspect")
var errResidentDatabaseOriginal = errors.New("original resident database holder/binding required")

type residentSQLAbnormal struct{ cause any }

func (e *residentSQLAbnormal) Error() string {
	return "original resident database operation ended abnormally"
}
func (e *residentSQLAbnormal) Unwrap() error { value, _ := e.cause.(error); return value }

// Pure construction. This does not validate/reassert a lock, open SQLite, start
// a pool/task, or interpret metadata as native/service custody. The concrete
// Manager constructor owns the single original claim and this pending slot.
func NewResidentOpen(claim *lifetimeview.ManagerClaim) (*ResidentOpen, error) {
	if claim == nil {
		return nil, errResidentDatabaseOriginal
	}
	o := &ResidentOpen{claim: claim, phase: "pending"}
	o.self = o
	o.files = &residentSQLFiles{owner: o}
	o.closeObservation.NativeOutcome = "unknown"
	return o, nil
}
func (o *ResidentOpen) beginOriginal(kind string) (*residentSQLOperation, error) {
	if o == nil || o.self != o {
		return nil, errResidentDatabaseOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrResidentDatabasePending
	}
	if kind == "open" {
		if o.openTried {
			if o.firstError != nil {
				return nil, o.firstError
			}
			return nil, fmt.Errorf("original database open already attempted")
		}
		o.openTried = true
	} else {
		if o.closeTried {
			return nil, fmt.Errorf("original database close already attempted")
		}
		o.closeTried = true
	}
	op := &residentSQLOperation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	o.phase = kind + "_pending"
	return op, nil
}
func (o *ResidentOpen) setOriginalPhase(phase string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("database phase lacks original operation")
	}
	o.active.phase = phase
}
func (o *ResidentOpen) publishFileProgress(phase string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("database native effect lacks original operation")
	}
	o.active.phase = phase
	live := 0
	for _, f := range o.files.slots {
		if f != nil {
			live++
		}
	}
	o.fileObservation = residentSQLFileObservation{o.files.opened, o.files.closed, live}
}

// Directly deferred by the actual original operation. Abnormal/Goexit keeps the
// original slot and every retained object; an observation never issues Closed.
func (o *ResidentOpen) finishOriginal(op *residentSQLOperation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original database operation identity lost")
	}
	if !*normal {
		op.abnormal = true
		op.cause = cause
		if o.firstError == nil {
			o.firstError = &residentSQLAbnormal{cause: cause}
		}
		o.phase = "original_operation_unknown"
	} else {
		if *resultErr != nil {
			if o.firstError == nil {
				o.firstError = *resultErr
			}
			o.phase = op.kind + "_failed_retained"
		} else {
			o.phase = op.kind + "_returned"
		}
		if op.kind == "close" {
			o.closeError = *resultErr
		}
		o.active = nil
	}
	live := 0
	for _, f := range o.files.slots {
		if f != nil {
			live++
		}
	}
	o.fileObservation = residentSQLFileObservation{o.files.opened, o.files.closed, live}
	o.closeObservation.NativeOutcome = "unknown"
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *ResidentOpen) OpenOriginal(ctx context.Context) (binding *ResidentDatabaseBinding, resultErr error) {
	op, err := o.beginOriginal("open")
	if err != nil {
		return nil, err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	if resultErr = ctx.Err(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	o.setOriginalPhase("original_claim_validation_pending")
	original, err := o.claim.ValidateOriginal()
	if err != nil {
		resultErr = err
		normal = true
		return nil, resultErr
	}
	filename, err := canonicalResidentSQLPath(original.Path)
	if err != nil {
		resultErr = err
		normal = true
		return nil, resultErr
	}
	o.files.path = original.Path
	o.files.uid = original.UID
	if resultErr = o.files.openCanonicalFresh(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	if resultErr = o.matchOriginalPreparedFiles(ctx, original); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	// No fallback to legacy state.db-as-file, default DSN or independent opener.
	// mode=rw refuses implicit create; actual parent prepared the retained file.
	uri := url.URL{Scheme: "file", Path: filename}
	query := url.Values{}
	query.Set("mode", "rw")
	uri.RawQuery = query.Encode()
	o.setOriginalPhase("official_connector_construction_pending")
	standard, err := sqlite.NewConnector(uri.String())
	// NewConnector parses the DSN but does not open SQLite. Keep its actual
	// returned object before sql.OpenDB can start an internal pool task.
	o.mu.Lock()
	o.standardConnector = standard
	o.filename = filename
	if standard != nil {
		o.connector = &residentConnector{owner: o, original: standard, originalClaim: o.claim}
	}
	connector := o.connector
	o.mu.Unlock()
	if err != nil {
		resultErr = err
		normal = true
		return nil, resultErr
	}
	// The actual pool operation is already in this retained original task before
	// sql.OpenDB's internal opener task. No pool-ended/native witness is invented.
	o.setOriginalPhase("original_pool_construction_pending")
	pool := sql.OpenDB(connector)
	o.mu.Lock()
	o.pool = pool
	o.mu.Unlock()
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	pool.SetConnMaxIdleTime(0)
	o.setOriginalPhase("original_physical_connect_pending")
	if resultErr = pool.PingContext(ctx); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	o.setOriginalPhase("actual_selected_file_query_pending")
	if resultErr = o.verifyOriginalSelectedFile(ctx, pool, filename); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	if resultErr = o.files.validateCanonical(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	confirmed, err := o.claim.ValidateOriginal()
	if err != nil {
		resultErr = err
		normal = true
		return nil, resultErr
	}
	if confirmed != original {
		resultErr = fmt.Errorf("original database authority changed during open")
		normal = true
		return nil, resultErr
	}
	if resultErr = ctx.Err(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	binding = &ResidentDatabaseBinding{owner: o, generation: original.Generation, pool: pool}
	o.mu.Lock()
	o.binding = binding
	o.mu.Unlock()
	normal = true
	return binding, nil
}
func (o *ResidentOpen) verifyOriginalSelectedFile(ctx context.Context, pool *sql.DB, filename string) (resultErr error) {
	rows, err := pool.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	count, main := 0, false
	for rows.Next() {
		count++
		if count > 2 {
			return fmt.Errorf("unexpected attached database during original fresh open")
		}
		var sequence int
		var name, path string
		if err = rows.Scan(&sequence, &name, &path); err != nil {
			return err
		}
		if name == "main" {
			if main || sequence != 0 || path != filename {
				return fmt.Errorf("SQLite selected a different main file")
			}
			main = true
		} else if name != "temp" {
			return fmt.Errorf("unexpected original database attachment")
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !main {
		return fmt.Errorf("original SQLite main file was not observed")
	}
	return nil
}

// Only the actual original Manager construction obtains this pool from its
// registry binding. The retained driver/physical-close authority is not exposed.
func (o *ResidentOpen) DatabaseForOriginalBinding(binding *ResidentDatabaseBinding) (*sql.DB, error) {
	if o == nil || o.self != o {
		return nil, errResidentDatabaseOriginal
	}
	o.mu.Lock()
	if binding == nil || binding != o.binding || binding.owner != o || binding.pool != o.pool || o.closeTried {
		o.mu.Unlock()
		return nil, errResidentDatabaseOriginal
	}
	if o.active != nil {
		o.mu.Unlock()
		return nil, ErrResidentDatabasePending
	}
	if o.firstError != nil {
		err := o.firstError
		o.mu.Unlock()
		return nil, err
	}
	pool, claim, generation := o.pool, o.claim, binding.generation
	o.mu.Unlock()
	snapshot, err := claim.ValidateOriginal()
	if err != nil {
		return nil, err
	}
	if snapshot.Generation != generation {
		return nil, errResidentDatabaseOriginal
	}
	return pool, nil
}

// The actual Manager stop/join adapter must call this ONLY after all original
// DB users ended. The original role stop ticket authorizes that holder's cleanup;
// this method returns public pool API observations, NEVER a full pool/task/native
// Closed witness. Official Connect may be noncancelable once inside native open.
func (o *ResidentOpen) CloseOriginalSQLAPI(ctx context.Context, stop *lifetimeview.ManagerStop) (observation ResidentSQLCloseObservation, resultErr error) {
	if o == nil || o.self != o {
		return ResidentSQLCloseObservation{NativeOutcome: "unknown"}, errResidentDatabaseOriginal
	}
	if err := stop.ValidateOriginalClaim(o.claim); err != nil {
		return ResidentSQLCloseObservation{NativeOutcome: "unknown"}, err
	}
	o.mu.Lock()
	if o.active != nil {
		o.mu.Unlock()
		return ResidentSQLCloseObservation{NativeOutcome: "unknown"}, ErrResidentDatabasePending
	}
	if o.closeTried {
		observation = o.closeObservation
		resultErr = o.closeError
		o.mu.Unlock()
		return observation, resultErr
	}
	o.connectSealed = true
	o.closeObservation.ConnectorSealed = true
	pending := o.connect != nil && !o.connect.finished
	o.closeObservation.OriginalConnectPending = pending
	if pending {
		observation = o.closeObservation
		o.mu.Unlock()
		return observation, ErrResidentDatabasePending
	}
	o.mu.Unlock()
	op, err := o.beginOriginal("close")
	if err != nil {
		return ResidentSQLCloseObservation{NativeOutcome: "unknown"}, err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	o.mu.Lock()
	pool := o.pool
	o.mu.Unlock()
	if pool != nil {
		if resultErr = ctx.Err(); resultErr != nil {
			normal = true
			return o.closeObservationCopy(), resultErr
		}
		o.setOriginalPhase("original_standard_pool_close_pending")
		o.mu.Lock()
		o.closeObservation.PoolCloseAttempted = true
		o.mu.Unlock()
		// There is no direct Conn.Close/native fallback and no internal driver
		// join witness. database/sql owns the original unwrapped connection.
		resultErr = pool.Close()
		o.mu.Lock()
		o.closeObservation.PoolCloseReturned = true
		o.mu.Unlock()
	}
	o.mu.Lock()
	o.closeObservation.NativeOutcome = "unknown"
	observation = o.closeObservation
	o.mu.Unlock()
	// Original canonical directory/file descriptors remain retained too. Unknown
	// SQLite/VFS/TLS/pool completion authorizes neither their release nor retirement.
	normal = true
	return observation, resultErr
}
func (o *ResidentOpen) InspectOriginal() ResidentDatabaseInspection {
	if o == nil || o.self != o {
		return ResidentDatabaseInspection{Phase: "invalid", NativeCensusUncertain: true}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	result := ResidentDatabaseInspection{Phase: o.phase, ConnectorRetained: o.standardConnector != nil, PoolRetained: o.pool != nil, BindingRetained: o.binding != nil, FilesOpened: o.fileObservation.opened, FilesClosed: o.fileObservation.closed, LastPublishedLiveFiles: o.fileObservation.live, NativeCensusUncertain: true, FirstError: o.firstError, Connector: o.inspectConnectorLocked(), Close: o.closeObservation}
	if o.active != nil {
		result.OriginalOperationPending = true
		result.OriginalOperationAbnormal = o.active.abnormal
		result.OriginalOperationKind = o.active.kind
		result.OriginalOperationPhase = o.active.phase
	}
	return result
}
