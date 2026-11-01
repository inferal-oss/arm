package residentservice

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
)

// DATA from the actual original prepared call; no current/readiness capability.
type WorkerPreparedServiceData struct {
	Home                                              string
	HomeID                                            NativeID
	DebitID, LaunchKey, ProfileSHA256, ContractSHA256 string
	InitialPackageSHA256, InitialManifestSHA256       string
	EnvironmentSHA256                                 string
	Build                                             Build
	Resources                                         Resources
	Identities                                        residentrefs.ServiceIdentities
	BirthJournalBytes                                 int64
	BirthJournalSHA256                                string
}

type workerPreparedServiceCall struct {
	child                *WorkerChildPending
	op                   *operation
	ctx                  context.Context
	custody              *residentrefs.ServiceOriginal
	runtime              *mesh.WorkerRuntimeView
	charge               *WorkerManagerCharge
	birth                *WorkerBirthBinding
	serve                *WorkerServeBirthView
	read                 *workerNativeRead
	current              residentrefs.ServiceIdentities
	currentJournal       os.FileInfo
	environment          []string
	environmentCanonical []byte
	proof                mesh.WorkerRuntimeProofData
	data                 WorkerPreparedServiceData
	normalReturned       bool
	resultError          error
}

func (p *WorkerChildPending) preparedOriginalLocked(c *workerPreparedServiceCall) bool {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || c == nil || c != p.preparedValidation || c.child != p || c.op == nil || c.op.owner != p.custody.raw || c.op.kind != "worker-child-prepared" || c.op.abnormal || !p.currentShapeLocked() {
		return false
	}
	w := p.custody
	return c.custody == p.source && c.runtime == p.runtime && c.charge == p.charge && c.charge != nil && c.charge.self == c.charge && c.charge.owner == w && c.charge.child == p && c.birth != nil && c.birth == p.birth && c.birth.self == c.birth && c.birth.child == p && c.birth.complete && c.birth.charge == c.charge && c.birth.serveTried && c.serve != nil && c.birth.serve == c.serve && c.serve.self == c.serve && c.serve.birth == c.birth && sameWorkerContextData(c.ctx, p.rootContext) && len(w.reads) == 1 && c.read == w.reads[0] && c.read != nil && c.read.owner == w && c.read.kind == "worker-child-receive" && c.read.finished && c.read.firstError == nil && c.read.after != nil && w.append == nil && len(w.journal.records) == 2
}

func (p *WorkerChildPending) ValidateWorkerPreparedOriginal(ctx context.Context, custody *residentrefs.ServiceOriginal, image *mesh.WorkerRuntimeView, charge *WorkerManagerCharge, birth *WorkerBirthBinding, serve *WorkerServeBirthView) (data WorkerPreparedServiceData, resultErr error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return data, ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-prepared")
	if err != nil {
		return data, err
	}
	w := p.custody
	c := &workerPreparedServiceCall{child: p, op: op, ctx: ctx, custody: custody, runtime: image, charge: charge, birth: birth, serve: serve}
	w.raw.mu.Lock()
	p.preparedValidation = c
	if len(w.reads) == 1 {
		c.read = w.reads[0]
	}
	valid := p.preparedOriginalLocked(c)
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		c.normalReturned = normal && op.normalFinished && !op.abnormal
		c.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	if !valid {
		resultErr = ErrInvalid
	} else {
		resultErr = p.validateWorkerPreparedBody(c)
	}
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}

func (p *WorkerChildPending) validateWorkerPreparedBody(c *workerPreparedServiceCall) error {
	w := p.custody
	if err := p.currentBodyOriginal(c.ctx, true); err != nil {
		return err
	}
	var err error
	c.current, err = c.custody.ValidateOriginal(c.ctx)
	if err != nil {
		return err
	}
	if c.current != p.identities {
		return ErrInvalid
	}
	c.currentJournal, err = p.journal.StatOriginal(c.ctx)
	if err != nil {
		return err
	}
	before := c.read.after
	if !os.SameFile(before, c.currentJournal) || before.Size() != c.currentJournal.Size() || !before.ModTime().Equal(c.currentJournal.ModTime()) || c.currentJournal.Size() != w.journal.bytes {
		return ErrUnresolved
	}
	c.environment = os.Environ()
	if err = w.grant.Intent.Profile.Core.RuntimeEnvironment.MatchesActual(c.environment); err != nil {
		return err
	}
	c.environmentCanonical, err = json.Marshal(w.grant.Intent.Profile.Core.RuntimeEnvironment)
	if err != nil || len(c.environmentCanonical) > 8<<10 {
		return ErrInvalid
	}
	c.proof, err = c.runtime.ProofOriginal(c.ctx, c.custody, w.path, w.debit.record.ID)
	if err != nil {
		return err
	}
	build := w.grant.Intent.Profile.Core.Build
	if c.proof.SHA256 != build.SHA256 || c.proof.Size != build.Size || build.GOOS != runtime.GOOS || build.GOARCH != runtime.GOARCH || build.GoBuild != runtime.Version() {
		return ErrInvalid
	}
	profileSHA, err := w.grant.Intent.Profile.SHA256()
	if err != nil || profileSHA != w.debit.record.ProfileSHA256 {
		return ErrInvalid
	}
	contractSHA, err := w.grant.Intent.Profile.Contract.SHA256()
	if err != nil || contractSHA != w.debit.record.ContractSHA256 {
		return ErrInvalid
	}
	if err = c.ctx.Err(); err != nil {
		return err
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if w.raw.active != c.op || !p.preparedOriginalLocked(c) {
		return ErrInvalid
	}
	c.data = WorkerPreparedServiceData{Home: w.path, HomeID: w.grant.Intent.HomeID, DebitID: w.debit.record.ID, LaunchKey: w.debit.record.LaunchKey, ProfileSHA256: profileSHA, ContractSHA256: contractSHA, InitialPackageSHA256: w.debit.record.InitialPackageSHA256, InitialManifestSHA256: w.debit.record.InitialManifestSHA256, EnvironmentSHA256: digest(c.environmentCanonical), Build: build, Resources: w.grant.Intent.Profile.Core.Resources, Identities: c.current, BirthJournalBytes: w.journal.bytes, BirthJournalSHA256: hex.EncodeToString(w.journal.last[:])}
	return nil
}
