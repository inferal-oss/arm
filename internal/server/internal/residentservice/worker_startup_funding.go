package residentservice

import (
	"context"
	"encoding/json"
	"runtime"
	"unsafe"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentcost"
)

// The compiled verifier is a finite suballocation, prepaid from the SAME
// original grant/debit. It is independent of the evidence input it must read.
// These conservative provisional reservations require explicit source/evidence
// review; they are not observed whole-root maxima or a native grant factory.
func compiledWorkerStructuralRequiredData() (residentcost.PendingTotalsData, error) {
	const externalOwnSourceBacking = 16 << 20
	known := uint64(unsafe.Sizeof(WorkerStartupFundingPending{}) + unsafe.Sizeof(workerStartupVerifierReservation{}) + unsafe.Sizeof(workerFundingInputRead{}) + unsafe.Sizeof(WorkerParentPending{}) + unsafe.Sizeof(WorkerChildPending{}) + unsafe.Sizeof(workerCustody{}) + unsafe.Sizeof(owner{}) + unsafe.Sizeof(nativeFiles{}) + unsafe.Sizeof(WorkerMeasuredProfile{}) + unsafe.Sizeof(WorkerEvidenceBundle{}) + unsafe.Sizeof(WorkerPendingStartupReview{}))
	// Preflight itself has bounded input/token backing. Allow sixfold JSON
	// escaping plus retained decoder/typed/base64 data and old/new growth buffers
	// during a malformed-input canonical re-marshal; post-decode caps alone do
	// not justify this allocation budget. Internal syscall counts stay opaque.
	known += uint64(24*MaxEvidenceBytes + 4*MaxWorkerPendingStartupReviewBytes + 4*workerJournalMaxBytes + 32*workerControlWireBytes + externalOwnSourceBacking)
	if known > 64<<20 {
		return residentcost.PendingTotalsData{}, ErrInvalid
	}
	return residentcost.PendingTotalsData{Resources: residentcost.ResourcesData{CPU: 2.25, Memory: 1 << 30, Disk: 64 << 20}, Bounds: residentcost.BoundsData{Tasks: 256, Descriptors: 512, Children: 1, Sockets: 64, NativePending: 512, IOBytes: 8 << 30, RetainedReceiptBytes: 32 << 20}, LogicalMemoryBytes: 2 * known}, nil
}

type workerStartupVerifierReservation struct {
	self                                    *workerStartupVerifierReservation
	owner                                   *workerCustody
	parent                                  *WorkerParentPending
	child                                   *WorkerChildPending
	charge                                  *WorkerManagerCharge
	birth                                   *WorkerBirthBinding
	grant                                   *WorkerGrantRecord
	debit                                   *WorkerOriginalDebit
	sourceOp, debitOp                       *operation
	ctx                                     context.Context
	required                                residentcost.PendingTotalsData
	budgetChecked, complete                 bool
	schedulerGetTried, schedulerGetReturned bool
	schedulerSetTried, schedulerSetReturned bool
	schedulerAfterTried, schedulerAfterDone bool
	schedulerBefore, schedulerSet, after    int
}

func workerProfileCostBudgetData(profile WorkerMeasuredProfile) (residentcost.ResourcesData, residentcost.BoundsData) {
	r, b := profile.Core.Resources, profile.Core.Bounds
	return residentcost.ResourcesData{CPU: r.CPU, Memory: r.Memory, Disk: r.Disk}, residentcost.BoundsData{Tasks: uint64(b.Tasks), Descriptors: uint64(b.Descriptors), Children: uint64(b.Children), Sockets: uint64(b.Sockets), NativePending: uint64(b.NativePending), IOBytes: b.IOBytes, RetainedReceiptBytes: b.RetainedReceiptBytes}
}

func workerStructuralEnvironmentData(profile WorkerMeasuredProfile) bool {
	if profile.Validate() != nil || profile.Core.RuntimeEnvironment == nil {
		return false
	}
	for _, entry := range profile.Core.RuntimeEnvironment.Entries {
		if entry == "GOMAXPROCS=1" {
			return true
		}
	}
	return false
}

