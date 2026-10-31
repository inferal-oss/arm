package server

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/lifetimeview"
)

// Only the retained resident root may own this holder. Workers receive no Server,
// Manager, claim, descriptor or lifetime cancellation function. Construction is
// pure; no listener, HTTP route, native work or Manager loop is started here.
type residentServerPending struct {
	mu                 sync.Mutex // publication only; no native effects or joins
	self               *residentServerPending
	view               *lifetimeview.ServerView
	claim              *lifetimeview.ServerClaim
	manager            *manager.ResidentManagerPending
	managerBound       *manager.ResidentManagerServerBinding
	server             *Server
	binding            *residentServerBinding
	original           lifetimeview.Snapshot
	active             *residentServerOperation
	openTried          bool
	opened             bool
	startTried         bool
	started            bool
	firstError         error
	preparedTried      bool
	preparedValidation *residentServerPreparedCall
}

type residentServerBinding struct {
	owner  *residentServerPending
	server *Server
	claim  *lifetimeview.ServerClaim
}

type residentServerOperation struct {
	owner       *residentServerPending
	kind, phase string
	abnormal    bool
	cause       any
}

type residentServerInspection struct {
	ServerRetained, ClaimRetained, ManagerBorrowerRetained, BindingRetained bool
	OpenAttempted, Opened, StartAttempted, Started                          bool
	OperationPending, OperationAbnormal                                     bool
	OperationKind, OperationPhase                                           string
	FirstError                                                              error
	NativeOutcome                                                           string
}

var errResidentServerOriginal = errors.New("original resident Server holder required")
var errResidentServerPending = errors.New("original resident Server effect pending; retain and inspect")

type residentServerAbnormal struct{ cause any }

func (e *residentServerAbnormal) Error() string {
	return "original resident Server effect ended abnormally; custody unresolved"
}
func (e *residentServerAbnormal) Unwrap() error { cause, _ := e.cause.(error); return cause }

func newResidentServerPending(view *lifetimeview.ServerView, originalManager *manager.ResidentManagerPending) (*residentServerPending, error) {
	if view == nil || originalManager == nil {
		return nil, errResidentServerOriginal
	}
	o := &residentServerPending{view: view, manager: originalManager}
	o.self = o
	o.server = &Server{resident: o, peerSlots: make(chan struct{}, 8), peerSurveySlots: make(chan struct{}, 2), storageScan: make(chan struct{}, 1), portScan: make(chan struct{}, 1), monitorSlots: make(chan struct{}, 32)}
	return o, nil
}

func (o *residentServerPending) begin(kind string) (*residentServerOperation, error) {
	if o == nil || o.self != o {
		return nil, errResidentServerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, errResidentServerPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	switch kind {
	case "open":
		if o.openTried {
			return nil, errResidentServerOriginal
		}
		o.openTried = true
	case "start":
		if !o.opened || o.startTried {
			return nil, errResidentServerOriginal
		}
		o.startTried = true
	case "validate_prepared":
		if !o.opened || o.startTried || o.started || o.preparedTried {
			return nil, errResidentServerOriginal
		}
		o.preparedTried = true
	default:
		return nil, errResidentServerOriginal
	}
	op := &residentServerOperation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

func (o *residentServerPending) phase(value string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("original Server operation missing")
	}
	o.active.phase = value
}

func (o *residentServerPending) finish(op *residentServerOperation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original Server operation identity changed")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &residentServerAbnormal{cause: cause}
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
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *residentServerPending) openOriginal(ctx context.Context) (resultErr error) {
	op, err := o.begin("open")
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.openBody(ctx)
	normal = true
	return resultErr
}

func (o *residentServerPending) openBody(ctx context.Context) error {
	if ctx == nil {
		return errResidentServerOriginal
	}
	if err := ctx.Err(); err != nil {
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
	borrower, err := o.manager.ClaimOriginalServer(claim)
	o.mu.Lock()
	o.managerBound = borrower
	o.mu.Unlock()
	if err != nil {
		return err
	}
	// No Manager pointer is borrowed before its actual original loops start.
	// This retained original Server and borrower already exist before that effect.
	o.server.home, o.server.generation = original.Path, original.Generation
	binding := &residentServerBinding{owner: o, server: o.server, claim: claim}
	o.mu.Lock()
	o.binding = binding
	o.mu.Unlock()
	return nil
}

// The actual root must validate its standing service/image/native custody before
// invoking this effect. It does not use a worker deadline or legacy Close defer.
func (o *residentServerPending) startOriginalManager() (resultErr error) {
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

func (o *residentServerPending) validateIngressBinding(ctx context.Context, binding *residentServerBinding, s *Server) error {
	if o == nil || o.self != o {
		return errResidentServerOriginal
	}
	o.mu.Lock()
	valid := o.active == nil && o.firstError == nil && o.opened && o.started && o.server == s && s.resident == o && o.binding == binding && binding != nil && binding.owner == o && binding.server == s && binding.claim == o.claim
	claim, original, borrower := o.claim, o.original, o.managerBound
	o.mu.Unlock()
	if !valid {
		return errResidentServerOriginal
	}
	confirmed, err := claim.ValidateOriginalContext(ctx)
	if err != nil {
		return err
	}
	if confirmed != original || s.home != original.Path || s.generation != original.Generation {
		return errResidentServerOriginal
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

func (o *residentServerPending) inspectOriginal() residentServerInspection {
	if o == nil || o.self != o {
		return residentServerInspection{FirstError: errResidentServerOriginal, NativeOutcome: "unknown"}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v := residentServerInspection{ServerRetained: o.server != nil, ClaimRetained: o.claim != nil, ManagerBorrowerRetained: o.managerBound != nil, BindingRetained: o.binding != nil, OpenAttempted: o.openTried, Opened: o.opened, StartAttempted: o.startTried, Started: o.started, FirstError: o.firstError, NativeOutcome: "unknown"}
	if op := o.active; op != nil {
		v.OperationPending, v.OperationAbnormal, v.OperationKind, v.OperationPhase = true, op.abnormal, op.kind, op.phase
	}
	return v
}
