package residentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

type p1BoundAckSignCall struct {
	owner          *owner
	op             *operation
	prepared       *p1PreparedServiceCall
	rootBound      *p1RootBoundCall
	custody        *residentrefs.ServiceOriginal
	charge         *ManagerCharge
	birth          *readOperation
	image          *mesh.RuntimeExecutable
	context        residentprotocol.ContextData
	payload, frame []byte
	normalReturned bool
	resultError    error
}
type p1ChallengeAuthCall struct {
	owner          *owner
	op             *operation
	read           *residentrefs.P1ChallengeRead
	ack            *p1BoundAckSignCall
	rootBound      *p1RootBoundCall
	view           *CurrentChallengeView
	payload        []byte
	body           residentprotocol.CurrentChallengeBodyData
	normalReturned bool
	resultError    error
}

// A nonclosing authenticated PHASE view, not a native grant/currentness proof.
// The actual service issue method alone publishes its original before parsing.
// It has no from-DATA constructor, getter/reissuer, secret or close capability.
type CurrentChallengeView struct {
	self      *CurrentChallengeView
	owner     *owner
	issue     *p1ChallengeAuthCall
	read      *residentrefs.P1ChallengeRead
	custody   *residentrefs.ServiceOriginal
	charge    *ManagerCharge
	birth     *readOperation
	rootBound *p1RootBoundCall
	ack       *p1BoundAckSignCall
	body      residentprotocol.CurrentChallengeBodyData
	nonce     [32]byte
}

func (CurrentChallengeView) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*CurrentChallengeView) UnmarshalJSON([]byte) error  { return ErrInvalid }

func p1OriginalKeySHAData(key string) string {
	h := sha256.Sum256([]byte("arm-resident-launch-key-v1\x00" + key))
	return hex.EncodeToString(h[:])
}
func (o *owner) completedP1RootBoundLocked() bool {
	c := o.p1RootBound
	p := o.p1PreparedValidation
	return o.firstError == nil && o.child != nil && o.child.owner == o && o.prepared && o.runtimeBound && o.image != nil && o.charge != nil && o.charge.owner == o && o.charge.child == o.child && o.p1Child != nil && o.p1Journal != nil && o.grant != nil && o.debit != nil && o.debit.validateOriginalLocked(o) == nil && p != nil && p.owner == o && p.op != nil && p.op.owner == o && p.op.kind == "p1-prepared-validation" && p.op.normalFinished && !p.op.abnormal && p.returned && p.resultError == nil && p.custody == o.p1Child && p.image == o.image && p.charge == o.charge && p.birth != nil && len(o.reads) == 1 && p.birth == o.reads[0] && p.birth.owner == o && p.birth.stage == "p1-receive" && p.birth.finished && p.birth.firstError == nil && p.birth.after != nil && c != nil && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "p1-root-bound" && c.op.normalFinished && !c.op.abnormal && c.returned && c.resultError == nil && c.prepared == p && c.custody == o.p1Child && c.image == o.image && c.charge == o.charge && c.append != nil && c.append.owner == o && c.append.committed && c.append.syncTried && c.syncReturned && c.syncError == nil && c.append.firstError == nil && c.append.written == len(c.append.frame) && c.data.Record == c.record
}

