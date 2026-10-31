package residentservice

import (
	"context"
	"reflect"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

type p1CurrentProofPhase struct {
	attempted, returned             bool
	contextError                    error
	censusAttempted, censusReturned bool
	censusError                     error
}

type p1CurrentProofSignCall struct {
	owner                                                            *owner
	op                                                               *operation
	ctx                                                              context.Context
	child                                                            *ChildPending
	observation                                                      *P1CurrentServiceObservation
	current                                                          *p1CurrentServiceCall
	view                                                             *CurrentChallengeView
	charge                                                           *ManagerCharge
	prepared                                                         *p1PreparedServiceCall
	rootBound                                                        *p1RootBoundCall
	auth                                                             *p1ChallengeAuthCall
	ack                                                              *p1BoundAckSignCall
	birth                                                            *readOperation
	image                                                            *mesh.RuntimeExecutable
	custody                                                          *residentrefs.ServiceOriginal
	read                                                             *residentrefs.P1ChallengeRead
	context                                                          residentprotocol.ContextData
	nonce                                                            [32]byte
	phases                                                           [3]p1CurrentProofPhase
	phaseCalls                                                       int
	payload, frame, output                                           []byte
	payloadAttempted, payloadReturned, frameAttempted, frameReturned bool
	payloadError, frameError                                         error
	normalReturned                                                   bool
	resultError                                                      error
}

// A context is supplied structurally by the future private Root. Require the
// SAME actual completed current call's context; do not replace it with a read2
// IO scope. Noncomparable custom context values refuse without invoking Done or
// another callback. No deadline/context/timer constructor is introduced.
func sameP1ProofContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

func (c *ChildPending) beginP1CurrentProofSign(ctx context.Context, observation *P1CurrentServiceObservation, view *CurrentChallengeView, charge *ManagerCharge) (*p1CurrentProofSignCall, error) {
	if c == nil || c.owner == nil || c.owner.child != c || ctx == nil || observation == nil || view == nil || charge == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	// This old PRIVATE predicate is metadata-only and called only BEFORE the
	// signer registers its active operation. It is never called by active work.
	if o.active != nil || o.p1CurrentProofSignTried || o.p1CurrentProofSign != nil || !o.completedCurrentObservationLocked(observation, c, view, charge) || !sameP1ProofContextData(ctx, observation.call.ctx) {
		return nil, ErrInvalid
	}
	o.p1CurrentProofSignTried = true
	op := &operation{owner: o, kind: "p1-current-proof-sign", phase: "registered_before_census_and_own_secret_encoding"}
	current := observation.call
	call := &p1CurrentProofSignCall{owner: o, op: op, ctx: ctx, child: c, observation: observation, current: current, view: view, charge: charge, prepared: current.prepared, rootBound: current.rootBound, auth: current.auth, ack: view.ack, birth: current.birth, image: current.image, custody: current.custody, read: view.read, context: current.data.Context, nonce: view.nonce}
	o.p1CurrentProofSign, o.active = call, op // BEFORE census/encoding/own-secret use
	return call, nil
}

// Narrow LOCAL exact-proof-op gate. The public view and current observation
// validators retain their unconditional active-operation refusals unchanged.
// This checks the SAME actual earlier completed current call; no flags, DATA
// adapter, generic allowed-kind list or cleared active slot supplies authority.
func (o *owner) currentProofSignCallLocked(call *p1CurrentProofSignCall) bool {
	if call == nil || call != o.p1CurrentProofSign || !o.p1CurrentProofSignTried || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-current-proof-sign" || call.op.abnormal || o.active != call.op || call.child == nil || call.child != o.child || call.child.owner != o || call.phaseCalls > len(call.phases) || !o.currentIssuedViewLocked(call.view, call.charge) {
		return false
	}
	v, c := call.observation, call.current
	if v == nil || v.self != v || v != o.p1CurrentObservation || v.owner != o || v.child != call.child || v.call != c || v.view != call.view || v.charge != call.charge || !o.p1CurrentTried || c == nil || c != o.p1Current || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "p1-current-service" || c.op.abnormal || !c.op.normalFinished || !c.normalReturned || c.resultError != nil || c.observation != v || c.view != call.view || c.charge != call.charge || !sameP1ProofContextData(call.ctx, c.ctx) || c.custody != o.p1Child || c.image != o.image || c.rootBound != o.p1RootBound || c.prepared != o.p1PreparedValidation || c.auth != o.p1ChallengeAuth || c.birth != o.reads[0] || call.custody != c.custody || call.image != c.image || call.rootBound != c.rootBound || call.prepared != c.prepared || call.auth != c.auth || call.birth != c.birth || call.ack != o.p1AckSign || call.ack != call.view.ack || call.read != call.view.read || call.context != c.data.Context || call.context != call.view.body.Context || call.nonce != call.view.nonce {
		return false
	}
	// Preserve the complete earlier current-result requirements, including its
	// OWN fresh read2/Env/image and six actual phase checks, while this exact
	// proof op (and only it) is active. Public active-refusing validators are
	// never called here; their bodies remain unchanged.
	if !c.identityAttempted || !c.identityReturned || c.identityError != nil || !c.read.verified || !c.read.beforeAttempted || !c.read.beforeReturned || c.read.beforeError != nil || !c.read.afterAttempted || !c.read.afterReturned || c.read.afterError != nil || c.read.calls <= 0 || c.read.calls > len(c.read.reads) || c.read.bytes != len(c.read.data) || !c.environmentAttempted || !c.environmentReturned || !c.imageAttempted || !c.imageReturned || c.imageError != nil || c.phaseCalls != len(c.phases) || c.data.RootBound != c.rootBound.data || c.data.Prepared != c.prepared.data {
		return false
	}
	for _, p := range c.phases {
		if !p.attempted || !p.returned || p.err != nil {
			return false
		}
	}
	return true
}

