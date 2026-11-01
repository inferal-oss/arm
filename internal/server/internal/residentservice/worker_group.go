package residentservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"

	"getarm.org/internal/mesh"
	"golang.org/x/sys/unix"
)

// Only the actual committed worker parent/debit can create this group. Raw
// native bookkeeping retains no legacy parent, grant, group or charge fields.
// The trusted root retains its actual command; this group exposes no getter.
type WorkerServiceGroup struct {
	self                *WorkerServiceGroup
	parent              *WorkerParentPending
	debit               *WorkerOriginalDebit
	command             *exec.Cmd
	extra               []*os.File
	environment         []string
	fundingTried        bool
	fundingChecked      bool
	channelTried        bool
	channelReady        bool
	bindTried, bound    bool
	withdrawTried       bool
	writerWithdrawn     bool
	beforeStartTried    bool
	beforeStartChecked  bool
	shortSealTried      bool
	shortSealed         bool
	pinTried            bool
	pin                 *mesh.WorkerResidentImagePinPending
	pinReferences       *mesh.WorkerPinParentReferenceOwner
	pinError            error
	references          *WorkerServiceParentReferenceOwner
	retirement          *workerServiceParentRetirement
	controlPhases       [4]*workerParentControlCall
	ackAuthentication   *WorkerParentAckAuthentication
	challenge           *WorkerParentChallenge
	proofAuthentication *WorkerParentProofAuthentication
}

// All fields are bounded DATA; this neither issues nor reconstructs a debit.
type WorkerServiceSelectorsData struct {
	Home, DebitID, LaunchKey, ProfileSHA256, ContractSHA256 string
	HomeID, LockID, JournalID                               NativeID
	Build                                                   Build
	Resources                                               Resources
	Contract                                                WorkerStandingContract
}

func (w *workerCustody) originalWorkerDebitLocked(debit *WorkerOriginalDebit) bool {
	return w.typedOriginalLocked() && w.legacyEmptyLocked() && w.parent != nil && w.parent.opened && w.parent.debitComplete && debit != nil && debit.self == debit && debit.owner == w && w.debit == debit && debit.record != nil && w.grant != nil && len(w.journal.records) == 2 && w.journal.records[0].Grant == w.grant && w.journal.records[1].Debit == debit.record && w.append != nil && w.append.owner == w && w.append.committed && w.raw.files != nil && w.raw.files.home != nil && w.raw.files.lock != nil && w.raw.files.journal != nil && w.raw.files.home.id == w.grant.Intent.HomeID && w.raw.files.lock.id == debit.record.LockID && w.raw.files.journal.id == debit.record.JournalID
}

// Pure identity registration, after the real native debit Sync. Source Apply
// must have completed in a separate process; no same-process unlock/reopen is
// performed here. Both destinations are retained before channel/pin effects.
func NewWorkerServiceGroupWithParentReferences(parent *WorkerParentPending, debit *WorkerOriginalDebit, command *exec.Cmd) (*WorkerServiceGroup, *WorkerServiceParentReferenceOwner, error) {
	if parent == nil || parent.self != parent || parent.custody == nil || parent.custody.raw == nil || command == nil || len(command.ExtraFiles) != 13 {
		return nil, nil, ErrInvalid
	}
	w := parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if parent.group != nil || w.raw.active != nil || w.raw.firstError != nil || !w.originalWorkerDebitLocked(debit) || w.grant.Intent.Profile.Validate() != nil || w.grant.Intent.Profile.Core.Bounds.Descriptors < 13 {
		return nil, nil, ErrInvalid
	}
	p := &WorkerServiceGroup{parent: parent, debit: debit, command: command, extra: command.ExtraFiles}
	p.self = p
	parent.group = p
	p.environment = append([]string(nil), w.grant.Intent.Profile.Core.RuntimeEnvironment.Entries...)
	r := &WorkerServiceParentReferenceOwner{group: p}
	r.self = r
	p.references = r
	return p, r, nil
}

