package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/ports"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/syncservice"
)

// ResidentManagerPending retains the actual Manager before any original claim,
// native DB open, pool task, SQL/schema initialization or sampler construction.
// The trusted resident root must retain this holder before OpenOriginal. It must
// separately obtain actual standing service authorization BEFORE invoking it.
// This constructor provides no such grant, no live migration and no fresh abort.
type ResidentManagerPending struct {
	mu                                    sync.Mutex // bounded publication only; never SQL, native work or Manager.mu
	self                                  *ResidentManagerPending
	view                                  *lifetimeview.ManagerView
	claim                                 *lifetimeview.ManagerClaim
	manager                               *Manager
	settings                              daemonconfig.Settings
	database                              *database.ResidentOpen
	binding                               *database.ResidentDatabaseBinding
	schemaTx                              *sql.Tx
	schemaCommitTried                     bool
	schemaCommitError                     error
	original                              lifetimeview.Snapshot
	active                                *residentManagerOperation
	openTried, ready, startTried, started bool
	firstError                            error
	server                                *ResidentManagerServerBinding
}

type residentManagerOperation struct {
	owner       *ResidentManagerPending
	kind, phase string
	abnormal    bool
	cause       any
}

// This is a single actual Server's lifetime borrower, not a worker token. The
// trusted Server keeps it until all its original DB users end. No End/Closed
// method exists in this cut; neither a callback nor an empty registry can issue
// a root retirement permit. Workers never receive the Manager pointer.
type ResidentManagerServerBinding struct {
	owner     *ResidentManagerPending
	claim     *lifetimeview.ServerClaim
	validated bool
}

type ResidentManagerInspection struct {
	ManagerRetained, ClaimRetained, DatabaseRetained, BindingRetained bool
	SchemaTransactionRetained, SchemaCommitAttempted                  bool
	OpenAttempted, Initialized, StartAttempted, LoopsLaunched         bool
	ServerBorrowerRetained, ServerBorrowerValidated                   bool
	OperationPending, OperationAbnormal                               bool
	OperationKind, OperationPhase                                     string
	FirstError, SchemaCommitError                                     error
	NativeOutcome                                                     string // always unknown; no root/database/service Closed
}

var errResidentManagerOriginal = errors.New("original resident Manager holder required")
var ErrResidentManagerPending = errors.New("original resident Manager operation pending; retain and inspect")

type residentManagerAbnormal struct{ cause any }

func (e *residentManagerAbnormal) Error() string {
	return "original resident Manager operation ended abnormally; custody unresolved"
}
func (e *residentManagerAbnormal) Unwrap() error { v, _ := e.cause.(error); return v }

