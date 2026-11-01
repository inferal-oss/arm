package server

import (
	"bytes"
	"context"
	"sync"
	"time"

	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentcost"
	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/nativeowner"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
	"getarm.org/internal/server/lifetimeview"
)

type residentWorkerChildPending struct {
	intent                         []byte
	birth                          *residentservice.WorkerBirthBinding
	serveBirth                     *residentservice.WorkerServeBirthView
	mu                             *sync.Mutex
	self                           *residentWorkerChildPending
	entry                          *residentWorkerCoreEntry
	rootContext                    context.Context // independent of parent/API/UI workers
	refs                           *residentrefs.ChildPending
	control                        *residentrefs.ControlOriginal
	serviceCustody                 *residentrefs.ServiceOriginal
	fenceCustody                   *residentrefs.FenceOriginal
	imageCustody                   *residentrefs.ImageOriginal
	service                        *residentservice.WorkerChildPending
	fence                          *residentbirth.WorkerInheritedFencePending
	image                          *mesh.WorkerInheritedImagePending
	runtime                        *mesh.WorkerRuntimeView // exact once-issued nonclosing worker runtime view
	bootstrapCall                  *residentWorkerChildBootstrapCall
	bootstrapRaw                   []byte
	bootstrap                      workerRootBootstrapData
	composed                       residentrefs.ComposedIdentities
	native                         *nativeowner.WorkerServeIssuer
	managerView                    *lifetimeview.WorkerManagerView
	serverView                     *lifetimeview.WorkerServerView
	charge                         *residentservice.WorkerManagerCharge
	funding                        *residentservice.WorkerStartupFundingPending
	fundingView                    *residentservice.WorkerStartupFundingView
	fundingSource                  residentcost.PendingSourceContractData
	manager                        *manager.ResidentWorkerManagerPending
	server                         *residentWorkerServerPending
	active                         *residentWorkerChildOperation
	prepareOperation               *residentWorkerChildOperation
	protocol                       *residentWorkerRootProtocolCall
	protocolTried                  bool
	protocolPhases                 [4]*residentWorkerProtocolPhase
	prepareTried, bindingsPrepared bool
	firstError                     error
	preparedValidationTried        bool
	preparedValidation             *residentWorkerRootPreparedCall
	rootBoundTried                 bool
	rootBound                      *residentWorkerRootBoundCall
}

// Owns the one finite bootstrap I/O context attempt. Runtime constructor/cancel
// internals stay opaque; these return records do not assert native quiescence.
type residentWorkerChildBootstrapCall = residentWorkerProtocolPhase

type residentWorkerChildOperation struct {
	kind           string
	phase          string
	abnormal       bool
	normalFinished bool
	finalError     error
	cause          any
}

