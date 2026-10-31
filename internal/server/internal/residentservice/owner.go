package residentservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"sync"
)

// ParentPending exists before its original native open/debit/launch. It is not a
// grant. Production caller must be the actual retained public-start birth owner.
type ParentPending struct{ owner *owner }
type OriginalDebit struct {
	owner  *owner
	record *DebitRecord
}
type owner struct {
	buildFromOriginalGrant                              bool
	p1Child                                             *residentrefs.ServiceOriginal
	p1Journal                                           *residentrefs.JournalView
	p1IDs                                               residentrefs.ServiceIdentities
	p1Environment                                       []string
	writerWithdrawn                                     bool
	p1                                                  *P1ServiceGroup
	operator                                            *OperatorPending // actual offline operator owner; never a parent/child capability
	child                                               *ChildPending
	command                                             *exec.Cmd
	startRecorded                                       bool
	startTried                                          bool
	startError                                          error
	channelTried, writerTried, writerSent, receiveTried bool
	channel                                             *channelOperation
	image                                               *mesh.RuntimeExecutable
	runtimeBound                                        bool
	charge                                              *ManagerCharge
	mu                                                  sync.Mutex // publication only; never file/channel/syscall/Wait
	parent                                              *ParentPending
	path                                                string
	build                                               Build
	files                                               *nativeFiles
	active                                              *operation
	openTried, opened, debitTried, prepared             bool
	grant                                               *GrantRecord
	journal                                             decodedJournal
	debit                                               *OriginalDebit
	secret                                              [32]byte
	append                                              *appendOperation
	reads                                               []*readOperation
	firstError                                          error
	p1PreparedTried                                     bool
	p1PreparedValidation                                *p1PreparedServiceCall
	p1RootBoundTried                                    bool
	p1RootBound                                         *p1RootBoundCall
	p1AckSignTried, p1ChallengeAuthTried                bool
	p1AckSign                                           *p1BoundAckSignCall
	p1ChallengeAuth                                     *p1ChallengeAuthCall
	p1ChallengeView                                     *CurrentChallengeView
	p1CurrentTried                                      bool
	p1Current                                           *p1CurrentServiceCall
	p1CurrentObservation                                *P1CurrentServiceObservation
	p1CurrentProofSignTried                              bool
	p1CurrentProofSign                                   *p1CurrentProofSignCall
	openedFDs, closedFDs                                uint64
	liveFDs                                             int
}
type operation struct {
	owner          *owner
	kind, phase    string
	abnormal       bool
	normalFinished bool // final metadata only; not native closure or admission
	cause          any
}
type appendOperation struct {
	owner                *owner
	frame                []byte
	offset               int64
	written              int
	writes               uint32
	syncTried, committed bool
	firstError           error
}
type Inspection struct {
	WriterWithdrawn, TransferAttempted, TransferWriteComplete bool
	TransferBytes, TransferWritten                            int
	TransferWrites                                            uint32
	OpenAttempted, Opened, DebitAttempted, DebitPrepared      bool
	OperationPending, OperationAbnormal                       bool
	OperationKind, OperationPhase                             string
	OriginalAppendRetained                                    bool
	AppendBytes, AppendWritten                                int
	AppendWrites                                              uint32
	AppendSyncAttempted, AppendCommitted                      bool
	OpenedFDs, ClosedFDs                                      uint64
	LastPublishedLiveFDs                                      int
	FirstError                                                error
	NativeCensusUncertain                                     bool
	NativeOutcome                                             string
}
type abnormal struct{ cause any }

func (e *abnormal) Error() string {
	return "original resident service operation abnormal; custody unresolved"
}
func (e *abnormal) Unwrap() error { err, _ := e.cause.(error); return err }

var ErrPending = errors.New("original resident service operation pending; retain and inspect")

func NewParentPending(home string, build Build) (*ParentPending, error) {
	// Pure construction; actual native home/profile/build checks belong to open.
	if !boundedText(home, 4096) || !validDigest(build.SHA256) {
		return nil, ErrInvalid
	}
	p := &ParentPending{}
	o := &owner{parent: p, path: home, build: build}
	p.owner = o
	o.files = &nativeFiles{owner: o}
	return p, nil
}

