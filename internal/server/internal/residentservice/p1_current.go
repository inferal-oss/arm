package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
)

// Bounded current-observation DATA, not a native/currentness grant or Ready.
type P1CurrentServiceData struct {
	Prepared  P1PreparedServiceData
	RootBound P1RootBoundData
	Context   residentprotocol.ContextData
}
type p1CurrentPhaseCheck struct {
	attempted, returned bool
	err                 error
}
type p1CurrentServiceRead struct {
	before, after                                                  os.FileInfo
	beforeAttempted, beforeReturned, afterAttempted, afterReturned bool
	beforeError, afterError                                        error
	data                                                           []byte
	reads                                                          [32]p1RootBoundIO
	calls, bytes                                                   int
	decoded                                                        decodedJournal
	verified                                                       bool
}
type p1CurrentServiceCall struct {
	owner                                     *owner
	op                                        *operation
	ctx                                       context.Context // actual independent Root lifetime supplied by future Root
	view                                      *CurrentChallengeView
	charge                                    *ManagerCharge
	custody                                   *residentrefs.ServiceOriginal
	image                                     *mesh.RuntimeExecutable
	prepared                                  *p1PreparedServiceCall
	rootBound                                 *p1RootBoundCall
	auth                                      *p1ChallengeAuthCall
	birth                                     *readOperation
	observation                               *P1CurrentServiceObservation
	phases                                    [6]p1CurrentPhaseCheck
	phaseCalls                                int
	identityAttempted, identityReturned       bool
	identities                                residentrefs.ServiceIdentities
	identityError                             error
	read                                      p1CurrentServiceRead // SEPARATE read2; never o.reads or o.journal
	environmentAttempted, environmentReturned bool
	environment                               []string
	environmentCanonical                      []byte
	environmentSHA, profileSHA                string
	imageAttempted, imageReturned             bool
	runtime                                   mesh.RuntimeProof
	imageError                                error
	data                                      P1CurrentServiceData
	normalReturned                            bool
	resultError                               error
}

// Sole constructor is the actual current operation below, published before its
// effects. It cannot be obtained from DATA, reissued, closed or used for Ready.
type P1CurrentServiceObservation struct {
	self   *P1CurrentServiceObservation
	owner  *owner
	call   *p1CurrentServiceCall
	child  *ChildPending
	view   *CurrentChallengeView
	charge *ManagerCharge
}

func (P1CurrentServiceObservation) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*P1CurrentServiceObservation) UnmarshalJSON([]byte) error  { return ErrInvalid }

