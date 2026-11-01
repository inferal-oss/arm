package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/lifetimeview"
)

// Only the retained resident root may own this holder. Workers receive no Server,
// Manager, claim, descriptor or lifetime cancellation function. Construction is
// pure; no listener, HTTP route, native work or Manager loop is started here.
type residentWorkerServerPending struct {
	currentTried       bool
	currentValidation  *residentWorkerServerCurrentCall
	rootContext        context.Context
	mu                 sync.Mutex // publication only; no native effects or joins
	self               *residentWorkerServerPending
	view               *lifetimeview.WorkerServerView
	claim              *lifetimeview.WorkerServerClaim
	manager            *manager.ResidentWorkerManagerPending
	managerBound       *manager.ResidentWorkerManagerServerBinding
	server             *Server
	binding            *residentWorkerServerBinding
	original           lifetimeview.WorkerSnapshot
	active             *residentWorkerServerOperation
	openTried          bool
	opened             bool
	startTried         bool
	started            bool
	firstError         error
	preparedTried      bool
	preparedValidation *residentWorkerServerPreparedCall
}

type residentWorkerServerBinding struct {
	owner  *residentWorkerServerPending
	server *Server
	claim  *lifetimeview.WorkerServerClaim
}

type residentWorkerServerOperation struct {
	owner          *residentWorkerServerPending
	kind, phase    string
	abnormal       bool
	normalFinished bool // final original normal metadata only
	cause          any
}

type residentWorkerServerInspection struct {
	ServerRetained, ClaimRetained, ManagerBorrowerRetained, BindingRetained bool
	OpenAttempted, Opened, StartAttempted, Started                          bool
	OperationPending, OperationAbnormal                                     bool
	OperationKind, OperationPhase                                           string
	FirstError                                                              error
	NativeOutcome                                                           string
}

var errResidentWorkerServerOriginal = errors.New("original resident Server holder required")
var errResidentWorkerServerPending = errors.New("original resident Server effect pending; retain and inspect")

type residentWorkerServerAbnormal struct{ cause any }

func (e *residentWorkerServerAbnormal) Error() string {
	return "original resident Server effect ended abnormally; custody unresolved"
}
func (e *residentWorkerServerAbnormal) Unwrap() error { cause, _ := e.cause.(error); return cause }

func newResidentWorkerServerPending(view *lifetimeview.WorkerServerView, originalManager *manager.ResidentWorkerManagerPending, rootContext context.Context) (*residentWorkerServerPending, error) {
	if view == nil || originalManager == nil || rootContext == nil {
		return nil, errResidentWorkerServerOriginal
	}
	o := &residentWorkerServerPending{view: view, manager: originalManager, rootContext: rootContext}
	o.self = o
	o.server = &Server{workerResident: o, peerSlots: make(chan struct{}, 8), peerSurveySlots: make(chan struct{}, 2), storageScan: make(chan struct{}, 1), portScan: make(chan struct{}, 1), monitorSlots: make(chan struct{}, 32)}
	o.server.lifecycleContext = rootContext
	return o, nil
}

