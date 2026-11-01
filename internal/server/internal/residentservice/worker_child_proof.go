package residentservice

import (
	"context"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

type workerChildProofSignCall struct {
	child                                                            *WorkerChildPending
	op                                                               *operation
	ctx                                                              context.Context
	observation                                                      *WorkerCurrentServiceObservation
	view                                                             *WorkerCurrentChallengeView
	charge                                                           *WorkerManagerCharge
	prepared                                                         *workerPreparedServiceCall
	rootBound                                                        *workerChildRootBoundCall
	ack                                                              *workerChildAckCall
	auth                                                             *workerChildChallengeAuthCall
	current                                                          *workerChildCurrentCall
	context                                                          residentprotocol.WorkerContextData
	nonce                                                            [32]byte
	phases                                                           [3]workerChildCurrentPhase
	phaseCalls                                                       int
	payload, frame                                                   []byte
	payloadAttempted, payloadReturned, frameAttempted, frameReturned bool
	payloadError, frameError                                         error
	normalReturned                                                   bool
	resultError                                                      error
}

func (p *WorkerChildPending) workerProofCallLocked(c *workerChildProofSignCall) bool {
	return c != nil && c == p.proofSign && c.child == p && c.op != nil && c.op.owner == p.custody.raw && c.op.kind == "worker-child-proof" && !c.op.abnormal && p.custody.raw.active == c.op && sameWorkerContextData(c.ctx, p.rootContext) && c.prepared == p.preparedValidation && c.rootBound == p.rootBound && c.ack == p.ackSign && c.auth == p.challengeAuth && c.current == p.currentValidation && c.observation != nil && c.observation.completedDataLocked(p, c.view, c.charge) && p.currentWorkerViewLocked(c.view, c.charge) && c.context == c.view.body.Context && c.context == c.current.data.Context && c.nonce == c.view.nonce
}
func (p *WorkerChildPending) checkWorkerProofPhase(c *workerChildProofSignCall) error {
	w := p.custody
	w.raw.mu.Lock()
	if !p.workerProofCallLocked(c) || c.phaseCalls >= len(c.phases) {
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
	if !p.workerProofCallLocked(c) {
		return ErrInvalid
	}
	return err
}

// The Root additionally checks its actual current DB/Manager/Server/fence and
// fresh canonical commitment before this signer and again before Wproof. This
// signer accepts only its genuine current-service observation and nonce view,
// never a supplied current DTO, success bit or lifecycle callback.
func (p *WorkerChildPending) SignWorkerCurrentProofOriginal(ctx context.Context, observation *WorkerCurrentServiceObservation, view *WorkerCurrentChallengeView, charge *WorkerManagerCharge) (frame []byte, resultErr error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || observation == nil || view == nil || charge == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return nil, ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-proof")
	if err != nil {
		return nil, err
	}
	w := p.custody
	c := &workerChildProofSignCall{child: p, op: op, ctx: ctx, observation: observation, view: view, charge: charge}
	w.raw.mu.Lock()
	c.prepared = p.preparedValidation
	c.rootBound = p.rootBound
	c.ack = p.ackSign
	c.auth = p.challengeAuth
	c.current = p.currentValidation
	c.context = view.body.Context
	c.nonce = view.nonce
	p.proofSign = c
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		c.normalReturned = normal && op.normalFinished && !op.abnormal
		c.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.checkWorkerProofPhase(c)
	if resultErr == nil {
		c.payloadAttempted = true
		c.payload, c.payloadError = residentprotocol.EncodeWorkerCurrentProofPayloadData(w.secret, c.context, c.nonce)
		c.payloadReturned = true
		resultErr = c.payloadError
	}
	if resultErr == nil {
		resultErr = p.checkWorkerProofPhase(c)
	}
	if resultErr == nil {
		c.frameAttempted = true
		c.frame, c.frameError = residentrefs.EncodeFrameData(c.payload)
		c.frameReturned = true
		resultErr = c.frameError
	}
	if resultErr == nil {
		resultErr = p.checkWorkerProofPhase(c)
	}
	if resultErr == nil {
		frame = c.frame
	}
	normal = true
	return frame, resultErr
}
