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

// WorkerResidentOpen is the actual database startup owner. The Manager pending holder
// must retain it BEFORE OpenOriginal. It owns its actual official driver
// Connector, wrapper-visible original Connect,
// pool, file registry and original operation, including partial/unknown outcomes.
// No function here constructs a service grant or adopts an existing Manager.
type WorkerResidentOpen struct {
	currentTried          bool
	currentValidation     *workerCurrentDatabaseCall
	mu                    sync.Mutex
	self                  *WorkerResidentOpen
	rootContext           context.Context
	claim                 *lifetimeview.WorkerManagerClaim
	files                 *workerResidentSQLFiles
	filename              string
	active                *workerResidentSQLOperation
	openTried, closeTried bool
	standardConnector     driver.Connector
	connector             *workerResidentConnector
	connect               *workerResidentConnectCall
	connectSealed         bool
	connectRefusals       uint64
	firstConnectRefusal   error
	pool                  *sql.DB
	binding               *WorkerResidentDatabaseBinding
	firstError            error
	phase                 string
	fileObservation       workerResidentSQLFileObservation
	closeObservation      WorkerResidentSQLCloseObservation
	closeError            error
	preparedTried         bool
	preparedValidation    *workerPreparedDatabaseCall
	selectedFile          *workerPreparedDatabaseCall
}
type workerResidentSQLOperation struct {
	owner          *WorkerResidentOpen
	kind, phase    string
	abnormal       bool
	normalFinished bool // final original normal metadata only
	cause          any
}
type WorkerResidentDatabaseBinding struct {
	owner      *WorkerResidentOpen
	generation string
	pool       *sql.DB
}
type workerResidentSQLFileObservation struct {
	opened, closed uint64
	live           int
}
type WorkerResidentSQLCloseObservation struct {
	ConnectorSealed, OriginalConnectPending bool
	PoolCloseAttempted, PoolCloseReturned   bool
	NativeOutcome                           string // ALWAYS unknown; pool return is not task/native Closed
}

type WorkerResidentDatabaseInspection struct {
	Phase                                               string
	OriginalOperationPending, OriginalOperationAbnormal bool
	OriginalOperationKind, OriginalOperationPhase       string
	ConnectorRetained, PoolRetained, BindingRetained    bool
	FilesOpened, FilesClosed                            uint64
	LastPublishedLiveFiles                              int
	NativeCensusUncertain                               bool
	FirstError                                          error
	Connector                                           WorkerResidentConnectorInspection
	Close                                               WorkerResidentSQLCloseObservation
}

var ErrWorkerResidentDatabasePending = errors.New("original resident database operation pending; retain and inspect")
var errWorkerResidentDatabaseOriginal = errors.New("original resident database holder/binding required")

type workerResidentSQLAbnormal struct{ cause any }

func (e *workerResidentSQLAbnormal) Error() string {
	return "original resident database operation ended abnormally"
}
func (e *workerResidentSQLAbnormal) Unwrap() error { value, _ := e.cause.(error); return value }

// Pure construction. This does not validate/reassert a lock, open SQLite, start
// a pool/task, or interpret metadata as native/service custody. The concrete
// Manager constructor owns the single original claim and this pending slot.
func NewWorkerResidentOpen(claim *lifetimeview.WorkerManagerClaim) (*WorkerResidentOpen, error) {
	if claim == nil {
		return nil, errWorkerResidentDatabaseOriginal
	}
	o := &WorkerResidentOpen{claim: claim, phase: "pending"}
	o.self = o
	o.files = &workerResidentSQLFiles{owner: o}
	o.closeObservation.NativeOutcome = "unknown"
	return o, nil
}
func (o *WorkerResidentOpen) beginOriginal(kind string) (*workerResidentSQLOperation, error) {
	if o == nil || o.self != o {
		return nil, errWorkerResidentDatabaseOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrWorkerResidentDatabasePending
	}
	switch kind {
	case "open":
		if o.openTried {
			if o.firstError != nil {
				return nil, o.firstError
			}
			return nil, fmt.Errorf("original database open already attempted")
		}
		o.openTried = true
	case "validate_current":
		if o.currentTried || !o.currentPreparedLocked(o.binding, o.claim, o.pool) {
			return nil, errWorkerResidentDatabaseOriginal
		}
		o.currentTried = true
	case "validate_prepared":
		if o.firstError != nil || o.closeTried || o.connectSealed || o.preparedTried || !o.openTried || o.binding == nil || o.pool == nil || o.claim == nil {
			return nil, errWorkerResidentDatabaseOriginal
		}
		o.preparedTried = true
	default:
		return nil, errWorkerResidentDatabaseOriginal
	}
	op := &workerResidentSQLOperation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	o.phase = kind + "_pending"
	return op, nil
}
func (o *WorkerResidentOpen) setOriginalPhase(phase string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("database phase lacks original operation")
	}
	o.active.phase = phase
}
func (o *WorkerResidentOpen) publishFileProgress(phase string) {
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
	o.fileObservation = workerResidentSQLFileObservation{o.files.opened, o.files.closed, live}
}