// Original in-progress parent open and its completed FIRST concrete native
// grant read. A profile/record DTO by itself cannot prepay runtime effects.
func workerParentGrantReadOriginalLocked(p *WorkerParentPending, w *workerCustody) bool {
	if p == nil || w == nil || p.custody != w || w.parent != p || w.raw == nil || w.raw.files == nil || w.raw.active == nil || w.raw.active.owner != w.raw || w.raw.active.kind != "worker-parent-open" || len(w.reads) != 1 || len(w.journal.records) != 1 || w.grant == nil || w.journal.records[0].Grant != w.grant || w.append != nil || w.debit != nil {
		return false
	}
	r := w.reads[0]
	if r == nil || r.owner != w || r.kind != "worker-parent-open" || !r.finished || r.firstError != nil || r.before == nil || r.after == nil || r.calls <= 0 || r.calls > 32 || len(r.data) == 0 || r.read != len(r.data) || int64(len(r.data)) != w.journal.bytes || r.after.Size() != w.journal.bytes {
		return false
	}
	for _, original := range []*retainedFD{w.raw.files.home, w.raw.files.lock, w.raw.files.journal} {
		if original == nil || original.file == nil || original.closeTried || original.id.Inode == 0 {
			return false
		}
		found := false
		for _, slot := range w.raw.files.slots {
			found = found || slot == original
		}
		if !found {
			return false
		}
	}
	return w.raw.files.home.id == w.grant.Intent.HomeID
}

