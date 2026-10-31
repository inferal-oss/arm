package residentservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// P1ServiceGroup is a concrete nonclosing original service/channel group for one
// composed thirteen-reference launch. It has NO File/Close/Start/command getter.
// The service registry remains sole closer; command slots are nonclosing aliases.
// Root must retain this ACTUAL group/debit before runtime/image/fence effects.
type P1ServiceGroup struct{ *p1ServiceState }
type p1ServiceState struct {
	environment                                                      []string
	self                                                             *P1ServiceGroup
	parent                                                           *ParentPending
	debit                                                            *OriginalDebit
	command                                                          *exec.Cmd
	extra                                                            []*os.File
	fundingTried, fundingChecked, bindTried, bound, beforeStartTried bool
}
type P1ServiceSelectors struct {
	Home, DebitID, LaunchKey, ProfileSHA256 string
	HomeID, LockID, JournalID               NativeID
	Build                                   Build
	Resources                               Resources
	// DATA only: never a transferable grant or proof of native/process closure.
}

// Pure identity registration after the REAL original debit's committed append.
// It cannot reconstruct a debit from selectors/records or adopt an old4FD run.
func NewP1ServiceGroup(parent *ParentPending, debit *OriginalDebit, command *exec.Cmd) (*P1ServiceGroup, error) {
	if parent == nil || parent.owner == nil || parent.owner.parent != parent || command == nil || len(command.ExtraFiles) != 13 {
		return nil, ErrInvalid
	}
	o := parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil || o.p1 != nil || o.channelTried || o.command != nil || o.startTried || o.writerTried || o.writerWithdrawn || debit.validateOriginalLocked(o) != nil || o.grant == nil || o.append == nil || !o.append.committed {
		return nil, ErrInvalid
	}
	if !filepath.IsAbs(o.path) || filepath.Clean(o.path) != o.path || o.path == "/" || len(strings.Split(strings.TrimPrefix(o.path, "/"), "/")) > 64 || o.grant.Intent.Profile.Bounds.Descriptors < 13 {
		return nil, ErrInvalid
	}
	if o.grant.Intent.Profile.RequireP1Environment() != nil {
		return nil, ErrInvalid
	}
	p := &P1ServiceGroup{&p1ServiceState{parent: parent, debit: debit, command: command, extra: command.ExtraFiles, environment: append([]string(nil), o.grant.Intent.Profile.RuntimeEnvironment.Entries...)}}
	p.self = p
	o.p1 = p
	return p, nil
}
func (p *P1ServiceGroup) begin(kind string) (*operation, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return nil, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	valid := o.p1 == p && o.parent == p.parent && p.debit.validateOriginalLocked(o) == nil
	o.mu.Unlock()
	if !valid {
		return nil, ErrInvalid
	}
	return p.parent.begin(kind)
}
func (p *P1ServiceGroup) ValidateFundingOriginal(ctx context.Context) (resultErr error) {
	op, err := p.begin("p1-funding")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.owner.finish(op, &normal, &resultErr)
	resultErr = p.checkFunding(ctx)
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) checkFunding(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o := p.parent.owner
	o.mu.Lock()
	err := p.debit.validateOriginalLocked(o)
	valid := o.grant != nil && o.append != nil && o.append.owner == o && o.append.committed
	o.mu.Unlock()
	if err != nil {
		return err
	}
	if p.command.Env == nil || !equalEnvironmentEntries(p.command.Env, p.environment) {
		return ErrInvalid
	}
	if !valid || o.grant.Intent.Profile.RequireP1Environment() != nil {
		return ErrInvalid
	}
	if err := o.files.validateCurrent(o.path); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return err
	}
	if len(journal.records) != 2 || journal.last != o.journal.last {
		return ErrUnresolved
	}
	return ctx.Err()
}