func (p *WorkerServiceGroup) beginOriginal(kind string) (*operation, error) {
	if p == nil || p.self != p || p.parent == nil || p.parent.self != p.parent || p.parent.custody == nil || p.parent.custody.raw == nil {
		return nil, ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || !w.originalWorkerDebitLocked(p.debit) || p.retirement != nil || w.raw.firstError != nil {
		return nil, ErrInvalid
	}
	if w.raw.active != nil {
		return nil, ErrPending
	}
	switch kind {
	case "worker-group-funding":
		if p.fundingTried {
			return nil, ErrInvalid
		}
		p.fundingTried = true
	case "worker-group-channel":
		if !p.fundingChecked || p.channelTried || p.writerWithdrawn {
			return nil, ErrInvalid
		}
		p.channelTried = true
	case "worker-group-bind":
		if !p.channelReady || p.bindTried || p.writerWithdrawn {
			return nil, ErrInvalid
		}
		p.bindTried = true
	case "worker-group-withdraw":
		if !p.bound || p.withdrawTried {
			return nil, ErrInvalid
		}
		p.withdrawTried = true
		p.writerWithdrawn = true // permanent before cancellation or native checks
	case "worker-group-before-start":
		if !p.bound || !p.writerWithdrawn || p.beforeStartTried {
			return nil, ErrInvalid
		}
		p.beforeStartTried = true
	case "worker-group-short-seal":
		if !p.beforeStartChecked || p.shortSealTried {
			return nil, ErrInvalid
		}
		p.shortSealTried = true
	case "worker-parent-bootstrap-write", "worker-parent-ack-read", "worker-parent-challenge-write", "worker-parent-proof-read":
		if !p.shortSealed || !p.writerWithdrawn || p.command == nil || p.command.Process == nil {
			return nil, ErrInvalid
		}
	case "worker-parent-ack-auth":
		if p.ackAuthentication != nil || !p.parentControlCompleteLocked(p.controlPhases[workerParentBoundAck], workerParentBoundAck) {
			return nil, ErrInvalid
		}
	case "worker-parent-challenge-sign":
		if !p.ackCompletedLocked() || p.challenge != nil || p.controlPhases[workerParentChallenge] != nil {
			return nil, ErrInvalid
		}
	case "worker-parent-proof-auth":
		if !p.challengeCompletedLocked() || p.proofAuthentication != nil || !p.parentControlCompleteLocked(p.controlPhases[workerParentProof], workerParentProof) {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	return w.operationLocked(kind)
}

func (p *WorkerServiceGroup) fundingBodyOriginal(ctx context.Context, readCurrentJournal bool) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	valid := p.parent.group == p && w.originalWorkerDebitLocked(p.debit)
	w.raw.mu.Unlock()
	if !valid || p.command.Env == nil || !equalEnvironmentEntries(p.command.Env, p.environment) {
		return ErrInvalid
	}
	if err := w.grant.Intent.Profile.Core.RuntimeEnvironment.MatchesActual(p.environment); err != nil {
		return err
	}
	if err := w.raw.files.validateCurrent(w.path); err != nil {
		return err
	}
	if readCurrentJournal {
		// Parent reads remain bounded by four: open grant, pre-debit grant,
		// this final pre-Start debit and the later actual RootBound receipt.
		current, err := w.readJournalOriginal(ctx)
		if err != nil {
			return err
		}
		if len(current.records) != 2 || current.bytes != w.journal.bytes || current.last != w.journal.last {
			return ErrUnresolved
		}
	} else {
		// Authority is the same original committed debit, not this size check.
		// Full current bytes are revalidated once immediately before native Start.
		info, err := w.journalStatOriginal(ctx)
		if err != nil {
			return err
		}
		if info.Size() != w.journal.bytes {
			return ErrUnresolved
		}
	}
	return ctx.Err()
}

func (p *WorkerServiceGroup) ValidateFundingOriginal(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-group-funding")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.fundingBodyOriginal(ctx, false)
	if resultErr == nil {
		p.parent.custody.raw.mu.Lock()
		p.fundingChecked = true
		p.parent.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) SelectorsOriginal() (WorkerServiceSelectorsData, error) {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || p.parent.custody.raw == nil {
		return WorkerServiceSelectorsData{}, ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || !p.fundingChecked || w.raw.active != nil || w.raw.firstError != nil || !w.originalWorkerDebitLocked(p.debit) {
		return WorkerServiceSelectorsData{}, ErrInvalid
	}
	d := p.debit.record
	return WorkerServiceSelectorsData{Home: w.path, DebitID: d.ID, LaunchKey: d.LaunchKey, ProfileSHA256: d.ProfileSHA256, ContractSHA256: d.ContractSHA256, HomeID: w.grant.Intent.HomeID, LockID: d.LockID, JournalID: d.JournalID, Build: w.grant.Intent.Profile.Core.Build, Resources: w.grant.Intent.Profile.Core.Resources, Contract: w.grant.Intent.Profile.Contract}, nil
}

// Identity validation for the actual root-held command. No command or process
// can be recovered through this surface, and it does not establish native Start.
func (p *WorkerServiceGroup) CheckOriginalCommand(command *exec.Cmd) error {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || p.parent.custody.raw == nil {
		return ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || command == nil || command != p.command || command.Process != nil || command.ProcessState != nil || len(command.ExtraFiles) != 13 || len(p.extra) != 13 || &command.ExtraFiles[0] != &p.extra[0] || p.retirement != nil || w.raw.active != nil || w.raw.firstError != nil || !p.fundingChecked || !w.originalWorkerDebitLocked(p.debit) {
		return ErrInvalid
	}
	return nil
}

func (p *WorkerServiceGroup) commandSelectorsOriginal(bound bool) error {
	w := p.parent.custody
	c := p.command
	if c == nil || c.Process != nil || c.ProcessState != nil || c.Cancel != nil || c.WaitDelay != 0 || c.Stdin != nil || c.Dir != "/" || !reflect.DeepEqual(c.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) || !equalEnvironmentEntries(c.Env, p.environment) {
		return ErrInvalid
	}
	expected := filepath.Join(w.path, "resident-images", "pin-"+p.debit.record.ID, "arm")
	if c.Path != expected || len(c.Args) != 2 || c.Args[0] != expected || c.Args[1] != WorkerCoreRole || len(c.ExtraFiles) != 13 || len(p.extra) != 13 || &c.ExtraFiles[0] != &p.extra[0] {
		return ErrInvalid
	}
	want := [4]*retainedFD{w.raw.files.childChannel, w.raw.files.home, w.raw.files.lock, w.raw.files.journal}
	for i, entry := range want {
		if entry == nil || entry.file == nil || entry.closeTried || bound && c.ExtraFiles[i] != entry.file || !bound && c.ExtraFiles[i] != nil {
			return ErrInvalid
		}
	}
	return nil
}

func (p *WorkerServiceGroup) channelBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	n := p.parent.custody.raw.files
	for _, entry := range []*retainedFD{n.parentChannel, n.childChannel} {
		if entry == nil || entry.file == nil || entry.closeTried {
			return ErrInvalid
		}
		n.progress("worker_original_channel_native_validation")
		var st unix.Stat_t
		if err := unix.Fstat(entry.number, &st); err != nil {
			return err
		}
		if nativeID(&st) != entry.id || uint32(st.Mode)&unix.S_IFMT != unix.S_IFSOCK {
			return ErrInvalid
		}
		kind, err := unix.GetsockoptInt(entry.number, unix.SOL_SOCKET, unix.SO_TYPE)
		if err != nil {
			return err
		}
		if kind != unix.SOCK_STREAM {
			return ErrInvalid
		}
		address, err := unix.Getsockname(entry.number)
		if err != nil {
			return err
		}
		if _, ok := address.(*unix.SockaddrUnix); !ok {
			return ErrInvalid
		}
		flags, err := unix.FcntlInt(uintptr(entry.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
	}
	return ctx.Err()
}

func (p *WorkerServiceGroup) BindOriginal(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-group-bind")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.fundingBodyOriginal(ctx, false)
	if resultErr == nil {
		resultErr = p.channelBodyOriginal(ctx)
	}
	if resultErr == nil {
		resultErr = p.commandSelectorsOriginal(false)
	}
	if resultErr == nil {
		n := p.parent.custody.raw.files
		copy(p.command.ExtraFiles[:4], []*os.File{n.childChannel.file, n.home.file, n.lock.file, n.journal.file})
		p.parent.custody.raw.mu.Lock()
		p.bound = true
		p.parent.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) WithdrawOriginalWriter(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-group-withdraw")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = workerContextData(ctx)
	if resultErr == nil {
		p.parent.custody.raw.mu.Lock()
		p.writerWithdrawn = true
		p.parent.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) ValidateOriginalBeforeStart(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.self != p || command != p.command {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-group-before-start")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.fundingBodyOriginal(ctx, true)
	if resultErr == nil {
		resultErr = p.channelBodyOriginal(ctx)
	}
	if resultErr == nil {
		resultErr = p.commandSelectorsOriginal(true)
	}
	if resultErr == nil {
		p.parent.custody.raw.mu.Lock()
		p.beforeStartChecked = true
		p.parent.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}