func (o *owner) checkP1CurrentProofPhase(call *p1CurrentProofSignCall) error {
	o.mu.Lock()
	if !o.currentProofSignCallLocked(call) || call.phaseCalls >= len(call.phases) {
		o.mu.Unlock()
		return ErrInvalid
	}
	p := &call.phases[call.phaseCalls]
	call.phaseCalls++
	p.attempted = true
	o.mu.Unlock()
	err := call.ctx.Err()
	p.contextError = err
	if err == nil {
		p.censusAttempted = true
		err = call.custody.ValidateOriginalChallengePhase(call.read)
		p.censusError, p.censusReturned = err, true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	p.returned = true
	if !o.currentProofSignCallLocked(call) {
		return ErrInvalid
	}
	return err
}

// Executes AFTER the unchanged directly deferred original finish/recover.
func (o *owner) publishP1CurrentProofSign(call *p1CurrentProofSignCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call != o.p1CurrentProofSign || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-current-proof-sign" {
		panic("original child current proof signing identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished && *resultErr == nil
	call.resultError = *resultErr
}

// Own-service signed DATA ONLY. The sole future PRIVATE Root MUST establish
// actual launch/fence/native/DB/Manager/Server current/no-pending/fresh canonical
// completion BEFORE this call and again BEFORE W2. W2 consumes the view even on
// partial/error. This signer starts/sends nothing and cannot certify those Root
// prerequisites, readiness, native closure or resource credit.
func (c *ChildPending) SignP1CurrentProofOriginal(ctx context.Context, observation *P1CurrentServiceObservation, view *CurrentChallengeView, charge *ManagerCharge) (frame []byte, resultErr error) {
	call, err := c.beginP1CurrentProofSign(ctx, observation, view, charge)
	if err != nil {
		return nil, err
	}
	o := c.owner
	normal := false
	defer o.publishP1CurrentProofSign(call, &normal, &resultErr)
	defer o.finish(call.op, &normal, &resultErr)
	resultErr = o.checkP1CurrentProofPhase(call)
	if resultErr == nil {
		resultErr = o.validateOwnAckContextData(call.context, call.ack)
	}
	if resultErr == nil {
		call.payloadAttempted = true
		call.payload, call.payloadError = residentprotocol.EncodeCurrentProofPayloadData(o.secret, call.context, call.nonce)
		call.payloadReturned = true
		resultErr = call.payloadError
	}
	if resultErr == nil {
		call.frameAttempted = true
		call.frame, call.frameError = residentrefs.EncodeFrameData(call.payload)
		call.frameReturned = true
		resultErr = call.frameError
	}
	if resultErr == nil {
		resultErr = o.checkP1CurrentProofPhase(call)
	}
	if resultErr == nil {
		call.output = append([]byte(nil), call.frame...)
	}
	if resultErr == nil {
		resultErr = o.checkP1CurrentProofPhase(call)
	}
	if resultErr == nil {
		frame = call.output
	} // aliases the one returned DATA copy
	normal = true
	return frame, resultErr
}