// Local metadata ONLY, called under owner.mu. Public ValidateOriginal retains
// its active-operation refusal unchanged. No census/native/foreign lock here.
func (o *owner) currentIssuedViewLocked(v *CurrentChallengeView, charge *ManagerCharge) bool {
	if v == nil || v.self != v || v.owner != o || charge == nil || charge != o.charge || charge.owner != o || charge.child != o.child || !o.completedP1RootBoundLocked() {
		return false
	}
	c := v.issue
	return o.p1ChallengeView == v && c != nil && c == o.p1ChallengeAuth && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "p1-current-challenge-auth" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && c.view == v && c.read == v.read && c.ack == v.ack && c.rootBound == v.rootBound && v.custody == o.p1Child && v.charge == charge && v.birth == o.reads[0] && v.rootBound == o.p1RootBound && v.ack != nil && v.ack == o.p1AckSign && c.body == v.body && v.body.Context == v.ack.context && residentprotocol.MatchNonceData(v.body.Nonce, v.nonce) == nil
}
func (o *owner) currentCallLocked(c *p1CurrentServiceCall) bool {
	return c != nil && c == o.p1Current && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "p1-current-service" && !c.op.abnormal && o.active == c.op && c.ctx != nil && c.prepared == o.p1PreparedValidation && c.rootBound == o.p1RootBound && c.auth == o.p1ChallengeAuth && c.birth != nil && len(o.reads) == 1 && c.birth == o.reads[0] && c.custody == o.p1Child && c.image == o.image && c.observation != nil && c.observation == o.p1CurrentObservation && c.observation.self == c.observation && c.observation.owner == o && c.observation.child == o.child && c.observation.view == c.view && c.observation.charge == c.charge && c.observation.call == c && o.currentIssuedViewLocked(c.view, c.charge)
}
func (c *ChildPending) beginP1Current(destination **P1CurrentServiceObservation, ctx context.Context, view *CurrentChallengeView, charge *ManagerCharge) (*p1CurrentServiceCall, error) {
	if c == nil || c.owner == nil || c.owner.child != c || destination == nil || *destination != nil || ctx == nil || view == nil || charge == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	// Only local original identities before registration. The issued/actual
	// phase check occurs AFTER the actual current call has been retained.
	if o.active != nil || o.p1CurrentTried || o.p1Current != nil || o.p1CurrentObservation != nil || !o.completedP1RootBoundLocked() || view.self != view || view.owner != o || o.p1ChallengeView != view || charge != o.charge || charge.owner != o || charge.child != c {
		return nil, ErrInvalid
	}
	o.p1CurrentTried = true
	op := &operation{owner: o, kind: "p1-current-service", phase: "registered_before_current_view_and_native_effects"}
	call := &p1CurrentServiceCall{owner: o, op: op, ctx: ctx, view: view, charge: charge, custody: o.p1Child, image: o.image, prepared: o.p1PreparedValidation, rootBound: o.p1RootBound, auth: o.p1ChallengeAuth, birth: o.reads[0]}
	o.p1Current, o.active = call, op
	v := &P1CurrentServiceObservation{owner: o, call: call, child: c, view: view, charge: charge}
	v.self = v
	call.observation, o.p1CurrentObservation = v, v
	*destination = v
	return call, nil
}
func (o *owner) checkP1CurrentPhase(call *p1CurrentServiceCall) error {
	o.mu.Lock()
	if !o.currentCallLocked(call) || call.phaseCalls >= len(call.phases) {
		o.mu.Unlock()
		return ErrInvalid
	}
	c := &call.phases[call.phaseCalls]
	call.phaseCalls++
	c.attempted = true
	o.mu.Unlock()
	// Never call public view validation or enter census while holding owner.mu.
	err := call.ctx.Err()
	if err == nil {
		err = call.custody.ValidateOriginalChallengePhase(call.view.read)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	c.returned, c.err = true, err
	if !o.currentCallLocked(call) {
		return ErrInvalid
	}
	return err
}
func (o *owner) publishP1CurrentResult(call *p1CurrentServiceCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if call == nil || call != o.p1Current || call.owner != o || call.op == nil || call.op.owner != o || call.op.kind != "p1-current-service" {
		panic("original current service identity lost")
	}
	call.normalReturned = *normal && !call.op.abnormal && call.op.normalFinished
	call.resultError = *resultErr
}
func (c *ChildPending) ValidateP1CurrentOriginal(destination **P1CurrentServiceObservation, ctx context.Context, view *CurrentChallengeView, charge *ManagerCharge) (data P1CurrentServiceData, resultErr error) {
	call, err := c.beginP1Current(destination, ctx, view, charge)
	if err != nil {
		return data, err
	}
	o := c.owner
	normal := false
	defer o.publishP1CurrentResult(call, &normal, &resultErr)
	defer o.finish(call.op, &normal, &resultErr)
	resultErr = o.validateP1CurrentBody(call)
	if resultErr == nil {
		data = call.data
	}
	normal = true
	return data, resultErr
}
func (o *owner) validateP1CurrentBody(call *p1CurrentServiceCall) error {
	if err := o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	call.identityAttempted = true
	call.identities, call.identityError = call.custody.ValidateOriginal(call.ctx)
	call.identityReturned = true
	if call.identityError != nil {
		return call.identityError
	}
	if call.identities != o.p1IDs || call.identities != call.prepared.data.Identities {
		return ErrInvalid
	}
	if err := o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	if err := o.readP1CurrentService(call); err != nil {
		return err
	}
	if err := o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	if o.grant.Intent.Profile.RequireP1Environment() != nil {
		return ErrInvalid
	}
	call.environmentAttempted = true
	call.environment = os.Environ() // retain actual snapshot before matching; opaque allocation
	call.environmentReturned = true
	if err := o.grant.Intent.Profile.RuntimeEnvironment.MatchesActual(call.environment); err != nil {
		return err
	}
	var err error
	call.environmentCanonical, err = json.Marshal(o.grant.Intent.Profile.RuntimeEnvironment)
	if err != nil || len(call.environmentCanonical) > 8<<10 {
		return ErrInvalid
	}
	h := sha256.Sum256(call.environmentCanonical)
	call.environmentSHA = hex.EncodeToString(h[:])
	call.profileSHA, err = o.grant.Intent.Profile.SHA256()
	if err != nil {
		return err
	}
	if err = o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	call.imageAttempted = true
	call.runtime, call.imageError = call.image.Proof() // SAME original inherited owner; no Capture/Verify
	call.imageReturned = true
	if call.imageError != nil {
		return call.imageError
	}
	if call.runtime.SHA256 != o.build.SHA256 || call.runtime.Size != o.build.Size || o.build.GOOS != runtime.GOOS || o.build.GOARCH != runtime.GOARCH || o.build.GoBuild != runtime.Version() || call.profileSHA != o.debit.record.ProfileSHA256 || call.environmentSHA != call.prepared.data.EnvironmentSHA256 {
		return ErrInvalid
	}
	if err = o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	if err = o.validateOwnAckContextData(call.view.body.Context, call.view.ack); err != nil {
		return err
	}
	call.data = P1CurrentServiceData{Prepared: P1PreparedServiceData{Home: o.path, HomeID: sharedID(call.identities.Home), DebitID: o.debit.record.ID, LaunchKey: o.debit.record.LaunchKey, ProfileSHA256: call.profileSHA, EnvironmentSHA256: call.environmentSHA, Build: o.build, Resources: o.grant.Intent.Profile.Resources, Identities: call.identities, BirthJournalBytes: o.journal.bytes, BirthJournalSHA256: hex.EncodeToString(o.journal.last[:])}, RootBound: call.rootBound.data, Context: call.view.body.Context}
	if err = o.checkP1CurrentPhase(call); err != nil {
		return err
	}
	return call.ctx.Err()
}
func (o *owner) readP1CurrentService(call *p1CurrentServiceCall) error {
	r := &call.read
	r.beforeAttempted = true
	r.before, r.beforeError = o.p1Journal.StatOriginal(call.ctx)
	r.beforeReturned = true
	if r.beforeError != nil {
		return r.beforeError
	}
	if r.before == nil || call.rootBound.before == nil || r.before.Size() <= 0 || r.before.Size() > MaxJournalBytes || r.before.Size() != call.rootBound.data.JournalBytes || !os.SameFile(r.before, call.rootBound.before) {
		return ErrInvalid
	}
	r.data = make([]byte, int(r.before.Size())) // retained separately BEFORE first ReadAt
	for r.bytes < len(r.data) {
		if err := call.ctx.Err(); err != nil {
			return err
		}
		if r.calls >= len(r.reads) {
			return io.ErrNoProgress
		}
		c := &r.reads[r.calls]
		c.offset, c.requested = int64(r.bytes), len(r.data)-r.bytes
		r.calls++
		n, err := o.p1Journal.ReadAtOriginal(call.ctx, r.data[r.bytes:], c.offset)
		c.returned, c.err, c.returnedKnown = n, err, true
		if n < 0 || n > c.requested {
			return ErrInvalid
		}
		r.bytes += n
		if err != nil && !(errors.Is(err, io.EOF) && r.bytes == len(r.data)) {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	r.afterAttempted = true
	r.after, r.afterError = o.p1Journal.StatOriginal(call.ctx)
	r.afterReturned = true
	if r.afterError != nil {
		return r.afterError
	}
	if r.after == nil || !os.SameFile(r.before, r.after) || r.before.Size() != r.after.Size() || !r.before.ModTime().Equal(r.after.ModTime()) {
		return ErrInvalid
	}
	var err error
	r.decoded, err = validateP1CurrentServiceJournalData(r.data, call.birth.data, call.rootBound.append.frame, call.rootBound.data)
	if err != nil {
		return err
	}
	r.verified = true
	return call.ctx.Err()
}
func validateP1CurrentServiceJournalData(current, birth, third []byte, expected P1RootBoundData) (decodedJournal, error) {
	if len(current) == 0 || len(current) > MaxJournalBytes || expected.JournalBytes != int64(len(current)) || len(birth) == 0 || len(birth) >= len(current) || len(current)-len(birth) != len(third) || !bytes.Equal(current[:len(birth)], birth) || !bytes.Equal(current[len(birth):], third) {
		return decodedJournal{}, ErrInvalid
	}
	h := sha256.Sum256(third)
	if hex.EncodeToString(h[:]) != expected.JournalSHA256 {
		return decodedJournal{}, ErrInvalid
	}
	j, err := decodeJournal(current)
	if err != nil || len(j.records) != 3 || j.last != h || j.records[2].Root == nil || *j.records[2].Root != expected.Record {
		return decodedJournal{}, ErrInvalid
	}
	return j, nil
}
func (o *owner) completedCurrentObservationLocked(v *P1CurrentServiceObservation, child *ChildPending, view *CurrentChallengeView, charge *ManagerCharge) bool {
	if v == nil || v.self != v || v.owner != o || v.child != child || child == nil || child != o.child || child.owner != o || o.active != nil || !o.p1CurrentTried || o.p1CurrentObservation != v || v.view != view || v.charge != charge || !o.currentIssuedViewLocked(view, charge) {
		return false
	}
	c := v.call
	if c == nil || c != o.p1Current || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "p1-current-service" || c.op.abnormal || !c.op.normalFinished || !c.normalReturned || c.resultError != nil || c.observation != v || c.view != view || c.charge != charge || c.custody != o.p1Child || c.image != o.image || c.rootBound != o.p1RootBound || c.prepared != o.p1PreparedValidation || c.auth != o.p1ChallengeAuth || c.birth != o.reads[0] || !c.identityAttempted || !c.identityReturned || c.identityError != nil || !c.read.verified || !c.read.beforeAttempted || !c.read.beforeReturned || c.read.beforeError != nil || !c.read.afterAttempted || !c.read.afterReturned || c.read.afterError != nil || c.read.calls <= 0 || c.read.calls > len(c.read.reads) || c.read.bytes != len(c.read.data) || !c.environmentAttempted || !c.environmentReturned || !c.imageAttempted || !c.imageReturned || c.imageError != nil || c.phaseCalls != len(c.phases) || c.data.Context != view.body.Context || c.data.RootBound != c.rootBound.data || c.data.Prepared != c.prepared.data {
		return false
	}
	for _, p := range c.phases {
		if !p.attempted || !p.returned || p.err != nil {
			return false
		}
	}
	return true
}

// Pure completed-current linkage. Census calls occur OUTSIDE owner.mu; the
// original same-service/current/view/charge state is rechecked afterward.
func (v *P1CurrentServiceObservation) ValidateOriginalForFence(child *ChildPending, fence *residentrefs.FenceOriginal, view *CurrentChallengeView, charge *ManagerCharge) (P1CurrentServiceData, error) {
	if v == nil || v.self != v || v.owner == nil || fence == nil {
		return P1CurrentServiceData{}, ErrInvalid
	}
	o := v.owner
	o.mu.Lock()
	valid := o.completedCurrentObservationLocked(v, child, view, charge)
	o.mu.Unlock()
	if !valid {
		return P1CurrentServiceData{}, ErrInvalid
	}
	if err := v.call.custody.ValidateOriginalChallengePhase(view.read); err != nil {
		return P1CurrentServiceData{}, err
	}
	if err := v.call.custody.ValidateOriginalFenceLink(fence); err != nil {
		return P1CurrentServiceData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.completedCurrentObservationLocked(v, child, view, charge) {
		return P1CurrentServiceData{}, ErrInvalid
	}
	return v.call.data, nil
}