func (p *ParentPending) begin(kind string) (*operation, error) {
	if p == nil || p.owner == nil || p.owner.parent != p {
		return nil, ErrInvalid
	}
	o := p.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrPending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	if (o.p1 != nil || (o.grant != nil && o.grant.Intent.Profile.Schema == 2)) && (kind == "channel" || kind == "command-binding" || kind == "start" || kind == "writer-handoff") {
		return nil, ErrInvalid
	}
	switch kind {
	case "open":
		if o.openTried {
			return nil, ErrInvalid
		}
		o.openTried = true
	case "debit":
		if !o.opened || o.debitTried {
			return nil, ErrInvalid
		}
		o.debitTried = true
	case "channel":
		if !o.prepared || o.channelTried || o.writerSent {
			return nil, ErrInvalid
		}
		o.channelTried = true
	case "command-binding":
		if !o.prepared || !o.channelTried || o.command != nil || o.writerSent {
			return nil, ErrInvalid
		}
	case "start":
		if !o.prepared || o.command == nil || o.image == nil || o.startTried || o.writerSent {
			return nil, ErrInvalid
		}
		o.startTried = true
	case "writer-handoff":
		if !o.prepared || o.writerTried || o.writerSent || !o.startRecorded || o.startError != nil || o.command == nil || o.command.Process == nil {
			return nil, ErrInvalid
		}
		o.writerTried = true
	case "p1-funding":
		if o.p1 == nil || !o.prepared || o.p1.fundingTried || o.channelTried || o.writerWithdrawn {
			return nil, ErrInvalid
		}
		o.p1.fundingTried = true
	case "p1-channel":
		if o.p1 == nil || !o.p1.fundingChecked || o.channelTried || o.writerWithdrawn {
			return nil, ErrInvalid
		}
		o.channelTried = true
	case "p1-binding":
		if o.p1 == nil || !o.p1.fundingChecked || !o.channelTried || o.p1.bindTried || o.command != nil || o.writerWithdrawn {
			return nil, ErrInvalid
		}
		o.p1.bindTried = true
	case "p1-withdraw":
		if o.p1 == nil || !o.p1.bound || o.writerTried || o.writerWithdrawn {
			return nil, ErrInvalid
		}
		o.writerTried = true
	case "p1-before-start":
		if o.p1 == nil || !o.p1.bound || !o.writerWithdrawn || o.p1.beforeStartTried {
			return nil, ErrInvalid
		}
		o.p1.beforeStartTried = true
	case "p1-short-seal":
		if o.p1 == nil || !o.p1.bound || !o.writerWithdrawn || !o.p1.beforeStartTried || o.p1.shortSealTried {
			return nil, ErrInvalid
		}
		o.p1.shortSealTried = true
	case "p1-transfer":
		if o.p1 == nil || !o.p1.shortSealed || !o.writerWithdrawn || o.p1.transferTried || o.writerSent || o.channel != nil {
			return nil, ErrInvalid
		}
		o.p1.transferTried = true
	case "p1-bound-ack-read", "p1-current-challenge-write", "p1-current-proof-read":
		if o.p1 == nil || !o.writerWithdrawn || !o.writerSent || !o.p1.transferTried {
			return nil, ErrInvalid
		}
	case "p1-parent-ack-auth", "p1-parent-current", "p1-parent-challenge-sign", "p1-parent-proof-auth":
		if o.p1 == nil || !o.p1.admitParentProtocolLocked(kind) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	op := &operation{owner: o, kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}

func (o *owner) finish(op *operation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original service operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &abnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && o.firstError == nil {
			o.firstError = *resultErr
		}
		if *resultErr == nil {
			if op.kind == "open" {
				o.opened = true
			}
			if op.kind == "debit" {
				o.prepared = true
			}
			if op.kind == "p1-funding" {
				o.p1.fundingChecked = true
			}
			if op.kind == "p1-short-seal" {
				o.p1.shortSealed = true
			}
			if op.kind == "p1-binding" {
				o.p1.bound = true
			}
		}
		o.active = nil
	}
	o.publishLocked()
	if *normal && !op.abnormal {
		op.normalFinished = true
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func (o *owner) publishLocked() {
	if o.p1Child != nil {
		census := o.p1Child.InspectOriginal()
		o.openedFDs = uint64(census.Registered)
		o.closedFDs = 0
		o.liveFDs = census.Registered
		return
	}

	o.openedFDs, o.closedFDs = o.files.opened, o.files.closed
	o.liveFDs = 0
	for _, f := range o.files.slots {
		if f != nil {
			o.liveFDs++
		}
	}
}

func (n *nativeFiles) progress(phase string) {
	o := n.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("service native effect lacks original operation")
	}
	o.active.phase = phase
	o.publishLocked()
}

func (p *ParentPending) OpenExistingAuthorizedOriginal(ctx context.Context) (resultErr error) {
	op, err := p.begin("open")
	if err != nil {
		return err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	resultErr = p.owner.openBody(ctx)
	normal = true
	return resultErr
}

func (o *owner) openBody(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.files.openExistingAuthorized(o.path); err != nil {
		return err
	}
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return err
	}
	grant := journal.records[0].Grant
	if o.buildFromOriginalGrant {
		if grant.Intent.Profile.RequireP1Environment() != nil {
			return ErrInvalid
		}
		o.mu.Lock()
		o.build = grant.Intent.Profile.Build
		o.mu.Unlock()
	}
	if grant.Intent.Home != o.path || grant.Intent.HomeID != o.files.home.id || grant.Intent.Profile.Build != o.build {
		return ErrInvalid
	}
	o.mu.Lock()
	o.journal = journal
	o.grant = grant
	o.mu.Unlock()
	if len(journal.records) != 1 {
		return ErrUnresolved
	} // never reconstruct/adopt an original debit
	return o.files.validateCurrent(o.path)
}

type readOperation struct {
	owner         *owner
	stage         string
	data          []byte
	before, after os.FileInfo
	bytes, calls  int
	firstError    error
	finished      bool
}

func (o *owner) readOriginalJournal(ctx context.Context) (decodedJournal, error) {
	call := &readOperation{owner: o}
	o.mu.Lock()
	if o.active == nil || o.active.owner != o {
		o.mu.Unlock()
		return decodedJournal{}, ErrInvalid
	}
	p1 := o.p1 != nil || o.buildFromOriginalGrant
	limit := 4
	if p1 {
		limit = 5
	}
	if len(o.reads) >= limit {
		o.mu.Unlock()
		return decodedJournal{}, ErrInvalid
	}
	prior := make([]string, 0, len(o.reads))
	for _, read := range o.reads {
		if p1 && (read == nil || read.owner != o || !read.finished || read.firstError != nil) {
			o.mu.Unlock()
			return decodedJournal{}, ErrInvalid
		}
		prior = append(prior, read.stage)
	}
	if err := journalReadBudgetData(p1, prior, o.active.kind); err != nil {
		o.mu.Unlock()
		return decodedJournal{}, err
	}
	call.stage = o.active.kind
	o.reads = append(o.reads, call)
	o.mu.Unlock()
	fail := func(err error) (decodedJournal, error) {
		o.mu.Lock()
		if call.firstError == nil {
			call.firstError = err
		}
		o.mu.Unlock()
		return decodedJournal{}, err
	}
	o.files.progress("original_service_journal_read_pending")
	before, err := o.journalStatOriginal(ctx)
	o.mu.Lock()
	call.before = before
	o.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if before.Size() <= 0 || before.Size() > MaxJournalBytes {
		return fail(ErrInvalid)
	}
	data := make([]byte, int(before.Size()))
	o.mu.Lock()
	call.data = data
	o.mu.Unlock() // retained before original read
	for at, calls := 0, 0; at < len(data); calls++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if calls >= 32 {
			return fail(ErrInvalid)
		}
		n, err := o.journalReadOriginal(ctx, data[at:], int64(at))
		at += n
		o.mu.Lock()
		call.bytes = at
		call.calls = calls + 1
		o.mu.Unlock()
		if err != nil && !(errors.Is(err, io.EOF) && at == len(data)) {
			return fail(err)
		}
		if n == 0 {
			return fail(io.ErrNoProgress)
		}
	}
	after, err := o.journalStatOriginal(ctx)
	o.mu.Lock()
	call.after = after
	o.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fail(ErrInvalid)
	}
	decoded, err := decodeJournal(data)
	if err != nil {
		return fail(err)
	}
	o.mu.Lock()
	call.finished = true
	o.mu.Unlock()
	return decoded, nil
}

