package residentservice

import (
	"context"
	"crypto/rand"
	"getarm.org/internal/residentrefs"
	"runtime"
	"strings"
)

type WorkerParentPending struct {
	launcher                                     *residentrefs.WorkerLauncherOriginal
	structural                                   *workerStartupVerifierReservation
	funding                                      *WorkerStartupFundingPending
	fundingTried                                 bool
	self                                         *WorkerParentPending
	custody                                      *workerCustody
	group                                        *WorkerServiceGroup
	openTried, opened, debitTried, debitComplete bool
}

// An original debit has no process, child, charge, birth, closer or release API.
// Its pointer alone does not establish a successful original append or admission.
type WorkerOriginalDebit struct {
	self   *WorkerOriginalDebit
	owner  *workerCustody
	record *WorkerDebitRecord
}

func NewWorkerExistingGrantParentPending(home string) (*WorkerParentPending, error) {
	if !validInputPath(home) {
		return nil, ErrInvalid
	}
	p := &WorkerParentPending{custody: newWorkerCustodyData(home)}
	p.self = p
	p.custody.parent = p
	return p, nil
}

func (p *WorkerParentPending) beginOriginal(kind string) (*operation, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.self != p.custody || p.custody.parent != p || p.custody.operator != nil || p.custody.raw == nil {
		return nil, ErrInvalid
	}
	w := p.custody
	r := w.raw
	r.mu.Lock()
	defer r.mu.Unlock()
	if !w.legacyEmptyLocked() {
		return nil, ErrInvalid
	}
	if r.active != nil {
		return nil, ErrPending
	}
	if r.firstError != nil {
		return nil, r.firstError
	}
	switch kind {
	case "worker-parent-open":
		if p.openTried || p.debitTried {
			return nil, ErrInvalid
		}
		p.openTried = true
	case "worker-parent-debit":
		if !p.opened || p.debitTried {
			return nil, ErrInvalid
		}
		p.debitTried = true
	case "worker-parent-startup-funding":
		if !p.opened || !p.debitComplete || p.funding == nil || p.fundingTried || p.group != nil {
			return nil, ErrInvalid
		}
		p.fundingTried = true
	default:
		return nil, ErrInvalid
	}
	return w.operationLocked(kind)
}

