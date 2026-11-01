package residentservice

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

type WorkerCurrentServiceData struct {
	Prepared  WorkerPreparedServiceData
	RootBound WorkerRootBoundData
	Context   residentprotocol.WorkerContextData
}
type workerChildCurrentPhase struct {
	attempted, returned bool
	err                 error
}
type workerChildCurrentRead struct {
	before, after                                                  os.FileInfo
	beforeAttempted, beforeReturned, afterAttempted, afterReturned bool
	beforeError, afterError                                        error
	data                                                           []byte
	reads                                                          [32]workerChildRootIO
	calls, bytes                                                   int
	decoded                                                        workerJournalData
	verified                                                       bool
}
type workerChildCurrentCall struct {
	child                                     *WorkerChildPending
	op                                        *operation
	ctx                                       context.Context
	view                                      *WorkerCurrentChallengeView
	charge                                    *WorkerManagerCharge
	prepared                                  *workerPreparedServiceCall
	rootBound                                 *workerChildRootBoundCall
	auth                                      *workerChildChallengeAuthCall
	observation                               *WorkerCurrentServiceObservation
	phases                                    [8]workerChildCurrentPhase
	phaseCalls                                int
	identities                                residentrefs.ServiceIdentities
	identityAttempted, identityReturned       bool
	identityError                             error
	read                                      workerChildCurrentRead
	environment                               []string
	environmentCanonical                      []byte
	environmentAttempted, environmentReturned bool
	proof                                     mesh.WorkerRuntimeProofData
	imageAttempted, imageReturned             bool
	imageError                                error
	data                                      WorkerCurrentServiceData
	normalReturned                            bool
	resultError                               error
}

// The original current call retains this before its first phase/native effect.
// The observation has no DATA constructor, closer or native readiness authority.
type WorkerCurrentServiceObservation struct {
	self   *WorkerCurrentServiceObservation
	child  *WorkerChildPending
	call   *workerChildCurrentCall
	view   *WorkerCurrentChallengeView
	charge *WorkerManagerCharge
}

func (WorkerCurrentServiceObservation) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*WorkerCurrentServiceObservation) UnmarshalJSON([]byte) error  { return ErrInvalid }

