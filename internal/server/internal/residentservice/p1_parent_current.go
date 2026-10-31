package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/residentrefs"
	"golang.org/x/sys/unix"
)

// These calls authenticate only this original service lineage. Actual normal
// Cmd.Start, launch-fence observations and nonce generation belong to Root.
// In particular, neither command.Process nor a full-write DATA projection is
// used as an authority here. There is no Root or public-start caller yet.
type p1ParentProtocolCall struct {
	group          *P1ServiceGroup
	op             *operation
	ctx            context.Context
	normalReturned bool
	resultError    error
}

type p1ParentAckAuthCall struct {
	p1ParentProtocolCall
	read        *P1ParentRead
	observation *P1ParentAckObservation
	payload     []byte
	body        residentprotocol.BoundAckBodyData
}

type p1ParentHeldCheck struct {
	entry                                                    *retainedFD
	stat                                                     unix.Stat_t
	statAttempted, statReturned                              bool
	statError                                                error
	fdFlags, accessFlags                                     int
	fdAttempted, fdReturned, accessAttempted, accessReturned bool
	fdError, accessError                                     error
}

type p1ParentNameCheck struct {
	name                string
	expected            *retainedFD
	stat                unix.Stat_t
	attempted, returned bool
	err                 error
}

type p1ParentCurrentCall struct {
	p1ParentProtocolCall
	ack                       *P1ParentAckObservation
	observation               *P1ParentCurrentObservation
	birth                     *readOperation // original fifth preStart read; never changed
	uidAttempted, uidReturned bool
	uid                       int
	held                      [3]p1ParentHeldCheck
	names                     [2]p1ParentNameCheck
	read                      p1CurrentServiceRead // DISTINCT read6, never owner.reads
	data                      P1ParentCurrentServiceData
}

type p1ParentChallengeSignCall struct {
	p1ParentProtocolCall
	ack     *P1ParentAckObservation
	current *P1ParentCurrentObservation
	context residentprotocol.ContextData
	nonce   [32]byte // DATA; this owner does not assert its entropy source
	payload []byte
	frame   []byte
}

type p1ParentProofAuthCall struct {
	p1ParentProtocolCall
	read      *P1ParentRead
	challenge *p1ParentChallengeSignCall
	payload   []byte
	body      residentprotocol.CurrentProofBodyData
}

// Sole original issue methods publish these nonclosing observations before
// parsing or native work. JSON/copies cannot construct another original.
type P1ParentAckObservation struct {
	self  *P1ParentAckObservation
	group *P1ServiceGroup
	call  *p1ParentAckAuthCall
}

type P1ParentCurrentObservation struct {
	self  *P1ParentCurrentObservation
	group *P1ServiceGroup
	call  *p1ParentCurrentCall
}

func (P1ParentAckObservation) MarshalJSON() ([]byte, error)     { return nil, ErrInvalid }
func (*P1ParentAckObservation) UnmarshalJSON([]byte) error      { return ErrInvalid }
func (P1ParentCurrentObservation) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (*P1ParentCurrentObservation) UnmarshalJSON([]byte) error  { return ErrInvalid }

type P1ParentCurrentServiceData struct {
	Context   residentprotocol.ContextData
	HomeID    NativeID
	RootBound P1RootBoundData
}

func (p *P1ServiceGroup) parentProtocolOwner(command *exec.Cmd) (*owner, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil || command == nil || command != p.command {
		return nil, ErrInvalid
	}
	return p.parent.owner, nil
}