func (p *ParentPending) PrepareOriginalDebit(ctx context.Context, launchKey string) (debit *OriginalDebit, resultErr error) {
	op, err := p.begin("debit")
	if err != nil {
		return nil, err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o := p.owner
	debit = &OriginalDebit{owner: o, record: &DebitRecord{LaunchKey: launchKey}}
	o.mu.Lock()
	o.debit = debit
	o.mu.Unlock() // actual pending debit BEFORE randomness/append effects
	resultErr = o.prepareBody(ctx, debit)
	normal = true
	return debit, resultErr
}

func (o *owner) prepareBody(ctx context.Context, debit *OriginalDebit) error {
	if ctx == nil || !boundedText(debit.record.LaunchKey, 128) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.files.validateCurrent(o.path); err != nil {
		return err
	}
	current, err := o.readOriginalJournal(ctx)
	if err != nil {
		return err
	}
	if len(current.records) != 1 || current.last != o.journal.last {
		return ErrUnresolved
	}
	o.files.progress("original_debit_secret_pending")
	if _, err = rand.Read(o.secret[:]); err != nil {
		return err
	}
	profile, err := o.grant.Intent.Profile.SHA256()
	if err != nil {
		return err
	}
	record := debit.record
	record.ID = digest(append([]byte(record.LaunchKey), o.secret[:]...))
	record.GrantID = o.grant.ID
	record.GrantRevision = o.grant.Revision
	record.ProfileSHA256 = profile
	record.SecretCommitment = digest(o.secret[:])
	record.LockID = o.files.lock.id
	record.JournalID = o.files.journal.id
	next := Record{Schema: 1, Sequence: 2, Kind: "debit-prepared", Debit: record}
	return o.appendOriginal(ctx, next)
}

func (o *owner) appendOriginal(ctx context.Context, next Record) error {
	candidate := append(append([]Record(nil), o.journal.records...), next)
	if err := ValidateLedger(candidate); err != nil {
		return err
	}
	frame, err := encodeFrame(next, o.journal.last)
	if err != nil {
		return err
	}
	if o.journal.bytes+int64(len(frame)) > MaxJournalBytes {
		return ErrInvalid
	}
	call := &appendOperation{owner: o, frame: frame, offset: o.journal.bytes}
	o.mu.Lock()
	if o.append != nil {
		o.mu.Unlock()
		return ErrInvalid
	}
	o.append = call
	o.mu.Unlock() // registered BEFORE actual write
	fail := func(err error) error {
		o.mu.Lock()
		if call.firstError == nil {
			call.firstError = err
		}
		o.mu.Unlock()
		return err
	}
	for written := 0; written < len(frame); {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		o.mu.Lock()
		if call.writes >= 32 {
			o.mu.Unlock()
			return fail(io.ErrShortWrite)
		}
		call.writes++
		o.mu.Unlock()
		o.files.progress("original_service_journal_write_pending")
		n, err := o.journalWriteOriginal(ctx, frame[written:], call.offset+int64(written))
		written += n
		o.mu.Lock()
		call.written = written
		o.mu.Unlock()
		if err != nil {
			return fail(err)
		}
		if n == 0 {
			return fail(io.ErrNoProgress)
		}
	}
	o.mu.Lock()
	call.syncTried = true
	o.mu.Unlock()
	o.files.progress("original_service_journal_fsync_pending")
	if err := o.journalSyncOriginal(ctx); err != nil {
		return fail(err)
	}
	o.mu.Lock()
	call.committed = true
	o.journal = decodedJournal{records: candidate, bytes: call.offset + int64(len(frame)), last: sha256.Sum256(frame)}
	o.mu.Unlock()
	return nil
}

func (p *ParentPending) InspectOriginal() Inspection {
	if p == nil || p.owner == nil || p.owner.parent != p {
		return Inspection{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	}
	return p.owner.inspectOriginal()
}
func (c *ChildPending) InspectOriginal() Inspection {
	if c == nil || c.owner == nil || c.owner.child != c {
		return Inspection{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	}
	return c.owner.inspectOriginal()
}
func (o *owner) inspectOriginal() Inspection {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := Inspection{OpenAttempted: o.openTried, Opened: o.opened, DebitAttempted: o.debitTried, DebitPrepared: o.prepared, OpenedFDs: o.openedFDs, ClosedFDs: o.closedFDs, LastPublishedLiveFDs: o.liveFDs, FirstError: o.firstError, NativeOutcome: "unknown"}
	v.WriterWithdrawn = o.writerWithdrawn
	if call := o.channel; call != nil {
		v.TransferAttempted = true
		v.TransferBytes = len(call.bytes)
		v.TransferWritten = call.transferred
		v.TransferWrites = call.writes
		v.TransferWriteComplete = call.writeComplete
	}
	if op := o.active; op != nil {
		v.OperationPending, v.OperationAbnormal, v.OperationKind, v.OperationPhase = true, op.abnormal, op.kind, op.phase
		v.NativeCensusUncertain = true
	}
	if call := o.append; call != nil {
		v.OriginalAppendRetained = true
		v.AppendBytes = len(call.frame)
		v.AppendWritten = call.written
		v.AppendWrites = call.writes
		v.AppendSyncAttempted = call.syncTried
		v.AppendCommitted = call.committed
	}
	return v
}

// OriginalDebit has no Ready/Closed/Release method. Inherited continuation and
// a nonretiring ManagerCharge are implemented only by the actual handoff owner.
func (d *OriginalDebit) validateOriginalLocked(o *owner) error {
	if d == nil || d.owner != o || o.debit != d || d.record == nil || !o.prepared || o.firstError != nil {
		return ErrInvalid
	}
	if len(o.journal.records) != 2 || o.journal.records[1].Debit != d.record {
		return ErrInvalid
	}
	return nil
}

type DebitObservation struct {
	WriterWithdrawn, TransferAttempted, TransferWriteComplete bool
	TransferBytes, TransferWritten                            int
	TransferWrites                                            uint32
	ID, LaunchKey                                             string
	CommitmentReturned, WriterAuthoritySent, OutcomeUnknown   bool
	NativeOutcome                                             string
}

// Readback from THIS actual retained original, including its original fsync
// return. It submits no native write/retry and cannot reconstruct a debit from
// a stored record, upgrade uncertain fsync or grant root/reference release.
func (p *ParentPending) LookupOriginalDebit(debit *OriginalDebit) (DebitObservation, error) {
	if p == nil || p.owner == nil || p.owner.parent != p {
		return DebitObservation{}, ErrInvalid
	}
	o := p.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if debit == nil || o.debit != debit || debit.owner != o || debit.record == nil {
		return DebitObservation{}, ErrInvalid
	}
	committed := o.prepared && o.append != nil && o.append.owner == o && o.append.committed
	v := DebitObservation{ID: debit.record.ID, LaunchKey: debit.record.LaunchKey, CommitmentReturned: committed, WriterWithdrawn: o.writerWithdrawn, WriterAuthoritySent: o.writerSent, OutcomeUnknown: o.active != nil || o.firstError != nil || (o.p1 != nil && o.writerWithdrawn), NativeOutcome: "unknown"}
	if call := o.channel; call != nil {
		v.TransferAttempted = true
		v.TransferBytes = len(call.bytes)
		v.TransferWritten = call.transferred
		v.TransferWrites = call.writes
		v.TransferWriteComplete = call.writeComplete
		if !call.writeComplete || call.firstError != nil {
			v.OutcomeUnknown = true
		}
	}
	return v, nil
}

// This file provides no profile creation, operator apply, child command, live
// core or source-record-as-grant shortcut. Root assembly must consume the actual
// original debit and fixed inherited-descriptor handoff before any core spawn.