func (p *WorkerChildPending) currentWorkerCallLocked(c *workerChildCurrentCall) bool {
	return c != nil && c == p.currentValidation && c.child == p && c.op != nil && c.op.owner == p.custody.raw && c.op.kind == "worker-child-current" && !c.op.abnormal && p.custody.raw.active == c.op && sameWorkerContextData(c.ctx, p.rootContext) && c.prepared == p.preparedValidation && c.rootBound == p.rootBound && c.auth == p.challengeAuth && c.observation != nil && c.observation == p.currentObservation && c.observation.self == c.observation && c.observation.child == p && c.observation.call == c && c.observation.view == c.view && c.observation.charge == c.charge && p.currentWorkerViewLocked(c.view, c.charge)
}
func (p *WorkerChildPending) checkWorkerCurrentPhase(c *workerChildCurrentCall) error {
	w := p.custody
	w.raw.mu.Lock()
	if !p.currentWorkerCallLocked(c) || c.phaseCalls >= len(c.phases) {
		w.raw.mu.Unlock()
		return ErrInvalid
	}
	ph := &c.phases[c.phaseCalls]
	c.phaseCalls++
	ph.attempted = true
	w.raw.mu.Unlock()
	err := c.ctx.Err()
	if err == nil {
		err = p.source.ValidateOriginalWorkerChallengePhase(c.view.read)
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	ph.returned, ph.err = true, err
	if !p.currentWorkerCallLocked(c) {
		return ErrInvalid
	}
	return err
}

func (p *WorkerChildPending) ValidateWorkerCurrentOriginal(destination **WorkerCurrentServiceObservation, ctx context.Context, view *WorkerCurrentChallengeView, charge *WorkerManagerCharge) (data WorkerCurrentServiceData, resultErr error) {
	if destination == nil || *destination != nil || p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || view == nil || charge == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return data, ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-current")
	if err != nil {
		return data, err
	}
	w := p.custody
	c := &workerChildCurrentCall{child: p, op: op, ctx: ctx, view: view, charge: charge}
	v := &WorkerCurrentServiceObservation{child: p, call: c, view: view, charge: charge}
	v.self = v
	c.observation = v
	w.raw.mu.Lock()
	c.prepared = p.preparedValidation
	c.rootBound = p.rootBound
	c.auth = p.challengeAuth
	p.currentValidation = c
	p.currentObservation = v
	*destination = v
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		c.normalReturned = normal && op.normalFinished && !op.abnormal
		c.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.validateWorkerCurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}

func (p *WorkerChildPending) validateWorkerCurrentBody(c *workerChildCurrentCall) error {
	if err := p.checkWorkerCurrentPhase(c); err != nil {
		return err
	}
	c.identityAttempted = true
	c.identities, c.identityError = p.source.ValidateOriginal(c.ctx)
	c.identityReturned = true
	if c.identityError != nil {
		return c.identityError
	}
	if c.identities != p.identities || c.identities != c.prepared.data.Identities {
		return ErrInvalid
	}
	if err := p.checkWorkerCurrentPhase(c); err != nil {
		return err
	}
	if err := p.readWorkerCurrentService(c); err != nil {
		return err
	}
	if err := p.checkWorkerCurrentPhase(c); err != nil {
		return err
	}
	w := p.custody
	c.environmentAttempted = true
	c.environment = os.Environ()
	c.environmentReturned = true
	if err := w.grant.Intent.Profile.Core.RuntimeEnvironment.MatchesActual(c.environment); err != nil {
		return err
	}
	var err error
	c.environmentCanonical, err = json.Marshal(w.grant.Intent.Profile.Core.RuntimeEnvironment)
	if err != nil || len(c.environmentCanonical) > 8<<10 || digest(c.environmentCanonical) != c.prepared.data.EnvironmentSHA256 {
		return ErrInvalid
	}
	if err = p.checkWorkerCurrentPhase(c); err != nil {
		return err
	}
	c.imageAttempted = true
	c.proof, c.imageError = p.runtime.ProofOriginal(c.ctx, p.source, w.path, w.debit.record.ID)
	c.imageReturned = true
	if c.imageError != nil {
		return c.imageError
	}
	if c.proof.SHA256 != c.prepared.data.Build.SHA256 || c.proof.Size != c.prepared.data.Build.Size {
		return ErrInvalid
	}
	profileSHA, err := w.grant.Intent.Profile.SHA256()
	if err != nil || profileSHA != c.prepared.data.ProfileSHA256 {
		return ErrInvalid
	}
	contractSHA, err := w.grant.Intent.Profile.Contract.SHA256()
	if err != nil || contractSHA != c.prepared.data.ContractSHA256 {
		return ErrInvalid
	}
	if err = p.checkWorkerCurrentPhase(c); err != nil {
		return err
	}
	c.data = WorkerCurrentServiceData{Prepared: c.prepared.data, RootBound: c.rootBound.data, Context: c.view.body.Context}
	return c.ctx.Err()
}

func (p *WorkerChildPending) readWorkerCurrentService(c *workerChildCurrentCall) error {
	r := &c.read
	r.beforeAttempted = true
	r.before, r.beforeError = p.journal.StatOriginal(c.ctx)
	r.beforeReturned = true
	if r.beforeError != nil {
		return r.beforeError
	}
	if r.before.Size() != c.rootBound.data.JournalBytes || r.before.Size() <= 0 || r.before.Size() > workerJournalMaxBytes {
		return ErrUnresolved
	}
	r.data = make([]byte, int(r.before.Size()))
	for r.bytes < len(r.data) {
		if err := p.checkWorkerCurrentPhaseForRead(c); err != nil {
			return err
		}
		if r.calls >= len(r.reads) {
			return io.ErrNoProgress
		}
		call := &r.reads[r.calls]
		r.calls++
		call.offset, call.requested = int64(r.bytes), len(r.data)-r.bytes
		call.attempted = true
		call.returned, call.err = p.journal.ReadAtOriginal(c.ctx, r.data[r.bytes:], call.offset)
		call.normalReturned = true
		if call.returned < 0 || call.returned > call.requested {
			return ErrInvalid
		}
		r.bytes += call.returned
		if call.err != nil && !(errors.Is(call.err, io.EOF) && r.bytes == len(r.data)) {
			return call.err
		}
		if call.returned == 0 {
			return io.ErrNoProgress
		}
	}
	r.afterAttempted = true
	r.after, r.afterError = p.journal.StatOriginal(c.ctx)
	r.afterReturned = true
	if r.afterError != nil {
		return r.afterError
	}
	if !os.SameFile(r.before, r.after) || r.before.Size() != r.after.Size() || !r.before.ModTime().Equal(r.after.ModTime()) {
		return ErrUnresolved
	}
	birth := c.prepared.read
	if birth == nil || int64(len(birth.data)) != c.prepared.data.BirthJournalBytes || len(birth.data) >= len(r.data) || !bytes.Equal(r.data[:len(birth.data)], birth.data) || !bytes.Equal(r.data[len(birth.data):], c.rootBound.frame) {
		return ErrInvalid
	}
	var err error
	r.decoded, err = decodeWorkerJournalData(r.data)
	if err != nil || len(r.decoded.records) != 3 || r.decoded.records[2].Root == nil || *r.decoded.records[2].Root != c.rootBound.record || r.decoded.bytes != c.rootBound.data.JournalBytes || hex.EncodeToString(r.decoded.last[:]) != c.rootBound.data.JournalSHA256 {
		return ErrInvalid
	}
	r.verified = true
	return nil
}

// Read loops use the same typed original phase without allocating more retained
// phase slots. This performs no nested child operation or generic callback.
func (p *WorkerChildPending) checkWorkerCurrentPhaseForRead(c *workerChildCurrentCall) error {
	w := p.custody
	w.raw.mu.Lock()
	valid := p.currentWorkerCallLocked(c)
	w.raw.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	return p.source.ValidateOriginalWorkerChallengePhase(c.view.read)
}

func (v *WorkerCurrentServiceObservation) completedDataLocked(p *WorkerChildPending, view *WorkerCurrentChallengeView, charge *WorkerManagerCharge) bool {
	if v == nil || v.self != v || p == nil || v.child != p || v.view != view || v.charge != charge || v.call == nil || v.call != p.currentValidation || v != p.currentObservation {
		return false
	}
	c := v.call
	if c.child != p || c.op == nil || c.op.owner != p.custody.raw || c.op.kind != "worker-child-current" || !c.op.normalFinished || c.op.abnormal || !c.normalReturned || c.resultError != nil || c.view != view || c.charge != charge || c.observation != v || c.prepared != p.preparedValidation || c.rootBound != p.rootBound || c.auth != p.challengeAuth || !c.read.verified || c.phaseCalls != 5 || !p.currentWorkerViewLocked(view, charge) {
		return false
	}
	for i := 0; i < c.phaseCalls; i++ {
		if !c.phases[i].attempted || !c.phases[i].returned || c.phases[i].err != nil {
			return false
		}
	}
	return c.data.Prepared == c.prepared.data && c.data.RootBound == c.rootBound.data && c.data.Context == view.body.Context
}

func (v *WorkerCurrentServiceObservation) ValidateOriginalForFence(p *WorkerChildPending, fence *residentrefs.FenceOriginal, view *WorkerCurrentChallengeView, charge *WorkerManagerCharge) (WorkerCurrentServiceData, error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || fence == nil {
		return WorkerCurrentServiceData{}, ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	valid := w.raw.active == nil && w.raw.firstError == nil && v.completedDataLocked(p, view, charge)
	var data WorkerCurrentServiceData
	if valid {
		data = v.call.data
	}
	w.raw.mu.Unlock()
	if !valid {
		return data, ErrInvalid
	}
	if _, err := view.ValidateOriginal(charge); err != nil {
		return data, err
	}
	if err := p.source.ValidateOriginalFenceLink(fence); err != nil {
		return data, err
	}
	w.raw.mu.Lock()
	valid = w.raw.active == nil && w.raw.firstError == nil && v.completedDataLocked(p, view, charge) && v.call.data == data
	w.raw.mu.Unlock()
	if !valid {
		return WorkerCurrentServiceData{}, ErrInvalid
	}
	return data, nil
}
