package residentservice

import (
	"context"
	"os"
	"reflect"
	"runtime"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

// Only the worker-tagged actual process census can supply this pending child.
// Fixed13 worker entry and native serve are separate dependencies. A record or
// synthetic descriptor registry cannot supply the original source.
type WorkerChildPending struct {
	self                                                                        *WorkerChildPending
	custody                                                                     *workerCustody
	source                                                                      *residentrefs.ServiceOriginal
	journal                                                                     *residentrefs.JournalView
	identities                                                                  residentrefs.ServiceIdentities
	rootContext                                                                 context.Context
	bootstrap                                                                   WorkerBootstrapData
	environment                                                                 []string
	runtime                                                                     *mesh.WorkerRuntimeView
	runtimeProof                                                                mesh.WorkerRuntimeProofData
	receiveTried, received, runtimeTried, runtimeBound, chargeTried, birthTried bool
	charge                                                                      *WorkerManagerCharge
	birth                                                                       *WorkerBirthBinding
}

func NewWorkerChildPending(source *residentrefs.ServiceOriginal) (*WorkerChildPending, error) {
	if source == nil || source.RequireOriginalWorkerCore() != nil {
		return nil, ErrInvalid
	}
	p := &WorkerChildPending{source: source, custody: newWorkerCustodyData("")}
	p.self = p
	p.custody.child = p
	view, err := source.ClaimJournalOriginalView()
	p.journal = view
	// The source also retains a partially claimed view. Return this holder on
	// normal errors so the composed root can retain its destination too.
	return p, err
}

func sameWorkerContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

func (p *WorkerChildPending) beginOriginal(kind string) (*operation, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.self != p.custody || p.custody.child != p || p.source == nil || p.journal == nil || p.custody.raw == nil {
		return nil, ErrInvalid
	}
	w := p.custody
	r := w.raw
	r.mu.Lock()
	defer r.mu.Unlock()
	if !w.typedOriginalLocked() || !w.legacyEmptyLocked() {
		return nil, ErrInvalid
	}
	if r.active != nil {
		return nil, ErrPending
	}
	if r.firstError != nil {
		return nil, r.firstError
	}
	switch kind {
	case "worker-child-receive":
		if p.receiveTried {
			return nil, ErrInvalid
		}
		p.receiveTried = true
	case "worker-child-runtime":
		if !p.received || p.runtimeTried {
			return nil, ErrInvalid
		}
		p.runtimeTried = true
	case "worker-child-environment":
		if !p.received {
			return nil, ErrInvalid
		}
	case "worker-child-charge":
		if !p.received || !p.runtimeBound || p.chargeTried {
			return nil, ErrInvalid
		}
		p.chargeTried = true
	case "worker-charge-current":
		if !p.received || !p.runtimeBound || p.charge == nil {
			return nil, ErrInvalid
		}
	case "worker-child-birth":
		if !p.received || !p.runtimeBound || p.charge == nil || p.birthTried {
			return nil, ErrInvalid
		}
		p.birthTried = true
	case "worker-birth-current":
		if p.birth == nil {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	return w.operationLocked(kind)
}

func (p *WorkerChildPending) ReceiveWorkerOriginal(ctx context.Context, data WorkerBootstrapData) (resultErr error) {
	op, err := p.beginOriginal("worker-child-receive")
	if err != nil {
		return err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	p.bootstrap = data
	resultErr = p.receiveBodyOriginal(ctx, data)
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		p.received = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerChildPending) receiveBodyOriginal(ctx context.Context, data WorkerBootstrapData) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	if !sameWorkerContextData(ctx, ctx) || data.Validate() != nil {
		return ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	p.rootContext = ctx
	w.raw.mu.Unlock()
	if err := p.source.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	if err := p.source.MaterializeOriginal(ctx); err != nil {
		return err
	}
	ids, err := p.source.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	w.raw.mu.Lock()
	p.identities = ids
	w.raw.mu.Unlock()
	journal, err := w.readJournalOriginal(ctx)
	if err != nil {
		return err
	}
	if err = validateWorkerBootstrapJournalData(data, journal); err != nil {
		return err
	}
	g, d := journal.records[0].Grant, journal.records[1].Debit
	if g.Intent.HomeID != sharedID(ids.Home) || d.LockID != sharedID(ids.Lock) || d.JournalID != sharedID(ids.Journal) || data.Build.GOOS != runtime.GOOS || data.Build.GOARCH != runtime.GOARCH || data.Build.GoBuild != runtime.Version() {
		return ErrInvalid
	}
	w.raw.mu.Lock()
	w.path, w.raw.path, w.journal, w.grant, w.secret = data.Home, data.Home, journal, g, data.Secret
	debit := &WorkerOriginalDebit{owner: w, record: d}
	debit.self = debit
	w.debit = debit
	w.raw.mu.Unlock()
	return p.validateEnvironmentBodyOriginal(ctx)
}

func (p *WorkerChildPending) validateEnvironmentBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.custody
	if !sameWorkerContextData(ctx, p.rootContext) || w.grant == nil || w.grant.Intent.Profile.Validate() != nil {
		return ErrInvalid
	}
	w.raw.files.progress("worker_child_actual_environment_snapshot_pending")
	actual := os.Environ()
	w.raw.mu.Lock()
	p.environment = actual
	w.raw.mu.Unlock()
	return w.grant.Intent.Profile.Core.RuntimeEnvironment.MatchesActual(actual)
}

func (p *WorkerChildPending) ValidateWorkerEnvironmentOriginal(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-child-environment")
	if err != nil {
		return err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.validateEnvironmentBodyOriginal(ctx)
	normal = true
	return resultErr
}

func (p *WorkerChildPending) BindOriginalWorkerRuntime(ctx context.Context, view *mesh.WorkerRuntimeView) (resultErr error) {
	if view == nil {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-runtime")
	if err != nil {
		return err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	p.custody.raw.mu.Lock()
	p.runtime = view
	p.custody.raw.mu.Unlock()
	resultErr = p.currentBodyOriginal(ctx, true)
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		p.runtimeBound = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerChildPending) currentShapeLocked() bool {
	w := p.custody
	return w.typedOriginalLocked() && w.legacyEmptyLocked() && p.received && w.grant != nil && w.debit != nil && w.debit.self == w.debit && w.debit.owner == w && len(w.journal.records) >= 2 && w.journal.records[0].Grant == w.grant && w.journal.records[1].Debit == w.debit.record && w.grant.Intent.Home == w.path && w.grant.Intent.HomeID == sharedID(p.identities.Home) && w.debit.record.LockID == sharedID(p.identities.Lock) && w.debit.record.JournalID == sharedID(p.identities.Journal)
}

func (p *WorkerChildPending) currentBodyOriginal(ctx context.Context, requireRuntime bool) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.custody
	w.raw.mu.Lock()
	valid := p.currentShapeLocked() && sameWorkerContextData(ctx, p.rootContext)
	w.raw.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	if err := p.source.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	ids, err := p.source.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	if ids != p.identities {
		return ErrInvalid
	}
	if err = p.validateEnvironmentBodyOriginal(ctx); err != nil {
		return err
	}
	if requireRuntime {
		if p.runtime == nil {
			return ErrInvalid
		}
		proof, err := p.runtime.ProofOriginal(ctx, p.source, w.path, w.debit.record.ID)
		if err != nil {
			return err
		}
		if proof.SHA256 != w.grant.Intent.Profile.Core.Build.SHA256 || proof.Size != w.grant.Intent.Profile.Core.Build.Size {
			return ErrInvalid
		}
		w.raw.mu.Lock()
		p.runtimeProof = proof
		w.raw.mu.Unlock()
	}
	return ctx.Err()
}

func (p *WorkerChildPending) ObserveOriginal() WorkerCustodyObservation {
	if p == nil || p.self != p || p.custody == nil || p.custody.child != p {
		return WorkerCustodyObservation{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	}
	return p.custody.observeData()
}
