package residentservice

import (
	"context"
	"encoding/hex"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

type workerChildAckCall struct {
	child          *WorkerChildPending
	op             *operation
	ctx            context.Context
	rootBound      *workerChildRootBoundCall
	context        residentprotocol.WorkerContextData
	payload, frame []byte
	normalReturned bool
	resultError    error
}
type workerChildChallengeAuthCall struct {
	child          *WorkerChildPending
	op             *operation
	ctx            context.Context
	ack            *workerChildAckCall
	read           *residentrefs.WorkerChallengeRead
	charge         *WorkerManagerCharge
	payload        []byte
	body           residentprotocol.WorkerCurrentChallengeBodyData
	view           *WorkerCurrentChallengeView
	normalReturned bool
	resultError    error
}

// Issued only by this original child's actual authenticated challenge read.
// No DATA, constructor, old challenge or foreign registry can supply this view.
type WorkerCurrentChallengeView struct {
	self   *WorkerCurrentChallengeView
	child  *WorkerChildPending
	issue  *workerChildChallengeAuthCall
	charge *WorkerManagerCharge
	read   *residentrefs.WorkerChallengeRead
	body   residentprotocol.WorkerCurrentChallengeBodyData
	nonce  [32]byte
}

func (WorkerCurrentChallengeView) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*WorkerCurrentChallengeView) UnmarshalJSON([]byte) error  { return ErrInvalid }

// Pure original-owner consistency; Root additionally binds its actual footer
// receipt and freshly rebuilt canonical prepared commitment before Wack/Wproof.
func (p *WorkerChildPending) ownWorkerAckContextLocked(c residentprotocol.WorkerContextData) bool {
	if c.Validate() != nil || !p.completedWorkerRootBoundLocked() {
		return false
	}
	b := p.rootBound
	prepared := b.prepared.data
	intent := p.birth.intent
	key, err := WorkerLaunchKeyCommitmentData(prepared.LaunchKey)
	return err == nil && c.Generation == b.record.Generation && c.DebitID == prepared.DebitID && c.LaunchKeySHA256 == key && c.ProfileSHA256 == prepared.ProfileSHA256 && c.ContractSHA256 == prepared.ContractSHA256 && c.InitialPackageSHA256 == prepared.InitialPackageSHA256 && c.InitialManifestSHA256 == prepared.InitialManifestSHA256 && c.ImageSHA256 == prepared.Build.SHA256 && c.ImageBytes == prepared.Build.Size && c.EnvironmentSHA256 == prepared.EnvironmentSHA256 && c.SettingsSHA256 == intent.SettingsSHA256 && c.IntentSHA256 == p.birth.intentSHA256 && c.PreparedCommitmentSHA256 == b.record.CommitmentSHA256 && c.ServiceJournalBytes == b.data.JournalBytes && c.ServiceJournalSHA256 == b.data.JournalSHA256
}

func (p *WorkerChildPending) SignWorkerBoundAckOriginal(ctx context.Context, c residentprotocol.WorkerContextData) (frame []byte, resultErr error) {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return nil, ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-ack")
	if err != nil {
		return nil, err
	}
	w := p.custody
	call := &workerChildAckCall{child: p, op: op, ctx: ctx, context: c}
	w.raw.mu.Lock()
	call.rootBound = p.rootBound
	p.ackSign = call
	valid := p.ownWorkerAckContextLocked(c)
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		call.normalReturned = normal && op.normalFinished && !op.abnormal
		call.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	if !valid {
		resultErr = ErrInvalid
	} else {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		call.payload, resultErr = residentprotocol.EncodeWorkerBoundAckPayloadData(w.secret, c)
	}
	if resultErr == nil {
		call.frame, resultErr = residentrefs.EncodeFrameData(call.payload)
	}
	if resultErr == nil {
		frame = call.frame
	}
	normal = true
	return frame, resultErr
}

func (p *WorkerChildPending) completedWorkerAckLocked() bool {
	if p == nil || p.custody == nil || p.custody.raw == nil {
		return false
	}
	a := p.ackSign
	return a != nil && a.child == p && a.op != nil && a.op.owner == p.custody.raw && a.op.kind == "worker-child-ack" && a.op.normalFinished && !a.op.abnormal && a.normalReturned && a.resultError == nil && sameWorkerContextData(a.ctx, p.rootContext) && a.rootBound == p.rootBound && len(a.frame) > 36 && len(a.frame) <= residentrefs.MaxWireBytes && p.ownWorkerAckContextLocked(a.context)
}