// Called inside the actual parent's still-active open, AFTER the original
// locked grant read and BEFORE current executable capture. Only this standalone
// launcher process is changed. The call/result is retained before invocation;
// the child profile Env cannot silently assert the parent's actual scheduler.
func (p *WorkerParentPending) prepayStructuralOriginal(ctx context.Context) error {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || ctx == nil || p.launcher == nil || p.launcher.RequireOriginalStandaloneStart() != nil {
		return ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	if p.structural != nil || w.parent != p || w.child != nil || !workerParentGrantReadOriginalLocked(p, w) || !w.typedOriginalLocked() || !w.legacyEmptyLocked() || w.raw.firstError != nil {
		w.raw.mu.Unlock()
		return ErrInvalid
	}
	r := &workerStartupVerifierReservation{owner: w, parent: p, grant: w.grant, sourceOp: w.raw.active, ctx: ctx}
	r.self = r
	p.structural = r // retain BEFORE arithmetic/runtime calls and all errors
	profile := w.grant.Intent.Profile
	w.raw.mu.Unlock()
	required, err := compiledWorkerStructuralRequiredData()
	if err != nil || !workerStructuralEnvironmentData(profile) {
		return ErrInvalid
	}
	r.required = required
	resources, bounds := workerProfileCostBudgetData(profile)
	if residentcost.CheckBudgetData(required, resources, bounds) != nil || ctx.Err() != nil {
		return ErrInvalid
	}
	r.budgetChecked = true
	r.schedulerGetTried = true
	r.schedulerBefore = runtime.GOMAXPROCS(0)
	r.schedulerGetReturned = true
	r.schedulerSetTried = true
	r.schedulerSet = runtime.GOMAXPROCS(1)
	r.schedulerSetReturned = true
	r.schedulerAfterTried = true
	r.after = runtime.GOMAXPROCS(0)
	r.schedulerAfterDone = true
	if r.after != 1 || r.schedulerSet != r.schedulerBefore || ctx.Err() != nil {
		return ErrInvalid
	}
	r.complete = true
	return nil
}

// Bind only the actual normal committed debit, still inside its original
// prepare operation. Group construction later requires both operations normal.
func (p *WorkerParentPending) bindStructuralDebitOriginal(debit *WorkerOriginalDebit) error {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil {
		return ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	r := p.structural
	if r == nil || r.self != r || r.parent != p || r.owner != w || r.child != nil || r.debit != nil || !r.complete || !r.budgetChecked || r.grant != w.grant || r.sourceOp == nil || !r.sourceOp.normalFinished || r.sourceOp.abnormal || w.raw.firstError != nil || w.raw.active == nil || w.raw.active.kind != "worker-parent-debit" || debit == nil || debit != w.debit || debit.owner != w || debit.record == nil || w.append == nil || !w.append.committed || w.append.firstError != nil {
		return ErrInvalid
	}
	r.debit, r.debitOp = debit, w.raw.active
	return nil
}

// Caller holds the same original custody mutex; DATA identity only.
func (r *workerStartupVerifierReservation) paidLocked() bool {
	if r == nil || r.self != r || r.owner == nil || !r.budgetChecked || !r.complete || !r.schedulerGetTried || !r.schedulerGetReturned || !r.schedulerAfterTried || !r.schedulerAfterDone || r.after != 1 || r.owner.grant != r.grant || r.owner.debit != r.debit || r.ctx == nil {
		return false
	}
	if r.parent != nil {
		return r.child == nil && r.charge == nil && r.birth == nil && r.owner.parent == r.parent && r.parent.structural == r && r.parent.opened && r.parent.debitComplete && r.sourceOp != nil && r.sourceOp.normalFinished && !r.sourceOp.abnormal && r.debitOp != nil && r.debitOp.normalFinished && !r.debitOp.abnormal && r.schedulerSetTried && r.schedulerSetReturned && r.schedulerSet == r.schedulerBefore
	}
	return r.child != nil && r.owner.child == r.child && r.child.structural == r && r.child.charge == r.charge && r.child.birth == r.birth && r.birth != nil && r.birth.charge == r.charge && r.child.received && r.child.runtimeBound && r.child.currentShapeLocked()
}

type workerStartupFundingCheck struct {
	owner          *WorkerStartupFundingPending
	op             *operation
	attempted      bool
	normalReturned bool
	err            error
}

// Distinct actual original funding owner. The parent branch can authorize its
// own prelaunch helpers only; it can never issue a child/native serve view.
type WorkerStartupFundingPending struct {
	self              *WorkerStartupFundingPending
	owner             *workerCustody
	parent            *WorkerParentPending
	child             *WorkerChildPending
	charge            *WorkerManagerCharge
	birth             *WorkerBirthBinding
	runtime           *mesh.WorkerRuntimeView
	ctx               context.Context
	reservation       *workerStartupVerifierReservation
	check             *workerStartupFundingCheck
	inputRead         *workerFundingInputRead
	evidence          *WorkerEvidenceBundle
	review            *WorkerPendingStartupReview
	requiredSource    *residentcost.PendingSourceContractData
	requiredSourceRaw []byte
	sourceSHA256      string
	view              *WorkerStartupFundingView
	viewTried         bool
}

func NewWorkerParentStartupFundingPending(destination **WorkerStartupFundingPending, parent *WorkerParentPending, debit *WorkerOriginalDebit, ctx context.Context) error {
	if destination == nil || *destination != nil || parent == nil || parent.self != parent || parent.custody == nil || parent.custody.raw == nil || ctx == nil {
		return ErrInvalid
	}
	w := parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if parent.funding != nil || parent.group != nil || w.raw.active != nil || w.raw.firstError != nil || !w.originalWorkerDebitLocked(debit) || parent.structural == nil || !parent.structural.paidLocked() || !sameWorkerContextData(parent.structural.ctx, ctx) {
		return ErrInvalid
	}
	p := &WorkerStartupFundingPending{owner: w, parent: parent, ctx: ctx, reservation: parent.structural}
	p.self = p
	parent.funding, *destination = p, p
	return nil
}

func NewWorkerStartupFundingPending(destination **WorkerStartupFundingPending, child *WorkerChildPending, charge *WorkerManagerCharge, executable *mesh.WorkerRuntimeView, birth *WorkerBirthBinding) error {
	if destination == nil || *destination != nil || child == nil || child.self != child || charge == nil || birth == nil || executable == nil {
		return ErrInvalid
	}
	original, w, err := charge.originalData()
	if err != nil || original != child {
		return ErrInvalid
	}
	birthChild, birthOwner, err := birth.originalData()
	if err != nil || birthChild != child || birthOwner != w {
		return ErrInvalid
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if child.funding != nil || child.structural != nil || child.runtime != executable || child.birth != birth || child.charge != charge || w.raw.active != nil || w.raw.firstError != nil || child.rootContext == nil {
		return ErrInvalid
	}
	p := &WorkerStartupFundingPending{owner: w, child: child, charge: charge, birth: birth, runtime: executable, ctx: child.rootContext}
	p.self = p
	r := &workerStartupVerifierReservation{owner: w, child: child, charge: charge, birth: birth, grant: w.grant, debit: w.debit, ctx: child.rootContext}
	r.self = r
	p.reservation = r
	child.funding, child.structural, *destination = p, r, p // before verifier arithmetic/getter
	return nil
}

func (p *WorkerStartupFundingPending) prepayChildVerifierData() error {
	if p.child == nil || p.parent != nil || p.reservation == nil || p.reservation.complete {
		return ErrInvalid
	}
	r := p.reservation
	required, err := compiledWorkerStructuralRequiredData()
	if err != nil || !workerStructuralEnvironmentData(p.owner.grant.Intent.Profile) {
		return ErrInvalid
	}
	r.required = required
	resources, bounds := workerProfileCostBudgetData(p.owner.grant.Intent.Profile)
	if residentcost.CheckBudgetData(required, resources, bounds) != nil || p.ctx.Err() != nil {
		return ErrInvalid
	}
	r.budgetChecked = true
	r.schedulerGetTried = true
	r.schedulerBefore = runtime.GOMAXPROCS(0)
	r.schedulerGetReturned = true
	r.schedulerAfterTried = true
	r.after = runtime.GOMAXPROCS(0)
	r.schedulerAfterDone = true
	if r.schedulerBefore != 1 || r.after != 1 {
		return ErrInvalid
	}
	r.complete = true
	return nil
}

func (r *workerStartupVerifierReservation) requireOriginalFundingRead(p *WorkerStartupFundingPending, input *workerFundingInputRead, op *operation, ctx context.Context) error {
	if r == nil || r.self != r || p == nil || p.self != p || r.owner == nil || p.owner != r.owner || input == nil || op == nil || p.inputRead != input || input.owner != p || input.reservation != r || input.op != op || !sameWorkerContextData(ctx, p.ctx) {
		return ErrInvalid
	}
	w := r.owner
	w.raw.mu.Lock()
	valid := r.paidLocked() && w.raw.firstError == nil && p.reservation == r && p.check != nil && p.check.op == op && p.check.owner == p && p.check.attempted && !p.check.normalReturned && !op.normalFinished && !op.abnormal && w.raw.active == op && input.input == w.grant.Intent.Inputs.Evidence
	if p.parent != nil {
		valid = valid && p.child == nil && p.parent.funding == p && op.kind == "worker-parent-startup-funding"
	} else {
		valid = valid && p.child != nil && p.child.funding == p && p.child.structural == r && op.kind == "worker-child-startup-funding"
	}
	w.raw.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	return ctx.Err()
}

func (p *WorkerStartupFundingPending) publishCheck(normal *bool, resultErr *error) {
	w := p.owner
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	c := p.check
	if c == nil || c.owner != p || c.op == nil {
		panic("original worker funding check identity lost")
	}
	c.normalReturned = *normal && c.op.normalFinished && !c.op.abnormal
	c.err = *resultErr
}

func (p *WorkerStartupFundingPending) CheckOriginal(ctx context.Context, requiredSource residentcost.PendingSourceContractData) (resultErr error) {
	if p == nil || p.self != p || p.owner == nil || p.owner.raw == nil || !sameWorkerContextData(ctx, p.ctx) {
		return ErrInvalid
	}
	w := p.owner
	w.raw.mu.Lock()
	if p.check != nil || w.raw.active != nil || w.raw.firstError != nil {
		w.raw.mu.Unlock()
		return ErrInvalid
	}
	c := &workerStartupFundingCheck{owner: p, attempted: true}
	p.check = c
	w.raw.mu.Unlock()
	var err error
	if p.parent != nil {
		c.op, err = p.parent.beginOriginal("worker-parent-startup-funding")
	} else if p.child != nil {
		c.op, err = p.child.beginOriginal("worker-child-startup-funding")
	} else {
		err = ErrInvalid
	}
	if err != nil {
		c.err = err
		return err
	}
	normal := false
	defer p.publishCheck(&normal, &resultErr)
	defer w.raw.finish(c.op, &normal, &resultErr)
	resultErr = p.checkBodyOriginal(ctx, requiredSource)
	normal = true
	return resultErr
}

func (p *WorkerStartupFundingPending) checkBodyOriginal(ctx context.Context, requiredSource residentcost.PendingSourceContractData) error {
	if p.child != nil {
		if err := p.prepayChildVerifierData(); err != nil {
			return err
		}
	}
	fingerprint, err := requiredSource.FingerprintData()
	if err != nil {
		return ErrInvalid
	}
	// Retain a private bounded canonical copy. Caller slice aliases cannot
	// mutate the source description that justified this original completion.
	p.requiredSourceRaw, err = json.Marshal(requiredSource)
	if err != nil || len(p.requiredSourceRaw) > residentcost.MaxLedgerBytes {
		return ErrInvalid
	}
	p.requiredSource = &residentcost.PendingSourceContractData{}
	if json.Unmarshal(p.requiredSourceRaw, p.requiredSource) != nil {
		return ErrInvalid
	}
	p.sourceSHA256 = fingerprint
	p.inputRead = &workerFundingInputRead{owner: p, reservation: p.reservation, op: p.check.op, input: p.owner.grant.Intent.Inputs.Evidence}
	if err = p.inputRead.readOriginal(ctx); err != nil {
		return err
	}
	if residentcost.PreflightEvidenceJSONData(p.inputRead.data) != nil {
		return ErrInvalid
	}
	p.evidence = &WorkerEvidenceBundle{}
	if decodeOperatorDocument(p.inputRead.data, MaxEvidenceBytes, p.evidence) != nil {
		return ErrInvalid
	}
	review, err := workerPendingStartupReviewData(p.owner.grant.Intent.Profile, *p.evidence, *p.requiredSource)
	if err != nil {
		return err
	}
	p.review = &review
	// Full pending scope contains the prepaid verifier; it cannot forget it or
	// hide already-retained obligations behind an otherwise valid small total.
	if residentcost.CheckBudgetData(p.reservation.required, review.CheckedTotals.Resources, review.CheckedTotals.Bounds) != nil {
		return ErrInvalid
	}
	floor, err := p.owner.grant.Intent.Profile.Contract.LogicalFloorData()
	if err != nil {
		return ErrInvalid
	}
	_, floorBounds := workerProfileCostBudgetData(WorkerMeasuredProfile{Core: MeasuredProfile{Bounds: floor}})
	if residentcost.CheckBudgetData(residentcost.PendingTotalsData{Bounds: floorBounds}, review.CheckedTotals.Resources, review.CheckedTotals.Bounds) != nil {
		return ErrInvalid
	}
	if err := p.inputRead.retireReadReferencesOriginal(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

func (p *WorkerStartupFundingPending) completedLocked() bool {
	if p == nil || p.self != p || p.ctx == nil || p.owner == nil || p.owner.self != p.owner || p.owner.raw == nil || p.owner.grant == nil || p.owner.raw.firstError != nil || !p.owner.typedOriginalLocked() || !p.owner.legacyEmptyLocked() {
		return false
	}
	w, r, c, input := p.owner, p.reservation, p.check, p.inputRead
	if r == nil || r.owner != w || !sameWorkerContextData(r.ctx, p.ctx) || !r.paidLocked() || c == nil || c.owner != p || c.op == nil || c.op.owner != w.raw || !c.attempted || !c.normalReturned || c.err != nil || !c.op.normalFinished || c.op.abnormal || input == nil || input.owner != p || input.op != c.op || input.reservation != r || input.input != w.grant.Intent.Inputs.Evidence || !input.completed || !input.retirementComplete || !input.retirementAttempted || input.firstError != nil || p.requiredSource == nil || len(p.requiredSourceRaw) == 0 || len(p.requiredSourceRaw) > residentcost.MaxLedgerBytes || p.evidence == nil || p.review == nil || p.review.Stage != residentcost.PendingStage || p.review.SourceContractSHA256 != p.sourceSHA256 || !validDigest(p.sourceSHA256) {
		return false
	}
	if p.parent != nil {
		return p.child == nil && w.parent == p.parent && p.parent.funding == p && r.parent == p.parent && p.charge == nil && p.birth == nil && p.runtime == nil && c.op.kind == "worker-parent-startup-funding"
	}
	return p.child != nil && w.child == p.child && p.child.funding == p && r.child == p.child && p.child.charge == p.charge && p.child.birth == p.birth && p.child.runtime == p.runtime && p.charge != nil && p.birth != nil && p.runtime != nil && c.op.kind == "worker-child-startup-funding"
}

// Caller holds the original parent custody mutex. No actual native/read effect.
func (p *WorkerParentPending) pendingFundingCompletedLocked(debit *WorkerOriginalDebit) bool {
	if p == nil || p.structural == nil {
		return false
	}
	f := p.funding
	return f != nil && f.parent == p && f.child == nil && f.charge == nil && f.birth == nil && f.runtime == nil && f.owner == p.custody && f.reservation == p.structural && p.structural.debit == debit && f.completedLocked()
}

type WorkerStartupFundingView struct {
	self      *WorkerStartupFundingView
	owner     *WorkerStartupFundingPending
	binding   *WorkerServeFundingBinding
	bindTried bool
}

type WorkerServeFundingBinding struct {
	self  *WorkerServeFundingBinding
	view  *WorkerStartupFundingView
	birth *WorkerServeBirthView
}

func (p *WorkerStartupFundingPending) ClaimOriginalServeFunding() (*WorkerStartupFundingView, error) {
	if p == nil || p.self != p || p.owner == nil || p.owner.raw == nil {
		return nil, ErrInvalid
	}
	w := p.owner
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent != nil || p.child == nil || p.child.funding != p || p.child.birth != p.birth || p.birth == nil || p.birth.serve == nil || p.viewTried || !p.completedLocked() || w.raw.active != nil || w.raw.firstError != nil || p.ctx.Err() != nil {
		return nil, ErrInvalid
	}
	p.viewTried = true
	v := &WorkerStartupFundingView{owner: p}
	v.self = v
	p.view = v
	return v, nil
}

// This once-only binding is consumed by the actual WorkerServeIssuer, never an
// old issuer or runtime/controller. It is exact birth identity, not a boolean.
func (v *WorkerStartupFundingView) ClaimOriginalServeBinding(destination **WorkerServeFundingBinding, birth *WorkerServeBirthView) error {
	if destination == nil || *destination != nil || v == nil || v.self != v || v.owner == nil || v.owner.owner == nil || birth == nil || birth.self != birth {
		return ErrInvalid
	}
	p := v.owner
	w := p.owner
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.view != v || !p.viewTried || v.bindTried || p.parent != nil || p.child == nil || p.child.funding != p || p.birth == nil || birth.birth == nil || p.birth != birth.birth || p.birth.serve != birth || !p.completedLocked() || w.raw.active != nil || w.raw.firstError != nil || p.ctx.Err() != nil {
		return ErrInvalid
	}
	v.bindTried = true
	b := &WorkerServeFundingBinding{view: v, birth: birth}
	b.self = b
	v.binding, *destination = b, b
	return nil
}

// Pure exact original identity checking. NativeIssuer calls this BEFORE its
// raw mutex/effect; actual current birth/native checks remain its next stage.
func (b *WorkerServeFundingBinding) RequireOriginalBirth(birth *WorkerServeBirthView) error {
	if b == nil || b.self != b || b.view == nil || b.view.self != b.view || b.birth != birth || birth == nil || birth.self != birth || b.view.owner == nil || b.view.owner.owner == nil {
		return ErrInvalid
	}
	p := b.view.owner
	w := p.owner
	w.raw.mu.Lock()
	valid := b.view.binding == b && b.view.bindTried && p.view == b.view && p.parent == nil && p.child != nil && p.child.funding == p && p.birth != nil && birth.birth != nil && p.birth == birth.birth && p.birth.serve == birth && p.completedLocked() && w.raw.firstError == nil
	w.raw.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	return p.ctx.Err()
}