// Service-owned fields only. Footer/settings/intent/launch links remain DATA
// here; actual Root C4d must derive/compare its complete context BEFORE any send.
func (o *owner) validateOwnAckContextData(c residentprotocol.ContextData, call *p1BoundAckSignCall) error {
	if c.Validate() != nil {
		return ErrInvalid
	}
	bound := call.rootBound.data
	if !boundedText(bound.Record.LaunchKey, 128) || bound.Record.DebitID != o.debit.record.ID || bound.Record.DebitID != call.prepared.data.DebitID || bound.Record.LaunchKey != call.prepared.data.LaunchKey {
		return ErrInvalid
	}
	profile, err := o.grant.Intent.Profile.SHA256()
	if err != nil {
		return err
	}
	environment, err := p1EnvironmentSHA256Data(o.grant.Intent.Profile.RuntimeEnvironment)
	if err != nil {
		return err
	}
	if c.Generation != bound.Record.Generation || c.DebitID != bound.Record.DebitID || c.PreparedCommitmentSHA256 != bound.Record.CommitmentSHA256 || c.ServiceJournalBytes != bound.JournalBytes || c.ServiceJournalSHA256 != bound.JournalSHA256 || c.ProfileSHA256 != bound.ProfileSHA256 || c.ProfileSHA256 != profile || c.ProfileSHA256 != o.debit.record.ProfileSHA256 || c.ProfileSHA256 != call.prepared.data.ProfileSHA256 || c.ImageSHA256 != bound.ImageSHA256 || c.ImageSHA256 != o.build.SHA256 || c.ImageSHA256 != call.prepared.data.Build.SHA256 || c.ImageBytes != o.build.Size || c.ImageBytes != call.prepared.data.Build.Size || c.EnvironmentSHA256 != environment || c.EnvironmentSHA256 != call.prepared.data.EnvironmentSHA256 || c.LaunchKeySHA256 != p1OriginalKeySHAData(bound.Record.LaunchKey) || bound.Record.LaunchKey != o.debit.record.LaunchKey {
		return ErrInvalid
	}
	return nil
}
func (c *ChildPending) beginP1AckSign(ctx context.Context, data residentprotocol.ContextData) (*p1BoundAckSignCall, error) {
	if c == nil || c.owner == nil || c.owner.child != c || ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || o.p1AckSignTried || !o.completedP1RootBoundLocked() {
		return nil, ErrInvalid
	}
	o.p1AckSignTried = true
	op := &operation{owner: o, kind: "p1-bound-ack-sign", phase: "registered_before_own_secret_encoding"}
	call := &p1BoundAckSignCall{owner: o, op: op, prepared: o.p1PreparedValidation, rootBound: o.p1RootBound, custody: o.p1Child, charge: o.charge, birth: o.reads[0], image: o.image, context: data}
	o.p1AckSign, o.active = call, op
	return call, nil
}
func (o *owner) publishAckSignResult(call *p1BoundAckSignCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call != o.p1AckSign || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-bound-ack-sign" {
		panic("original Ack signing identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (c *ChildPending) SignP1BoundAckOriginal(ctx context.Context, data residentprotocol.ContextData) (frame []byte, resultErr error) {
	call, err := c.beginP1AckSign(ctx, data)
	if err != nil {
		return nil, err
	}
	o := c.owner
	normal := false
	defer o.publishAckSignResult(call, &normal, &resultErr)
	defer o.finish(call.op, &normal, &resultErr)
	resultErr = o.validateOwnAckContextData(data, call)
	if resultErr == nil {
		call.payload, resultErr = residentprotocol.EncodeBoundAckPayloadData(o.secret, data)
	}
	if resultErr == nil {
		call.frame, resultErr = residentrefs.EncodeFrameData(call.payload)
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		frame = append([]byte(nil), call.frame...)
	} // bounded DATA copy, no authority getter
	normal = true
	return frame, resultErr
}
func (c *ChildPending) beginP1ChallengeAuth(destination **CurrentChallengeView, ctx context.Context, read *residentrefs.P1ChallengeRead, charge *ManagerCharge) (*p1ChallengeAuthCall, error) {
	if c == nil || c.owner == nil || c.owner.child != c || destination == nil || *destination != nil || ctx == nil || read == nil || charge == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	a := o.p1AckSign
	if o.active != nil || o.p1ChallengeAuthTried || o.p1ChallengeView != nil || !o.completedP1RootBoundLocked() || charge != o.charge || charge.owner != o || charge.child != c || a == nil || a.owner != o || a.op == nil || a.op.owner != o || a.op.kind != "p1-bound-ack-sign" || !a.op.normalFinished || a.op.abnormal || !a.normalReturned || a.resultError != nil || a.rootBound != o.p1RootBound || a.prepared != o.p1PreparedValidation || a.custody != o.p1Child || a.charge != charge || a.birth != o.reads[0] || a.image != o.image || len(a.frame) <= 36 || len(a.frame) > residentrefs.MaxWireBytes {
		return nil, ErrInvalid
	}
	o.p1ChallengeAuthTried = true
	op := &operation{owner: o, kind: "p1-current-challenge-auth", phase: "registered_before_actual_read_link_and_HMAC"}
	call := &p1ChallengeAuthCall{owner: o, op: op, read: read, ack: a, rootBound: o.p1RootBound}
	o.p1ChallengeAuth, o.active = call, op
	v := &CurrentChallengeView{owner: o, issue: call, read: read, custody: o.p1Child, charge: charge, birth: o.reads[0], rootBound: o.p1RootBound, ack: a}
	v.self = v
	call.view, o.p1ChallengeView = v, v
	*destination = v // actual owner and outslot BEFORE parse/secret use
	return call, nil
}
func (o *owner) publishChallengeAuthResult(call *p1ChallengeAuthCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call != o.p1ChallengeAuth || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-current-challenge-auth" || call.view != o.p1ChallengeView {
		panic("original challenge auth identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (c *ChildPending) AuthenticateP1CurrentChallengeOriginal(destination **CurrentChallengeView, ctx context.Context, read *residentrefs.P1ChallengeRead, charge *ManagerCharge) (resultErr error) {
	call, err := c.beginP1ChallengeAuth(destination, ctx, read, charge)
	if err != nil {
		return err
	}
	o := c.owner
	normal := false
	defer o.publishChallengeAuthResult(call, &normal, &resultErr)
	defer o.finish(call.op, &normal, &resultErr)
	call.payload, resultErr = o.p1Child.OriginalChallengePayloadData(read, call.ack.frame)
	if resultErr == nil {
		call.body, resultErr = residentprotocol.DecodeCurrentChallengePayloadData(o.secret, call.payload)
	}
	if resultErr == nil {
		resultErr = residentprotocol.MatchContextData(call.body.Context, call.ack.context)
	}
	if resultErr == nil {
		resultErr = o.validateOwnAckContextData(call.body.Context, call.ack)
	}
	if resultErr == nil {
		_, resultErr = hex.Decode(call.view.nonce[:], []byte(call.body.Nonce))
	}
	if resultErr == nil {
		call.view.body = call.body
		resultErr = ctx.Err()
	}
	normal = true
	return resultErr
}

// Pure same-original PHASE observation. No native currentness, grant or Ready.
// Actual control W2 registration consumes this phase even if its IO later fails.
func (v *CurrentChallengeView) ValidateOriginal(charge *ManagerCharge) (residentprotocol.CurrentChallengeBodyData, error) {
	var empty residentprotocol.CurrentChallengeBodyData
	if v == nil || v.self != v || v.owner == nil || charge == nil {
		return empty, ErrInvalid
	}
	o := v.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	c := v.issue
	if o.active != nil || !o.completedP1RootBoundLocked() || o.p1ChallengeView != v || c == nil || c != o.p1ChallengeAuth || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "p1-current-challenge-auth" || !c.op.normalFinished || c.op.abnormal || !c.normalReturned || c.resultError != nil || c.view != v || c.read != v.read || c.ack != v.ack || c.rootBound != v.rootBound || v.custody != o.p1Child || v.charge != charge || charge != o.charge || charge.owner != o || charge.child != o.child || v.birth != o.reads[0] || v.rootBound != o.p1RootBound || v.ack != o.p1AckSign || c.body != v.body || v.body.Context != v.ack.context || residentprotocol.MatchNonceData(v.body.Nonce, v.nonce) != nil {
		return empty, ErrInvalid
	}
	if err := v.custody.ValidateOriginalChallengePhase(v.read); err != nil {
		return empty, err
	}
	return v.body, nil
}

// Ensure no accidental JSON projection through a future wrapper is accepted.
var _ json.Marshaler = (*CurrentChallengeView)(nil)