// LOCAL metadata only, with this service owner's mutex held. All original
// five-stage observations and birth2/debit remain immutable and independently
// retained. No new IO, path walk, process lookup or secret access here.
func (p *P1ServiceGroup) parentProtocolBaseLocked() bool {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil || p.command == nil {
		return false
	}
	o := p.parent.owner
	if o.parent != p.parent || o.p1 != p || o.command != p.command || o.firstError != nil || o.files == nil || o.files.owner != o || o.grant == nil || o.append == nil || o.append.owner != o || !o.append.committed || p.debit.validateOriginalLocked(o) != nil || !p.fundingChecked || !p.bound || !p.shortSealed || !o.writerWithdrawn || !o.writerSent || !p.transferTried || len(o.reads) != 5 || p.command.Env == nil || !equalEnvironmentEntries(p.command.Env, p.environment) {
		return false
	}
	if len(p.extra) != 13 || len(p.command.ExtraFiles) != 13 || &p.extra[0] != &p.command.ExtraFiles[0] {
		return false
	}
	for i, entry := range []*retainedFD{o.files.childChannel, o.files.home, o.files.lock, o.files.journal} {
		if entry == nil || entry.file == nil || entry.closeTried || p.extra[i] != entry.file {
			return false
		}
	}
	stages := [...]string{"open", "debit", "p1-funding", "p1-binding", "p1-before-start"}
	for i, read := range o.reads {
		if read == nil || read.owner != o || read.stage != stages[i] || !read.finished || read.firstError != nil || read.before == nil || read.after == nil || len(read.data) == 0 {
			return false
		}
	}
	return int64(len(o.reads[4].data)) == o.journal.bytes
}

// Exact phase prefix; later IO must not be pre-registered while authentication
// or read6 is in progress. Completed IO's canceled context is deliberately NOT
// consulted again: only its original normal finish and exact endpoint matter.
func (p *P1ServiceGroup) parentProtocolPhasesLocked(count int) bool {
	o := p.parent.owner
	if count < 1 || count > len(p.controlPhases) || o.files.parentChannel == nil || o.files.parentChannel.file == nil || o.files.parentChannel.closeTried {
		return false
	}
	for i, call := range p.controlPhases {
		if i >= count {
			if call != nil {
				return false
			}
			continue
		}
		if !p.parentControlCompleteLocked(call, i) || call.endpoint != o.files.parentChannel || len(call.frame) <= 36 || len(call.frame) > residentrefs.MaxWireBytes || !call.deadlineAttempted || !call.deadlineReturned || call.deadlineError != nil {
			return false
		}
		if i == parentBoundAck || i == parentProof {
			if call.read == nil || call.read.self != call.read || call.read.group != p || call.read.call != call {
				return false
			}
		}
	}
	b := p.controlPhases[parentBootstrap]
	return b.bootstrap != nil && b.bootstrap == o.channel && b.bootstrap.owner == o && b.bootstrap.writeComplete && b.bootstrap.firstError == nil && b.bootstrap.transferred == len(b.frame) && len(b.bootstrap.bytes) == len(b.frame) && &b.bootstrap.bytes[0] == &b.frame[0]
}

func (p *P1ServiceGroup) completedParentAckLocked(v *P1ParentAckObservation) bool {
	if v == nil || v.self != v || v.group != p || v.call == nil || v.call != p.parentAckAuth || p.controlPhases[parentBoundAck] == nil {
		return false
	}
	c := v.call
	return c.observation == v && c.read != nil && c.read == p.controlPhases[parentBoundAck].read && p.completedParentProtocolLocked(&c.p1ParentProtocolCall, "p1-parent-ack-auth")
}

func (p *P1ServiceGroup) completedParentProtocolLocked(c *p1ParentProtocolCall, kind string) bool {
	return c != nil && c.group == p && c.ctx != nil && c.op != nil && c.op.owner == p.parent.owner && c.op.kind == kind && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil
}

func (p *P1ServiceGroup) completedParentCurrentLocked(v *P1ParentCurrentObservation) bool {
	if v == nil || v.self != v || v.group != p || v.call == nil || v.call != p.parentCurrent {
		return false
	}
	c := v.call
	if c.observation != v || !p.completedParentProtocolLocked(&c.p1ParentProtocolCall, "p1-parent-current") || !p.completedParentAckLocked(c.ack) || c.birth != p.parent.owner.reads[4] || !c.uidAttempted || !c.uidReturned || !c.read.verified || c.read.before == nil || c.read.after == nil || !c.read.beforeAttempted || !c.read.beforeReturned || c.read.beforeError != nil || !c.read.afterAttempted || !c.read.afterReturned || c.read.afterError != nil || c.read.calls <= 0 || c.read.calls > len(c.read.reads) || c.read.bytes != len(c.read.data) || c.data.Context != c.ack.call.body.Context {
		return false
	}
	for _, h := range c.held {
		if !h.statAttempted || !h.statReturned || h.statError != nil || !h.fdAttempted || !h.fdReturned || h.fdError != nil || !h.accessAttempted || !h.accessReturned || h.accessError != nil {
			return false
		}
	}
	for _, n := range c.names {
		if !n.attempted || !n.returned || n.err != nil {
			return false
		}
	}
	return true
}