// Pure Go construction. Configuration ceilings are not service resource grants.
func NewResidentManagerPending(view *lifetimeview.ManagerView, settings daemonconfig.Settings) (*ResidentManagerPending, error) {
	if view == nil {
		return nil, errResidentManagerOriginal
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	settings.StoragePools = append([]model.StoragePoolConfig(nil), settings.StoragePools...)
	o := &ResidentManagerPending{view: view, settings: settings}
	o.self = o
	o.manager = &Manager{
		resident:                   o,
		kubernetesSourceAdmissions: map[string]bool{}, kubernetesSources: map[string]*KubernetesSourcePreparation{},
		relayTransits: map[string]*RelayTransit{}, jobs: map[string]*job{},
		syncWorkers: map[string]*SyncWorkerHold{}, syncSourceWorkers: map[string]*SyncSourceHold{},
		stop: make(chan struct{}), storageJobs: map[string]bool{}, portSockets: map[string][]*ports.Socket{},
		syncSchemaVersion: "2", leaseClockRead: leaseclock.Read,
		diskSampleSlots: make(chan struct{}, 1), usages: map[string]workloadSample{}, policyClocks: map[string]incidentClock{},
		inboxChanged: make(chan struct{}), inboxSlots: make(chan struct{}, 64),
	}
	return o, nil
}

func (o *ResidentManagerPending) beginOriginal(kind string) (*residentManagerOperation, error) {
	if o == nil || o.self != o {
		return nil, errResidentManagerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrResidentManagerPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	switch kind {
	case "open":
		if o.openTried {
			return nil, fmt.Errorf("original Manager open already attempted")
		}
		o.openTried = true
	case "start":
		if !o.ready || o.startTried {
			return nil, fmt.Errorf("original initialized Manager may start only once")
		}
		o.startTried = true
	case "server_borrower":
		if !o.ready || o.server != nil {
			return nil, fmt.Errorf("one initialized original Server borrower required")
		}
	default:
		return nil, errResidentManagerOriginal
	}
	op := &residentManagerOperation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

func (o *ResidentManagerPending) phase(phase string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("resident Manager effect lacks original operation")
	}
	o.active.phase = phase
}

// Directly deferred in each actual effect. Panic/Goexit retains the SAME active
// original operation, all partial objects and first error. No cleanup replaces it.
func (o *ResidentManagerPending) finishOriginal(op *residentManagerOperation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original Manager operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &residentManagerAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && o.firstError == nil {
			o.firstError = *resultErr
		}
		if *resultErr == nil {
			if op.kind == "open" {
				o.ready = true
			}
			if op.kind == "start" {
				o.started = true
			}
		}
		o.active = nil
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *ResidentManagerPending) OpenOriginal(ctx context.Context) (resultErr error) {
	op, err := o.beginOriginal("open")
	if err != nil {
		return err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	resultErr = o.openOriginalBody(ctx)
	normal = true
	return resultErr
}

func (o *ResidentManagerPending) openOriginalBody(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.phase("original_Manager_claim_pending")
	claim, err := o.view.ClaimOriginal()
	o.mu.Lock()
	o.claim = claim
	o.mu.Unlock() // retain even the nonnil partial failed claim
	if err != nil {
		return err
	}
	original, err := claim.ValidateOriginal()
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.original = original
	o.mu.Unlock()
	o.phase("original_database_pending_construction")
	pending, err := database.NewResidentOpen(claim)
	o.mu.Lock()
	o.database = pending
	o.mu.Unlock()
	if err != nil {
		return err
	}
	o.phase("original_database_open_pending")
	binding, err := pending.OpenOriginal(ctx)
	o.mu.Lock()
	o.binding = binding
	o.mu.Unlock()
	if err != nil {
		return err
	}
	db, err := pending.DatabaseForOriginalBinding(binding)
	if err != nil {
		return err
	}
	m := o.manager
	m.db, m.home = db, original.Path // actual retained pool; no attach/reopen/legacy fallback
	o.phase("original_fresh_base_schema_pending")
	if err = o.installFreshOriginalSchema(ctx); err != nil {
		return err
	}
	o.phase("original_native_helpers_pending")
	m.pressureEpoch = local.ID()
	m.cpuObserver = observe.NewCPUSampler()
	m.portNamespace, m.portErr = ports.Namespace()
	if err = db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&m.host); err != nil {
		return err
	}
	identity := local.CurrentIdentity()
	if identity.UID != fmt.Sprint(original.UID) {
		return fmt.Errorf("original Manager account changed")
	}
	m.owner, m.ownerUsername = identity.UID, identity.Username
	o.phase("original_configuration_and_schema_pending")
	if err = m.applyStartupConfiguration(o.settings); err != nil {
		return err
	}
	// Fresh original has no old Unix pairs to recover; initialize the real guard.
	if err = initForwardUnixPairGuard(db, false); err != nil {
		return err
	}
	if err = syncservice.VerifyDurableSQLite(ctx, db); err != nil {
		return err
	}
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"sync", func() error { return m.initSyncRecords(ctx) }},
		{"subpools", m.initSubpoolRecords}, {"budgets", m.initBudgetRecords},
		{"runs", m.initRunRecords}, {"cargo", m.initCargoRecords}, {"gradle", m.initGradleRecords},
		{"lease_reviews", m.initLeaseReviews}, {"storage_exports", m.initStorageExports}, {"contacts", m.migrateContacts},
		{"storage_pools", func() error { return m.initStoragePools(&o.settings) }},
		{"lease_clock", m.initLeaseClock}, {"deliveries", m.recoverDeliveries}, {"policy_outbox", m.recoverPolicyOutbox},
		{"host_pressure", m.loadHostPressure}, {"shutdown", m.recoverShutdown}, {"budget_bindings", m.migrateBudgetBindings},
		{"scheduling", m.initScheduling}, {"workspaces", m.initWorkspaces},
	} {
		if err = ctx.Err(); err != nil {
			return err
		}
		o.phase("original_initialize_" + step.name)
		if err = step.run(); err != nil {
			return err
		} // retain actual Manager; never abort-close its pool/providers
	}
	o.phase("original_initial_observations_pending")
	m.sample()
	m.initializeCPU()
	m.sampleMemory()
	m.sampleDisk()
	confirmed, err := claim.ValidateOriginal()
	if err != nil {
		return err
	}
	if confirmed != original {
		return fmt.Errorf("original Manager authority changed during initialization")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (o *ResidentManagerPending) StartOriginal() (resultErr error) {
	op, err := o.beginOriginal("start")
	if err != nil {
		return err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	o.phase("original_Manager_loops_launch_pending")
	// The original pending holder and Manager.wg retain all actual launched loops.
	// Method return only means launch completed, never that these loops ended.
	o.manager.startOriginalLoops()
	normal = true
	return nil
}

func (o *ResidentManagerPending) ClaimOriginalServer(claim *lifetimeview.ServerClaim) (binding *ResidentManagerServerBinding, resultErr error) {
	if claim == nil {
		return nil, errResidentManagerOriginal
	}
	op, err := o.beginOriginal("server_borrower")
	if err != nil {
		return nil, err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	binding = &ResidentManagerServerBinding{owner: o, claim: claim}
	o.mu.Lock()
	o.server = binding
	o.mu.Unlock()
	o.phase("actual_Server_borrower_validation_pending")
	server, err := claim.ValidateOriginal()
	if err != nil {
		resultErr = err
		normal = true
		return binding, resultErr
	}
	manager, err := o.claim.ValidateOriginal()
	if err != nil {
		resultErr = err
		normal = true
		return binding, resultErr
	}
	if server != manager || manager != o.original {
		resultErr = fmt.Errorf("Server and Manager do not belong to the same original issuer")
		normal = true
		return binding, resultErr
	}
	o.mu.Lock()
	binding.validated = true
	o.mu.Unlock()
	normal = true
	return binding, nil
}

// Only the actual resident Server constructor receives this pointer. It is
// never serialized, passed to API/UI workers or used to prove borrower closure.
func (o *ResidentManagerPending) ManagerForOriginalServer(binding *ResidentManagerServerBinding) (*Manager, error) {
	if o == nil || o.self != o {
		return nil, errResidentManagerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrResidentManagerPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	if binding == nil || o.server != binding || binding.owner != o || !binding.validated || !o.ready || !o.started {
		return nil, errResidentManagerOriginal
	}
	return o.manager, nil
}

func (o *ResidentManagerPending) InspectOriginal() ResidentManagerInspection {
	if o == nil || o.self != o {
		return ResidentManagerInspection{NativeOutcome: "unknown", FirstError: errResidentManagerOriginal}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v := ResidentManagerInspection{ManagerRetained: o.manager != nil, ClaimRetained: o.claim != nil, DatabaseRetained: o.database != nil, BindingRetained: o.binding != nil, SchemaTransactionRetained: o.schemaTx != nil, SchemaCommitAttempted: o.schemaCommitTried, SchemaCommitError: o.schemaCommitError, OpenAttempted: o.openTried, Initialized: o.ready, StartAttempted: o.startTried, LoopsLaunched: o.started, ServerBorrowerRetained: o.server != nil, ServerBorrowerValidated: o.server != nil && o.server.validated, FirstError: o.firstError, NativeOutcome: "unknown"}
	if o.active != nil {
		v.OperationPending = true
		v.OperationAbnormal = o.active.abnormal
		v.OperationKind = o.active.kind
		v.OperationPhase = o.active.phase
	}
	return v
}