func (p *WorkerParentPending) OpenOriginal(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-parent-open")
	if err != nil {
		return err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.openBodyOriginal(ctx)
	if resultErr == nil {
		p.custody.raw.mu.Lock()
		p.opened = true
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerParentPending) openBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	if p.launcher == nil || p.launcher.RequireOriginalStandaloneStart() != nil {
		return ErrInvalid
	}
	w := p.custody
	n := w.raw.files
	if err := n.openExistingAuthorized(w.path); err != nil {
		return err
	}
	journal, err := w.readJournalOriginal(ctx)
	if err != nil {
		return err
	}
	if len(journal.records) != 1 {
		return ErrUnresolved
	}
	grant := journal.records[0].Grant
	if grant == nil || grant.Intent.Home != w.path || grant.Intent.HomeID != n.home.id || grant.Intent.Profile.Validate() != nil {
		return ErrInvalid
	}
	w.raw.mu.Lock()
	w.journal, w.grant = journal, grant
	w.raw.mu.Unlock()
	if err := p.prepayStructuralOriginal(ctx); err != nil {
		return err
	}
	n.progress("worker_parent_runtime_image_capture_pending")
	image, err := w.captureRuntimeImageOriginal(ctx)
	if err != nil {
		return err
	}
	n.progress("worker_parent_runtime_image_proof_pending")
	proof, err := image.proofOriginal(ctx)
	if err != nil {
		return err
	}
	build := Build{SHA256: proof.SHA256, Size: proof.Size, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoBuild: runtime.Version(), Protocol: 1}
	if build != grant.Intent.Profile.Core.Build {
		return ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return n.validateCurrent(w.path)
}

func (p *WorkerParentPending) PrepareOriginalDebit(ctx context.Context, key string) (debit *WorkerOriginalDebit, resultErr error) {
	op, err := p.beginOriginal("worker-parent-debit")
	if err != nil {
		return nil, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	w := p.custody
	debit = &WorkerOriginalDebit{owner: w, record: &WorkerDebitRecord{LaunchKey: key}}
	debit.self = debit
	w.raw.mu.Lock()
	w.debit = debit
	w.raw.mu.Unlock()
	resultErr = p.prepareBodyOriginal(ctx, debit)
	if resultErr == nil {
		w.raw.mu.Lock()
		p.debitComplete = true
		w.raw.mu.Unlock()
	}
	normal = true
	return debit, resultErr
}

func (p *WorkerParentPending) prepareBodyOriginal(ctx context.Context, debit *WorkerOriginalDebit) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.custody
	n := w.raw.files
	if debit == nil || debit.self != debit || w.debit != debit || debit.owner != w || debit.record == nil || !boundedText(debit.record.LaunchKey, 128) || w.grant == nil || w.image == nil {
		return ErrInvalid
	}
	if err := n.validateCurrent(w.path); err != nil {
		return err
	}
	current, err := w.readJournalOriginal(ctx)
	if err != nil {
		return err
	}
	if len(current.records) != 1 || current.bytes != w.journal.bytes || current.last != w.journal.last {
		return ErrUnresolved
	}
	n.progress("worker_parent_predebit_image_proof_pending")
	proof, err := w.image.proofOriginal(ctx)
	if err != nil {
		return err
	}
	if proof.SHA256 != w.grant.Intent.Profile.Core.Build.SHA256 || proof.Size != w.grant.Intent.Profile.Core.Build.Size {
		return ErrInvalid
	}
	profileSHA, err := w.grant.Intent.Profile.SHA256()
	if err != nil {
		return err
	}
	contractSHA, err := w.grant.Intent.Profile.Contract.SHA256()
	if err != nil {
		return err
	}
	r := debit.record
	w.raw.mu.Lock()
	*r = WorkerDebitRecord{ID: strings.Repeat("0", 64), LaunchKey: r.LaunchKey, GrantID: w.grant.ID, GrantRevision: w.grant.Revision, ProfileSHA256: profileSHA, ContractSHA256: contractSHA, InitialPackageSHA256: w.grant.Intent.Profile.Contract.InitialPackageSHA256, InitialManifestSHA256: w.grant.Intent.Profile.Contract.InitialManifestSHA256, SecretCommitment: strings.Repeat("0", 64), LockID: n.lock.id, JournalID: n.journal.id}
	w.raw.mu.Unlock()
	// Full framed candidate is checked before entropy or durable debit effects.
	next := WorkerRecordData{Schema: 3, Sequence: 2, Kind: "worker-debit-prepared", Debit: r}
	if _, err = encodeWorkerJournalData([]WorkerRecordData{w.journal.records[0], next}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	n.progress("worker_parent_debit_secret_pending")
	if _, err = rand.Read(w.secret[:]); err != nil {
		return err
	}
	id := digest(append([]byte("resident-worker-debit-v1:"+r.LaunchKey+":"), w.secret[:]...))
	commitment := digest(w.secret[:])
	w.raw.mu.Lock()
	r.ID, r.SecretCommitment = id, commitment
	w.raw.mu.Unlock()
	if err = w.appendJournalOriginal(ctx, next); err != nil {
		return err
	}
	if err = n.validateCurrent(w.path); err != nil {
		return err
	}
	return p.bindStructuralDebitOriginal(debit)
}

type WorkerDebitObservation struct {
	ID                   string `json:"id"`
	LaunchKey            string `json:"launch_key"`
	OriginalSyncReturned bool   `json:"original_sync_returned"`
	OutcomeUnknown       bool   `json:"outcome_unknown"`
	NativeOutcome        string `json:"native_outcome"`
}

func (p *WorkerParentPending) LookupOriginalDebit(debit *WorkerOriginalDebit) (WorkerDebitObservation, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.self != p.custody || p.custody.parent != p || p.custody.raw == nil {
		return WorkerDebitObservation{}, ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if !w.legacyEmptyLocked() || debit == nil || debit.self != debit || debit.owner != w || w.debit != debit || debit.record == nil {
		return WorkerDebitObservation{}, ErrInvalid
	}
	syncReturned := w.append != nil && w.append.owner == w && w.append.committed && len(w.journal.records) == 2 && w.journal.records[1].Debit == debit.record
	return WorkerDebitObservation{ID: debit.record.ID, LaunchKey: debit.record.LaunchKey, OriginalSyncReturned: syncReturned, OutcomeUnknown: w.raw.active != nil || w.raw.firstError != nil || !p.debitComplete || !syncReturned, NativeOutcome: "unknown"}, nil
}

func (p *WorkerParentPending) ObserveOriginal() WorkerCustodyObservation {
	if p == nil || p.self != p || p.custody == nil || p.custody.parent != p {
		return WorkerCustodyObservation{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	}
	return p.custody.observeData()
}