// These selectors come from this retained original but are DATA, not funding.
// Actual root retains the opaque ParentPending/OriginalDebit/group together.
func (p *P1ServiceGroup) SelectorsOriginal() (P1ServiceSelectors, error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return P1ServiceSelectors{}, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || !p.fundingChecked || p.debit.validateOriginalLocked(o) != nil {
		return P1ServiceSelectors{}, ErrInvalid
	}
	return P1ServiceSelectors{Home: o.path, DebitID: p.debit.record.ID, LaunchKey: p.debit.record.LaunchKey, ProfileSHA256: p.debit.record.ProfileSHA256, HomeID: o.files.home.id, LockID: o.files.lock.id, JournalID: o.files.journal.id, Build: o.build, Resources: o.grant.Intent.Profile.Resources}, nil
}
func (p *P1ServiceGroup) PrepareOriginalChannel(ctx context.Context) (resultErr error) {
	op, err := p.begin("p1-channel")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.owner.finish(op, &normal, &resultErr)
	resultErr = p.parent.owner.prepareChannelBody(ctx)
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) commandSelectors(bound bool) error {
	o := p.parent.owner
	c := p.command
	if c == nil || c.Process != nil || c.ProcessState != nil || c.Cancel != nil || c.WaitDelay != 0 || c.Stdin != nil || c.Dir != "/" || !reflect.DeepEqual(c.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) {
		return ErrInvalid
	}
	if c.Env == nil || !equalEnvironmentEntries(c.Env, p.environment) {
		return ErrInvalid
	}
	expected := filepath.Join(o.path, "resident-images", "pin-"+p.debit.record.ID, "arm")
	if c.Path != expected || len(c.Args) != 2 || c.Args[0] != expected || c.Args[1] != FixedCoreRole || len(c.ExtraFiles) != 13 || len(p.extra) != 13 || &c.ExtraFiles[0] != &p.extra[0] {
		return ErrInvalid
	}
	want := []*os.File{o.files.childChannel.file, o.files.home.file, o.files.lock.file, o.files.journal.file}
	for i := 0; i < 4; i++ {
		if bound && c.ExtraFiles[i] != want[i] || !bound && c.ExtraFiles[i] != nil {
			return ErrInvalid
		}
	}
	return nil
}
func (p *P1ServiceGroup) checkChannel(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o := p.parent.owner
	entry := o.files.childChannel
	if entry == nil || entry.file == nil || entry.closeTried {
		return ErrInvalid
	}
	o.files.progress("original_P1_service_channel_native_validation")
	var stat unix.Stat_t
	if err := unix.Fstat(entry.number, &stat); err != nil {
		return err
	}
	if nativeID(&stat) != entry.id || uint32(stat.Mode)&unix.S_IFMT != unix.S_IFSOCK {
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
	for _, reference := range []*retainedFD{entry, o.files.home, o.files.lock, o.files.journal} {
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(reference.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
	}
	return nil
}
func (p *P1ServiceGroup) BindOriginal(ctx context.Context) (resultErr error) {
	op, err := p.begin("p1-binding")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.owner.finish(op, &normal, &resultErr)
	resultErr = p.checkFunding(ctx)
	if resultErr == nil {
		resultErr = p.checkChannel(ctx)
	}
	if resultErr == nil {
		resultErr = p.commandSelectors(false)
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		o := p.parent.owner
		// SAME original references, not wrappers and not transferred close ownership.
		copy(p.command.ExtraFiles[:4], []*os.File{o.files.childChannel.file, o.files.home.file, o.files.lock.file, o.files.journal.file})
		o.mu.Lock()
		o.command = p.command
		o.mu.Unlock()
	}
	normal = true
	return resultErr
}

// Withdrawal may precede native Start: failure afterward leaves the debit fully
// retained/charged and cannot restore parent writing or authorize another birth.
// Composed root must withdraw BOTH this and actual launch-journal writer before
// ANY bootstrap byte. This method sends no byte and observes no process birth.
func (p *P1ServiceGroup) WithdrawOriginalWriter(ctx context.Context) (resultErr error) {
	op, err := p.begin("p1-withdraw")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.owner.finish(op, &normal, &resultErr)
	if ctx == nil {
		resultErr = ErrInvalid
	} else if resultErr = ctx.Err(); resultErr == nil {
		o := p.parent.owner
		o.mu.Lock()
		o.writerWithdrawn = true
		o.mu.Unlock()
	}
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) ValidateOriginalBeforeStart(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || command != p.command {
		return ErrInvalid
	}
	op, err := p.begin("p1-before-start")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.owner.finish(op, &normal, &resultErr)
	resultErr = p.checkFunding(ctx)
	if resultErr == nil {
		resultErr = p.checkChannel(ctx)
	}
	if resultErr == nil {
		resultErr = p.commandSelectors(true)
	}
	normal = true
	return resultErr
}