func (p *WorkerChildPending) AuthenticateWorkerCurrentChallengeOriginal(destination **WorkerCurrentChallengeView, ctx context.Context, read *residentrefs.WorkerChallengeRead, charge *WorkerManagerCharge) (resultErr error) {
	if destination == nil || *destination != nil || read == nil || charge == nil || p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || !sameWorkerContextData(ctx, p.rootContext) {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-child-challenge-auth")
	if err != nil {
		return err
	}
	w := p.custody
	call := &workerChildChallengeAuthCall{child: p, op: op, ctx: ctx, read: read, charge: charge}
	v := &WorkerCurrentChallengeView{child: p, issue: call, read: read, charge: charge}
	v.self = v
	call.view = v
	w.raw.mu.Lock()
	call.ack = p.ackSign
	p.challengeAuth = call
	p.challengeView = v
	*destination = v
	valid := p.completedWorkerAckLocked() && charge == p.charge && charge.self == charge && charge.child == p && charge.owner == w
	w.raw.mu.Unlock()
	normal := false
	defer func() {
		w.raw.mu.Lock()
		call.normalReturned = normal && op.normalFinished && !op.abnormal
		call.resultError = resultErr
		w.raw.mu.Unlock()
	}()
	defer w.raw.finish(op, &normal, &resultErr)
	if !valid {
		resultErr = ErrInvalid
	} else {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		call.payload, resultErr = p.source.OriginalWorkerChallengePayloadData(read, call.ack.frame)
	}
	if resultErr == nil {
		call.body, resultErr = residentprotocol.DecodeWorkerCurrentChallengePayloadData(w.secret, call.payload)
	}
	if resultErr == nil && call.body.Context != call.ack.context {
		resultErr = ErrInvalid
	}
	if resultErr == nil {
		var decoded []byte
		decoded, resultErr = hex.DecodeString(call.body.Nonce)
		if resultErr == nil && (len(decoded) != 32 || hex.EncodeToString(decoded) != call.body.Nonce) {
			resultErr = ErrInvalid
		}
		if resultErr == nil {
			copy(v.nonce[:], decoded)
			v.body = call.body
			resultErr = p.source.ValidateOriginalWorkerChallengePhase(read)
		}
	}
	normal = true
	return resultErr
}

// Local metadata only, under this same original child's raw publication mutex.
// Native phase checks are always outside the mutex, never a nested begin call.
func (p *WorkerChildPending) currentWorkerViewLocked(v *WorkerCurrentChallengeView, charge *WorkerManagerCharge) bool {
	if v == nil || v.self != v || v.child != p || charge == nil || charge != p.charge || v.charge != charge || charge.self != charge || charge.child != p || charge.owner != p.custody || !p.completedWorkerAckLocked() {
		return false
	}
	c := v.issue
	return p.challengeView == v && c != nil && c == p.challengeAuth && c.child == p && c.op != nil && c.op.owner == p.custody.raw && c.op.kind == "worker-child-challenge-auth" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && sameWorkerContextData(c.ctx, p.rootContext) && c.view == v && c.charge == charge && c.read == v.read && c.read != nil && c.ack == p.ackSign && c.body == v.body && v.body.Context == c.ack.context && residentprotocol.MatchNonceData(v.body.Nonce, v.nonce) == nil
}

func (v *WorkerCurrentChallengeView) ValidateOriginal(charge *WorkerManagerCharge) (residentprotocol.WorkerCurrentChallengeBodyData, error) {
	if v == nil || v.self != v || v.child == nil || v.child.self != v.child || v.child.custody == nil || v.child.custody.raw == nil {
		return residentprotocol.WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	p := v.child
	w := p.custody
	w.raw.mu.Lock()
	valid := w.raw.active == nil && w.raw.firstError == nil && p.currentWorkerViewLocked(v, charge)
	ctx := p.rootContext
	body := v.body
	w.raw.mu.Unlock()
	if !valid {
		return residentprotocol.WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return residentprotocol.WorkerCurrentChallengeBodyData{}, err
	}
	if err := p.source.ValidateOriginalWorkerChallengePhase(v.read); err != nil {
		return residentprotocol.WorkerCurrentChallengeBodyData{}, err
	}
	w.raw.mu.Lock()
	valid = w.raw.active == nil && w.raw.firstError == nil && p.currentWorkerViewLocked(v, charge) && v.body == body
	w.raw.mu.Unlock()
	if !valid {
		return residentprotocol.WorkerCurrentChallengeBodyData{}, ErrInvalid
	}
	return body, nil
}