// In-place fixed-role initialization. The actual outer entry retains root BEFORE
// this call; no return-then-assignment can lose a partial original. All13 raw
// entries are published before component recipients or native holders exist.
func (root *residentWorkerChildPending) initializeOriginal() (resultErr error) {
	if root == nil || root.self != nil || root.mu != nil || root.entry == nil || !root.entry.ownsInitializingRoot(root) {
		return errResidentWorkerChildOriginal
	}
	root.mu = &sync.Mutex{}
	root.self = root
	defer func() {
		if resultErr != nil {
			root.mu.Lock()
			root.firstError = resultErr
			root.mu.Unlock()
		}
	}()
	resultErr = residentrefs.NewFixedWorkerChildPending(&root.refs) // FIRST: same process-wide raw13 claim
	if resultErr != nil {
		return resultErr
	}
	root.rootContext = context.Background() // never a launcher/request lifetime
	root.serviceCustody, root.fenceCustody, root.imageCustody, root.control, resultErr = root.refs.NewOriginalRecipients()
	if resultErr != nil {
		return resultErr
	}
	root.service, resultErr = residentservice.NewWorkerChildPending(root.serviceCustody)
	if resultErr != nil {
		return resultErr
	}
	resultErr = residentbirth.NewWorkerInheritedFencePending(&root.fence, root.rootContext, root.fenceCustody)
	if resultErr != nil {
		return resultErr
	}
	resultErr = mesh.NewWorkerInheritedImagePending(&root.image, root.rootContext, root.imageCustody)
	if resultErr != nil {
		return resultErr
	}
	return root.refs.DelegateOriginalRecipients(root.serviceCustody, root.fenceCustody, root.imageCustody, root.control)
}
func (r *residentWorkerChildPending) phase(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		panic("composed original child operation missing")
	}
	r.active.phase = value
}
func (r *residentWorkerChildPending) finish(op *residentWorkerChildOperation, normal *bool, resultErr *error) {
	cause := recover()
	r.mu.Lock()
	if r.active != op {
		r.mu.Unlock()
		panic("original composed child identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if r.firstError == nil {
			r.firstError = &residentWorkerServerAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && r.firstError == nil {
			r.firstError = *resultErr
		}
		if op.kind == "prepare" {
			r.bindingsPrepared = *resultErr == nil
		}
		r.active = nil
	}
	if *normal && !op.abnormal {
		op.normalFinished, op.finalError = true, *resultErr
	}
	r.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

// Performs actual original references -> service grant/env -> image/fence ->
// native issuer -> Manager/official DB -> Server binding. This stage sends no
// ACK, starts no Manager loops and publishes no ingress. The outer role performs
// authenticated handoff; public readiness requires a genuinely admitted worker.
// Errors/panics retain originals and Root lifetime without legacy abort/recovery.
func (r *residentWorkerChildPending) prepareOriginalBindings(bootstrapContext context.Context) (resultErr error) {
	if r == nil || r.self != r || r.mu == nil || r.entry == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentWorkerChildOriginal
	}
	r.mu.Lock()
	if r.active != nil || r.firstError != nil || r.prepareTried {
		r.mu.Unlock()
		return errResidentWorkerChildOriginal
	}
	r.prepareTried = true
	op := &residentWorkerChildOperation{kind: "prepare", phase: "registered_before_inherited_native_effect"}
	r.prepareOperation = op
	r.active = op
	r.mu.Unlock()
	normal := false
	defer r.finish(op, &normal, &resultErr)
	resultErr = r.prepareBody(bootstrapContext)
	normal = true
	return resultErr
}
func (r *residentWorkerChildPending) prepareBody(bootstrapContext context.Context) (err error) {
	r.phase("all13_CLOEXEC_before_any_wrapper_or_stat")
	if err = r.refs.SealOriginalDescriptors(bootstrapContext); err != nil {
		return err
	}
	r.phase("actual_original_control_materialization")
	if err = r.control.MaterializeOriginal(bootstrapContext); err != nil {
		return err
	}
	r.phase("actual_original_complete_bounded_bootstrap")
	err = r.receiveOriginalBootstrap(bootstrapContext)
	if err != nil {
		return err
	}
	if err = decodeWorkerRootBootstrapData(r.bootstrapRaw, &r.bootstrap); err != nil {
		return err
	}
	// Startup/parent reconnect timeout controls ONLY the channel bootstrap. Once
	// received, actual original root work uses its independent funded lifetime.
	ctx := r.rootContext
	r.phase("actual_delegated_worker_schema3_service_journal_and_schema2_environment")
	if err = r.service.ReceiveWorkerOriginal(ctx, r.bootstrap.Service); err != nil {
		return err
	}
	r.phase("actual_delegated_fence_and_same_intent")
	if err = r.fence.VerifyOriginal(ctx, r.bootstrap.Fence); err != nil {
		return err
	}
	intent, err := r.fence.IntentOriginalData()
	r.intent = intent
	if err != nil {
		return err
	}
	expected, err := encodeWorkerRootIntentData(r.bootstrap)
	if err != nil {
		return err
	}
	if !bytes.Equal(intent, expected) {
		return errResidentWorkerChildOriginal
	}
	r.phase("actual_delegated_image_and_current_child_runtime")
	if err = r.image.VerifyOriginal(ctx, r.bootstrap.Image); err != nil {
		return err
	}
	r.runtime, err = r.image.RuntimeViewOriginal()
	if err != nil {
		return err
	}
	if err = r.service.BindOriginalWorkerRuntime(ctx, r.runtime); err != nil {
		return err
	}
	r.phase("actual_all13_current_native_names_before_original_claims")
	r.composed, err = r.refs.ValidateComposedOriginal(ctx, r.bootstrap.Service.DebitID)
	if err != nil {
		return err
	}
	if err = validateWorkerRootComposedData(r.bootstrap, r.composed); err != nil {
		return err
	}
	r.phase("actual_original_standing_Manager_charge")
	r.charge, err = r.service.ClaimOriginalWorkerManagerCharge()
	if err != nil {
		return err
	}
	r.phase("actual_original_service_birth_binding")
	r.birth, err = r.service.BindOriginalWorkerBirth(r.charge, r.intent)
	if err != nil {
		return err
	}
	r.phase("actual_original_native_serve_birth_view")
	r.serveBirth, err = r.birth.ClaimOriginalServeView()
	if err != nil {
		return err
	}
	r.phase("actual_complete_pending_source_census_before_native_Open")
	r.fundingSource, err = compiledWorkerPendingSourceData()
	if err != nil {
		return err
	}
	r.phase("actual_original_funding_owner_retained_before_committed_evidence_read")
	if err = residentservice.NewWorkerStartupFundingPending(&r.funding, r.service, r.charge, r.runtime, r.birth); err != nil {
		return err
	}
	if err = r.funding.CheckOriginal(ctx, r.fundingSource); err != nil {
		return err
	}
	r.fundingView, err = r.funding.ClaimOriginalServeFunding()
	if err != nil {
		return err
	}
	r.phase("actual_worker_native_issuer_retained_before_Open")
	if err = nativeowner.NewInheritedWorkerServePending(&r.native, r.fence, r.serveBirth, r.fundingView); err != nil {
		return err
	}
	if err = r.native.OpenOriginal(); err != nil {
		return err
	}
	r.phase("actual_original_native_Manager_view")
	r.managerView, err = r.native.ManagerView()
	if err != nil {
		return err
	}
	r.phase("actual_original_native_Server_view")
	r.serverView, err = r.native.ServerView()
	if err != nil {
		return err
	}
	r.phase("actual_original_Manager_pending_before_database_effects")
	r.manager, err = manager.NewResidentWorkerManagerPending(r.managerView, r.bootstrap.Settings, r.charge)
	if err != nil {
		return err
	}
	r.phase("actual_original_Manager_official_database_schema")
	if err = r.manager.OpenOriginal(ctx); err != nil {
		return err
	}
	r.phase("actual_original_Server_pending_and_Manager_borrower")
	r.server, err = newResidentWorkerServerPending(r.serverView, r.manager, r.rootContext)
	if err != nil {
		return err
	}
	if err = r.server.openOriginal(ctx); err != nil {
		return err
	}
	if err = r.service.ValidateWorkerEnvironmentOriginal(ctx); err != nil {
		return err
	}
	// No ManagerForOriginalServer, loop start, listener, marker, ACK or retirement
	// is performed here. The actual worker/controller/ingress cut must follow.
	return nil
}

// Called ONLY after all13 CLOEXEC and the original control materialization.
// Creating a timer before that seal could leak inherited originals to helpers.
// The call record is retained before the stdlib timeout constructor is entered;
// an unreturned opaque constructor/cancel is UNKNOWN, never fabricated Closed.
func (r *residentWorkerChildPending) receiveOriginalBootstrap(parent context.Context) error {
	if parent == nil {
		return errResidentWorkerChildOriginal
	}
	if r.entry == nil || !r.entry.ownsPreparingRoot(r) {
		return errResidentWorkerChildOriginal
	}
	call := &residentWorkerChildBootstrapCall{child: r, childOperation: r.prepareOperation, index: 0, kind: "Rboot", lifetime: parent, bootstrapPostConstructorClock: true}
	r.mu.Lock()
	r.bootstrapCall = call
	r.protocolPhases[0] = call // EXACT existing bootstrap allocation, not another scope
	call.contextAttempted = true
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	r.mu.Lock()
	call.context, call.cancel, call.contextReturned = ctx, cancel, true
	call.clockAttempted = true
	r.mu.Unlock()
	observed := time.Now() // post-constructor observation; never a constructor timestamp
	r.mu.Lock()
	call.observedAt, call.clockReturned = observed, true
	call.deadlineAttempted = true
	r.mu.Unlock()
	deadline, finite := ctx.Deadline()
	r.mu.Lock()
	call.deadline, call.deadlineFinite, call.deadlineReturned = deadline, finite, true
	r.mu.Unlock()
	if !finite || deadline.After(observed.Add(5*time.Second)) {
		return errResidentWorkerChildOriginal // retain owned unknown context; no synthetic cancellation
	}
	r.mu.Lock()
	call.receiveAttempted = true
	r.mu.Unlock()
	raw, err := r.control.ReceiveWorkerBootstrapOriginal(ctx)
	r.mu.Lock()
	r.bootstrapRaw = raw
	call.receiveReturned, call.receiveError = true, err
	call.cancelAttempted = true
	r.mu.Unlock()
	cancel() // this finite bootstrap I/O context only; NEVER the Root lifetime
	r.mu.Lock()
	call.cancelReturned = true
	r.mu.Unlock()
	return err
}