// Directly deferred by the actual original operation. Abnormal/Goexit keeps the
// original slot and every retained object; an observation never issues Closed.
func (o *WorkerResidentOpen) finishOriginal(op *workerResidentSQLOperation, normal *bool, resultErr *error) {
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
			o.firstError = &workerResidentSQLAbnormal{cause: cause}
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
	o.fileObservation = workerResidentSQLFileObservation{o.files.opened, o.files.closed, live}
	o.closeObservation.NativeOutcome = "unknown"
	if *normal && !op.abnormal {
		op.normalFinished = true
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *WorkerResidentOpen) OpenOriginal(ctx context.Context) (binding *WorkerResidentDatabaseBinding, resultErr error) {
	if ctx == nil {
		return nil, errWorkerResidentDatabaseOriginal
	}
	op, err := o.beginOriginal("open")
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	o.rootContext = ctx
	o.mu.Unlock()
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	if resultErr = ctx.Err(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	o.setOriginalPhase("original_claim_validation_pending")
	original, err := o.claim.ValidateOriginalContext(ctx)
	if err != nil {
		resultErr = err
		normal = true
		return nil, resultErr
	}
	filename, err := canonicalWorkerResidentSQLPath(original.Path)
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
		o.connector = &workerResidentConnector{owner: o, original: standard, originalClaim: o.claim}
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
	selected := &workerPreparedDatabaseCall{owner: o, op: op, claim: o.claim, pool: pool}
	o.mu.Lock()
	o.selectedFile = selected // exact original query/rows/destinations before SQL effects
	o.mu.Unlock()
	resultErr = o.validatePreparedSelectedFile(ctx, selected)
	o.mu.Lock()
	selected.returned, selected.resultError = true, resultErr
	o.mu.Unlock()
	if resultErr != nil {
		normal = true
		return nil, resultErr
	}
	if resultErr = o.files.validateCanonical(); resultErr != nil {
		normal = true
		return nil, resultErr
	}
	confirmed, err := o.claim.ValidateOriginalContext(ctx)
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
	binding = &WorkerResidentDatabaseBinding{owner: o, generation: original.Generation, pool: pool}
	o.mu.Lock()
	o.binding = binding
	o.mu.Unlock()
	normal = true
	return binding, nil
}

// Only the actual original Manager construction obtains this pool from its
// registry binding. The retained driver/physical-close authority is not exposed.
func (o *WorkerResidentOpen) DatabaseForOriginalBinding(binding *WorkerResidentDatabaseBinding) (*sql.DB, error) {
	if o == nil || o.self != o {
		return nil, errWorkerResidentDatabaseOriginal
	}
	o.mu.Lock()
	if binding == nil || binding != o.binding || binding.owner != o || binding.pool != o.pool || o.closeTried {
		o.mu.Unlock()
		return nil, errWorkerResidentDatabaseOriginal
	}
	if o.active != nil {
		o.mu.Unlock()
		return nil, ErrWorkerResidentDatabasePending
	}
	if o.firstError != nil {
		err := o.firstError
		o.mu.Unlock()
		return nil, err
	}
	pool, claim, generation, ctx := o.pool, o.claim, binding.generation, o.rootContext
	o.mu.Unlock()
	snapshot, err := claim.ValidateOriginalContext(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot.Generation != generation {
		return nil, errWorkerResidentDatabaseOriginal
	}
	return pool, nil
}

func (o *WorkerResidentOpen) InspectOriginal() WorkerResidentDatabaseInspection {
	if o == nil || o.self != o {
		return WorkerResidentDatabaseInspection{Phase: "invalid", NativeCensusUncertain: true}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	result := WorkerResidentDatabaseInspection{Phase: o.phase, ConnectorRetained: o.standardConnector != nil, PoolRetained: o.pool != nil, BindingRetained: o.binding != nil, FilesOpened: o.fileObservation.opened, FilesClosed: o.fileObservation.closed, LastPublishedLiveFiles: o.fileObservation.live, NativeCensusUncertain: true, FirstError: o.firstError, Connector: o.inspectConnectorLocked(), Close: o.closeObservation}
	if o.active != nil {
		result.OriginalOperationPending = true
		result.OriginalOperationAbnormal = o.active.abnormal
		result.OriginalOperationKind = o.active.kind
		result.OriginalOperationPhase = o.active.phase
	}
	return result
}