// Called only by ParentPending.begin while its mutex is held. Explicit fixed
// admission slots never extend the old five reads or the four endpoint phases.
func (p *P1ServiceGroup) admitParentProtocolLocked(kind string) bool {
	if !p.parentProtocolBaseLocked() {
		return false
	}
	switch kind {
	case "p1-parent-ack-auth":
		if p.parentAckAuthTried || p.parentAckAuth != nil || !p.parentProtocolPhasesLocked(2) {
			return false
		}
		p.parentAckAuthTried = true
	case "p1-parent-current":
		if p.parentCurrentTried || p.parentCurrent != nil || !p.parentProtocolPhasesLocked(2) || p.parentAckAuth == nil || !p.completedParentAckLocked(p.parentAckAuth.observation) {
			return false
		}
		p.parentCurrentTried = true
	case "p1-parent-challenge-sign":
		if p.parentChallengeSignTried || p.parentChallengeSign != nil || !p.parentProtocolPhasesLocked(2) || p.parentCurrent == nil || !p.completedParentCurrentLocked(p.parentCurrent.observation) {
			return false
		}
		p.parentChallengeSignTried = true
	case "p1-parent-proof-auth":
		if p.parentProofAuthTried || p.parentProofAuth != nil || !p.parentProtocolPhasesLocked(4) || !p.parentChallengeWrittenLocked() {
			return false
		}
		p.parentProofAuthTried = true
	default:
		return false
	}
	return true
}

func (p *P1ServiceGroup) parentChallengeWrittenLocked() bool {
	c := p.parentChallengeSign
	w := p.controlPhases[parentChallenge]
	return c != nil && p.completedParentProtocolLocked(&c.p1ParentProtocolCall, "p1-parent-challenge-sign") && p.completedParentAckLocked(c.ack) && p.completedParentCurrentLocked(c.current) && w != nil && p.parentControlCompleteLocked(w, parentChallenge) && bytes.Equal(w.frame, c.frame) && c.context == c.ack.call.body.Context && c.context == c.current.call.data.Context
}