func (o *residentWorkerServerPending) begin(kind string) (*residentWorkerServerOperation, error) {
	if o == nil || o.self != o {
		return nil, errResidentWorkerServerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.server == nil || o.server.workerResident != o || o.server.resident != nil || !sameWorkerServerContextData(o.server.lifecycleContext, o.rootContext) || o.manager == nil {
		return nil, errResidentWorkerServerOriginal
	}
	if o.active != nil {
		return nil, errResidentWorkerServerPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	switch kind {
	case "open":
		if o.openTried {
			return nil, errResidentWorkerServerOriginal
		}
		o.openTried = true
	case "start":
		if !o.opened || o.startTried || o.preparedValidation == nil || !o.preparedValidation.returned || o.preparedValidation.resultError != nil || o.preparedValidation.op == nil || !o.preparedValidation.op.normalFinished || o.preparedValidation.op.abnormal {
			return nil, errResidentWorkerServerOriginal
		}
		o.startTried = true
	case "validate_current":
		if o.currentTried || !o.currentPreparedLocked(o.view) {
			return nil, errResidentWorkerServerOriginal
		}
		o.currentTried = true
	case "validate_prepared":
		if !o.opened || o.startTried || o.started || o.preparedTried {
			return nil, errResidentWorkerServerOriginal
		}
		o.preparedTried = true
	default:
		return nil, errResidentWorkerServerOriginal
	}
	op := &residentWorkerServerOperation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

func (o *residentWorkerServerPending) phase(value string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("original Server operation missing")
	}
	o.active.phase = value
}

func (o *residentWorkerServerPending) finish(op *residentWorkerServerOperation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original Server operation identity changed")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &residentWorkerServerAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && o.firstError == nil {
			o.firstError = *resultErr
		}
		if *resultErr == nil {
			if op.kind == "open" {
				o.opened = true
			}
			if op.kind == "start" {
				o.started = true
			}
		}
		o.active = nil
		op.normalFinished = true
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *residentWorkerServerPending) openOriginal(ctx context.Context) (resultErr error) {
	op, err := o.begin("open")
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	if !sameWorkerServerContextData(ctx, o.rootContext) {
		resultErr = errResidentWorkerServerOriginal
	} else {
		resultErr = o.openBody(o.rootContext)
	}
	normal = true
	return resultErr
}

func (o *residentWorkerServerPending) openBody(ctx context.Context) error {
	if ctx == nil {
		return errResidentWorkerServerOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.manager.CheckOriginalRootContext(o.rootContext); err != nil {
		return err
	}
	o.phase("original_Server_claim_pending")
	claim, err := o.view.ClaimOriginal()
	o.mu.Lock()
	o.claim = claim
	o.mu.Unlock()
	if err != nil {
		return err
	}
	o.phase("original_Server_native_validation_pending")
	original, err := claim.ValidateOriginalContext(ctx)
	if err != nil {
		return err
	}
	o.mu.Lock()
	o.original = original
	o.mu.Unlock()
	o.phase("original_Manager_Server_borrower_pending")
	borrower, err := o.manager.ClaimOriginalServerBinding(claim)
	o.mu.Lock()
	o.managerBound = borrower
	o.mu.Unlock()
	if err != nil {
		return err
	}
	// No Manager pointer is borrowed before its actual original loops start.
	// This retained original Server and borrower already exist before that effect.
	if o.server.resident != nil || o.server.workerResident != o || !sameWorkerServerContextData(o.server.lifecycleContext, o.rootContext) {
		return errResidentWorkerServerOriginal
	}
	o.server.home, o.server.generation = original.Path, original.Generation
	binding := &residentWorkerServerBinding{owner: o, server: o.server, claim: claim}
	o.mu.Lock()
	o.binding = binding
	o.mu.Unlock()
	return nil
}

// The actual root must validate its standing service/image/native custody before
// invoking this effect. It does not use a worker deadline or legacy Close defer.
func (o *residentWorkerServerPending) startOriginalManager() (resultErr error) {
	op, err := o.begin("start")
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	o.phase("original_Manager_loop_launch_pending")
	resultErr = o.manager.StartOriginal()
	if resultErr == nil {
		o.phase("actual_original_Manager_borrow_pending")
		var actual *manager.Manager
		actual, resultErr = o.manager.ManagerForOriginalServer(o.managerBound)
		if resultErr == nil {
			o.server.Manager = actual
		}
	}
	normal = true
	return resultErr
}

func (o *residentWorkerServerPending) validateIngressBinding(ctx context.Context, binding *residentWorkerServerBinding, s *Server) error {
	if o == nil || o.self != o {
		return errResidentWorkerServerOriginal
	}
	o.mu.Lock()
	valid := o.active == nil && o.firstError == nil && o.opened && o.started && o.server == s && s.workerResident == o && s.resident == nil && sameWorkerServerContextData(s.lifecycleContext, o.rootContext) && o.binding == binding && binding != nil && binding.owner == o && binding.server == s && binding.claim == o.claim
	claim, original, borrower := o.claim, o.original, o.managerBound
	o.mu.Unlock()
	if !valid {
		return errResidentWorkerServerOriginal
	}
	confirmed, err := claim.ValidateOriginalContext(ctx)
	if err != nil {
		return err
	}
	if confirmed != original || s.home != original.Path || s.generation != original.Generation {
		return errResidentWorkerServerOriginal
	}
	actual, err := o.manager.ManagerForOriginalServer(borrower)
	if err != nil {
		return err
	}
	if actual != s.Manager {
		return fmt.Errorf("original Server Manager pointer changed")
	}
	return nil
}

func (o *residentWorkerServerPending) inspectOriginal() residentWorkerServerInspection {
	if o == nil || o.self != o {
		return residentWorkerServerInspection{FirstError: errResidentWorkerServerOriginal, NativeOutcome: "unknown"}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v := residentWorkerServerInspection{ServerRetained: o.server != nil, ClaimRetained: o.claim != nil, ManagerBorrowerRetained: o.managerBound != nil, BindingRetained: o.binding != nil, OpenAttempted: o.openTried, Opened: o.opened, StartAttempted: o.startTried, Started: o.started, FirstError: o.firstError, NativeOutcome: "unknown"}
	if op := o.active; op != nil {
		v.OperationPending, v.OperationAbnormal, v.OperationKind, v.OperationPhase = true, op.abnormal, op.kind, op.phase
	}
	return v
}

func sameWorkerServerContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}