func (p *P1ServiceGroup) beginParentProtocol(ctx context.Context, command *exec.Cmd, kind string) (*operation, error) {
	if _, err := p.parentProtocolOwner(command); err != nil || ctx == nil {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.begin(kind)
}

func (p *P1ServiceGroup) parentProtocolCallLocked(c *p1ParentProtocolCall) bool {
	if c == nil || c.group != p || c.ctx == nil || c.op == nil || c.op.owner != p.parent.owner || c.op.abnormal || p.parent.owner.active != c.op || !p.parentProtocolBaseLocked() {
		return false
	}
	switch c.op.kind {
	case "p1-parent-ack-auth":
		return p.parentAckAuth != nil && c == &p.parentAckAuth.p1ParentProtocolCall && p.parentProtocolPhasesLocked(2)
	case "p1-parent-current":
		return p.parentCurrent != nil && c == &p.parentCurrent.p1ParentProtocolCall && p.parentProtocolPhasesLocked(2) && p.completedParentAckLocked(p.parentCurrent.ack)
	case "p1-parent-challenge-sign":
		return p.parentChallengeSign != nil && c == &p.parentChallengeSign.p1ParentProtocolCall && p.parentProtocolPhasesLocked(2) && p.completedParentAckLocked(p.parentChallengeSign.ack) && p.completedParentCurrentLocked(p.parentChallengeSign.current)
	case "p1-parent-proof-auth":
		return p.parentProofAuth != nil && c == &p.parentProofAuth.p1ParentProtocolCall && p.parentProtocolPhasesLocked(4) && p.parentChallengeWrittenLocked()
	}
	return false
}

func (p *P1ServiceGroup) parentProtocolGate(c *p1ParentProtocolCall) error {
	o := p.parent.owner
	o.mu.Lock()
	valid := p.parentProtocolCallLocked(c)
	o.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	return c.ctx.Err()
}

// Registered BEFORE finish, so publication occurs AFTER the original directly
// deferred finish/recover. Abnormal/pending/error calls cannot become completed.
func (p *P1ServiceGroup) publishParentProtocol(c *p1ParentProtocolCall, normal *bool, resultErr *error) {
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	var actual *p1ParentProtocolCall
	switch c.op.kind {
	case "p1-parent-ack-auth":
		if p.parentAckAuth != nil {
			actual = &p.parentAckAuth.p1ParentProtocolCall
		}
	case "p1-parent-current":
		if p.parentCurrent != nil {
			actual = &p.parentCurrent.p1ParentProtocolCall
		}
	case "p1-parent-challenge-sign":
		if p.parentChallengeSign != nil {
			actual = &p.parentChallengeSign.p1ParentProtocolCall
		}
	case "p1-parent-proof-auth":
		if p.parentProofAuth != nil {
			actual = &p.parentProofAuth.p1ParentProtocolCall
		}
	}
	if actual != c || c.group != p || c.op.owner != o {
		panic("original parent service protocol identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}

func (p *P1ServiceGroup) ownParentContextData(c residentprotocol.ContextData) error {
	o := p.parent.owner
	if c.Validate() != nil || !boundedText(p.debit.record.LaunchKey, 128) {
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
	if c.DebitID != p.debit.record.ID || c.LaunchKeySHA256 != p1OriginalKeySHAData(p.debit.record.LaunchKey) || c.ProfileSHA256 != p.debit.record.ProfileSHA256 || c.ProfileSHA256 != profile || c.ImageSHA256 != o.build.SHA256 || c.ImageBytes != o.build.Size || c.EnvironmentSHA256 != environment || !equalEnvironmentEntries(p.environment, o.grant.Intent.Profile.RuntimeEnvironment.Entries) || digest(o.secret[:]) != p.debit.record.SecretCommitment {
		return ErrInvalid
	}
	return nil
}

func (p *P1ServiceGroup) AuthenticateP1BoundAckOriginal(destination **P1ParentAckObservation, ctx context.Context, command *exec.Cmd, read *P1ParentRead) (resultErr error) {
	if destination == nil || *destination != nil || read == nil {
		return ErrInvalid
	}
	o, err := p.parentProtocolOwner(command)
	if err != nil {
		return err
	}
	o.mu.Lock()
	valid := read.self == read && read.group == p && read.call != nil && read.call == p.controlPhases[parentBoundAck] && read.call.read == read
	o.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	op, err := p.beginParentProtocol(ctx, command, "p1-parent-ack-auth")
	if err != nil {
		return err
	}
	c := &p1ParentAckAuthCall{p1ParentProtocolCall: p1ParentProtocolCall{group: p, op: op, ctx: ctx}, read: read}
	o.mu.Lock()
	p.parentAckAuth = c
	v := &P1ParentAckObservation{group: p, call: c}
	v.self = v
	c.observation, *destination = v, v // BEFORE parse/own secret access
	o.mu.Unlock()
	normal := false
	defer p.publishParentProtocol(&c.p1ParentProtocolCall, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	if resultErr == nil {
		c.payload, resultErr = read.BoundAckPayloadOriginalData()
	}
	if resultErr == nil {
		resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	}
	if resultErr == nil {
		c.body, resultErr = residentprotocol.DecodeBoundAckPayloadData(o.secret, c.payload)
	}
	if resultErr == nil {
		resultErr = p.ownParentContextData(c.body.Context)
	}
	if resultErr == nil {
		resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	}
	normal = true
	return resultErr
}

// Bounded authenticated DATA, not actual Start, native currency or readiness.
func (v *P1ParentAckObservation) OriginalData(group *P1ServiceGroup, command *exec.Cmd) (residentprotocol.ContextData, error) {
	if v == nil || v.self != v || v.group != group {
		return residentprotocol.ContextData{}, ErrInvalid
	}
	o, err := group.parentProtocolOwner(command)
	if err != nil {
		return residentprotocol.ContextData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || !group.parentProtocolBaseLocked() || !group.parentProtocolPhasesLocked(2) || !group.completedParentAckLocked(v) {
		return residentprotocol.ContextData{}, ErrInvalid
	}
	return v.call.body.Context, nil
}

func (p *P1ServiceGroup) ValidateP1ParentCurrentOriginal(destination **P1ParentCurrentObservation, ctx context.Context, command *exec.Cmd, ack *P1ParentAckObservation) (data P1ParentCurrentServiceData, resultErr error) {
	if destination == nil || *destination != nil || ack == nil {
		return data, ErrInvalid
	}
	o, err := p.parentProtocolOwner(command)
	if err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := p.parentProtocolBaseLocked() && p.completedParentAckLocked(ack)
	o.mu.Unlock()
	if !valid {
		return data, ErrInvalid
	}
	op, err := p.beginParentProtocol(ctx, command, "p1-parent-current")
	if err != nil {
		return data, err
	}
	c := &p1ParentCurrentCall{p1ParentProtocolCall: p1ParentProtocolCall{group: p, op: op, ctx: ctx}, ack: ack}
	o.mu.Lock()
	c.birth = o.reads[4]
	p.parentCurrent = c
	v := &P1ParentCurrentObservation{group: p, call: c}
	v.self = v
	c.observation, *destination = v, v // BEFORE held checks/Stat/ReadAt
	o.mu.Unlock()
	normal := false
	defer p.publishParentProtocol(&c.p1ParentProtocolCall, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	resultErr = p.validateParentCurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}

func (p *P1ServiceGroup) validateParentCurrentBody(c *p1ParentCurrentCall) error {
	if err := p.parentProtocolGate(&c.p1ParentProtocolCall); err != nil {
		return err
	}
	o := p.parent.owner
	c.uidAttempted = true
	c.uid = os.Geteuid()
	c.uidReturned = true
	for i, entry := range []*retainedFD{o.files.home, o.files.lock, o.files.journal} {
		h := &c.held[i]
		h.entry = entry
		if entry == nil || entry.file == nil || entry.closeTried {
			return ErrInvalid
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		h.statAttempted = true
		h.statError = unix.Fstat(entry.number, &h.stat)
		h.statReturned = true
		if h.statError != nil {
			return h.statError
		}
		mode, access := uint32(unix.S_IFREG)|0600, unix.O_RDWR
		if i == 0 {
			mode, access = uint32(unix.S_IFDIR)|0700, unix.O_RDONLY
		}
		if nativeID(&h.stat) != entry.id || h.stat.Uid != uint32(c.uid) || uint32(h.stat.Mode) != mode || h.stat.Nlink == 0 || i != 0 && h.stat.Nlink != 1 {
			return ErrInvalid
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		h.fdAttempted = true
		h.fdFlags, h.fdError = unix.FcntlInt(uintptr(entry.number), unix.F_GETFD, 0)
		h.fdReturned = true
		if h.fdError != nil {
			return h.fdError
		}
		if h.fdFlags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		h.accessAttempted = true
		h.accessFlags, h.accessError = unix.FcntlInt(uintptr(entry.number), unix.F_GETFL, 0)
		h.accessReturned = true
		if h.accessError != nil {
			return h.accessError
		}
		if h.accessFlags&unix.O_ACCMODE != access {
			return ErrInvalid
		}
	}
	for i, entry := range []*retainedFD{o.files.lock, o.files.journal} {
		n := &c.names[i]
		n.expected = entry
		if i == 0 {
			n.name = lockName
		} else {
			n.name = journalName
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		n.attempted = true
		n.err = unix.Fstatat(o.files.home.number, n.name, &n.stat, unix.AT_SYMLINK_NOFOLLOW)
		n.returned = true
		if n.err != nil {
			return n.err
		}
		if nativeID(&n.stat) != entry.id || n.stat.Uid != uint32(c.uid) || uint32(n.stat.Mode) != uint32(unix.S_IFREG)|0600 || n.stat.Nlink != 1 {
			return ErrInvalid
		}
	}
	if err := p.parentProtocolGate(&c.p1ParentProtocolCall); err != nil {
		return err
	}
	if err := p.readParentCurrent(c); err != nil {
		return err
	}
	c.data = P1ParentCurrentServiceData{Context: c.ack.call.body.Context, HomeID: o.files.home.id, RootBound: P1RootBoundData{Record: *c.read.decoded.records[2].Root, ProfileSHA256: p.debit.record.ProfileSHA256, ImageSHA256: o.build.SHA256, HomeID: o.files.home.id, JournalBytes: c.read.decoded.bytes, JournalSHA256: hex.EncodeToString(c.read.decoded.last[:])}}
	return p.parentProtocolGate(&c.p1ParentProtocolCall)
}

func (p *P1ServiceGroup) readParentCurrent(c *p1ParentCurrentCall) error {
	o := p.parent.owner
	r := &c.read
	if err := c.ctx.Err(); err != nil {
		return err
	}
	r.beforeAttempted = true
	r.before, r.beforeError = o.files.journal.file.Stat()
	r.beforeReturned = true
	if r.beforeError != nil {
		return r.beforeError
	}
	if r.before == nil || !os.SameFile(r.before, c.birth.after) || r.before.Size() <= 0 || r.before.Size() > MaxJournalBytes || r.before.Size() != c.ack.call.body.Context.ServiceJournalBytes {
		return ErrInvalid
	}
	r.data = make([]byte, int(r.before.Size()))
	for r.bytes < len(r.data) {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		if r.calls >= len(r.reads) {
			return io.ErrNoProgress
		}
		call := &r.reads[r.calls]
		call.offset, call.requested = int64(r.bytes), len(r.data)-r.bytes
		r.calls++
		n, err := o.files.journal.file.ReadAt(r.data[r.bytes:], call.offset)
		call.returned, call.err, call.returnedKnown = n, err, true
		if n < 0 || n > call.requested {
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
	if err := c.ctx.Err(); err != nil {
		return err
	}
	r.afterAttempted = true
	r.after, r.afterError = o.files.journal.file.Stat()
	r.afterReturned = true
	if r.afterError != nil {
		return r.afterError
	}
	if r.after == nil || !os.SameFile(r.before, r.after) || r.before.Size() != r.after.Size() || !r.before.ModTime().Equal(r.after.ModTime()) {
		return ErrInvalid
	}
	var err error
	r.decoded, err = validateParentCurrentJournalData(r.data, c.birth.data, o.grant, p.debit.record, c.ack.call.body.Context)
	if err != nil {
		return err
	}
	r.verified = true
	return c.ctx.Err()
}

// Canonical DATA only. The third FRAME SHA chains/commits the original birth2;
// it is deliberately distinct from SHA256 of the entire service file.
func validateParentCurrentJournalData(current, birth []byte, grant *GrantRecord, debit *DebitRecord, context residentprotocol.ContextData) (decodedJournal, error) {
	if context.Validate() != nil || grant == nil || debit == nil || len(current) == 0 || len(current) > MaxJournalBytes || int64(len(current)) != context.ServiceJournalBytes || len(birth) == 0 || len(birth) >= len(current) || !bytes.Equal(current[:len(birth)], birth) {
		return decodedJournal{}, ErrInvalid
	}
	j, err := decodeJournal(current)
	if err != nil || len(j.records) != 3 || j.records[0].Grant == nil || !reflect.DeepEqual(j.records[0].Grant, grant) || j.records[1].Debit == nil || *j.records[1].Debit != *debit || j.records[2].Root == nil {
		return decodedJournal{}, ErrInvalid
	}
	h := sha256.Sum256(current[len(birth):])
	want := RootBoundRecord{DebitID: debit.ID, LaunchKey: debit.LaunchKey, Generation: context.Generation, CommitmentSHA256: context.PreparedCommitmentSHA256}
	if j.last != h || hex.EncodeToString(h[:]) != context.ServiceJournalSHA256 || *j.records[2].Root != want || context.DebitID != debit.ID || context.LaunchKeySHA256 != p1OriginalKeySHAData(debit.LaunchKey) || context.ProfileSHA256 != debit.ProfileSHA256 || context.ImageSHA256 != grant.Intent.Profile.Build.SHA256 || context.ImageBytes != grant.Intent.Profile.Build.Size {
		return decodedJournal{}, ErrInvalid
	}
	return j, nil
}

// Usable by the already-bound FreshFence with its own actual group/command.
// This proves ONLY this service call's completed same-group lineage, not fence
// ownership, absolute home-path currency, atomic stability, Start or Ready.
func (v *P1ParentCurrentObservation) ValidateOriginalGroup(group *P1ServiceGroup, command *exec.Cmd) (P1ParentCurrentServiceData, error) {
	if v == nil || v.self != v || v.group != group {
		return P1ParentCurrentServiceData{}, ErrInvalid
	}
	o, err := group.parentProtocolOwner(command)
	if err != nil {
		return P1ParentCurrentServiceData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || !group.parentProtocolBaseLocked() || !group.parentProtocolPhasesLocked(2) || !group.completedParentCurrentLocked(v) {
		return P1ParentCurrentServiceData{}, ErrInvalid
	}
	return v.call.data, nil
}

func (p *P1ServiceGroup) SignP1CurrentChallengeOriginal(ctx context.Context, command *exec.Cmd, ack *P1ParentAckObservation, current *P1ParentCurrentObservation, nonce [32]byte) (frame []byte, resultErr error) {
	o, err := p.parentProtocolOwner(command)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	valid := p.parentProtocolBaseLocked() && p.completedParentAckLocked(ack) && p.completedParentCurrentLocked(current) && current.call.ack == ack
	o.mu.Unlock()
	if !valid {
		return nil, ErrInvalid
	}
	op, err := p.beginParentProtocol(ctx, command, "p1-parent-challenge-sign")
	if err != nil {
		return nil, err
	}
	c := &p1ParentChallengeSignCall{p1ParentProtocolCall: p1ParentProtocolCall{group: p, op: op, ctx: ctx}, ack: ack, current: current, context: ack.call.body.Context, nonce: nonce}
	o.mu.Lock()
	p.parentChallengeSign = c
	o.mu.Unlock()
	normal := false
	defer p.publishParentProtocol(&c.p1ParentProtocolCall, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	if resultErr == nil {
		resultErr = p.ownParentContextData(c.context)
	}
	if resultErr == nil {
		c.payload, resultErr = residentprotocol.EncodeCurrentChallengePayloadData(o.secret, c.context, c.nonce)
	}
	if resultErr == nil {
		c.frame, resultErr = residentrefs.EncodeFrameData(c.payload)
	}
	if resultErr == nil {
		resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	}
	if resultErr == nil {
		frame = append([]byte(nil), c.frame...)
	}
	normal = true
	return frame, resultErr
}

func (p *P1ServiceGroup) AuthenticateP1CurrentProofOriginal(ctx context.Context, command *exec.Cmd, read *P1ParentRead) (data residentprotocol.CurrentProofBodyData, resultErr error) {
	o, err := p.parentProtocolOwner(command)
	if err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := read != nil && read.self == read && read.group == p && read.call != nil && read.call == p.controlPhases[parentProof] && read.call.read == read
	o.mu.Unlock()
	if !valid {
		return data, ErrInvalid
	}
	op, err := p.beginParentProtocol(ctx, command, "p1-parent-proof-auth")
	if err != nil {
		return data, err
	}
	c := &p1ParentProofAuthCall{p1ParentProtocolCall: p1ParentProtocolCall{group: p, op: op, ctx: ctx}, read: read, challenge: p.parentChallengeSign}
	o.mu.Lock()
	p.parentProofAuth = c
	o.mu.Unlock()
	normal := false
	defer p.publishParentProtocol(&c.p1ParentProtocolCall, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	if resultErr == nil {
		c.payload, resultErr = read.CurrentProofPayloadOriginalData()
	}
	if resultErr == nil {
		resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	}
	if resultErr == nil {
		c.body, resultErr = residentprotocol.DecodeCurrentProofPayloadData(o.secret, c.payload)
	}
	if resultErr == nil {
		resultErr = residentprotocol.MatchContextData(c.body.Context, c.challenge.context)
	}
	if resultErr == nil {
		resultErr = residentprotocol.MatchNonceData(c.body.Nonce, c.challenge.nonce)
	}
	if resultErr == nil {
		resultErr = p.ownParentContextData(c.body.Context)
	}
	if resultErr == nil {
		resultErr = p.parentProtocolGate(&c.p1ParentProtocolCall)
	}
	if resultErr == nil {
		data = c.body
	}
	normal = true
	return data, resultErr
}
