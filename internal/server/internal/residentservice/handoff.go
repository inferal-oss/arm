package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"syscall"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"golang.org/x/sys/unix"
)

type bootstrap struct {
	Schema    uint32   `json:"schema"`
	Home      string   `json:"home"`
	Build     Build    `json:"build"`
	DebitID   string   `json:"debit_id"`
	LaunchKey string   `json:"launch_key"`
	Secret    [32]byte `json:"secret"` // original inherited channel ONLY, never journal/argv/logs
}
type channelOperation struct {
	writes        uint32
	writeComplete bool
	owner         *owner
	bytes         []byte
	transferred   int
	firstError    error
}

// ChildPending is constructed only by the fixed private-core entry on the
// retained original inherited descriptors. It does not reopen a grant/debit.
type ChildPending struct{ owner *owner }
type ManagerCharge struct {
	owner *owner
	child *ChildPending
}
type ChargeObservation struct {
	Home                   string // original-bound private view only; never in public consumer projection
	HomeID                 NativeID
	DebitID, ProfileSHA256 string
	Resources              Resources
	AdmissionAvailable     bool
	NativeOutcome          string
}

func (p *ParentPending) PrepareOriginalChannel(ctx context.Context, debit *OriginalDebit) (extra []*os.File, resultErr error) {
	op, err := p.begin("channel")
	if err != nil {
		return nil, err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o := p.owner
	o.mu.Lock()
	resultErr = debit.validateOriginalLocked(o)
	o.mu.Unlock()
	if resultErr == nil {
		resultErr = o.prepareChannelBody(ctx)
	}
	if resultErr == nil {
		extra = []*os.File{o.files.childChannel.file, o.files.home.file, o.files.lock.file, o.files.journal.file}
	}
	normal = true
	return extra, resultErr
}

func (o *owner) prepareChannelBody(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.files.capacity(2); err != nil {
		return err
	}
	o.files.progress("original_service_socketpair_pending")
	// Darwin lacks SOCK_CLOEXEC. Serialize creation/CLOEXEC with Go's real fork
	// gate, and retain BOTH raw originals before either fcntl can fail.
	err := func() error {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			return err
		}
		o.files.parentChannel = o.files.registerRaw(pair[0], "original-service-parent-channel")
		o.files.childChannel = o.files.registerRaw(pair[1], "original-service-child-channel")
		o.files.progress("original_socketpair_cloexec_pending")
		for _, fd := range pair {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
				return err
			}
			if o.p1 != nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := unix.SetNonblock(fd, true); err != nil {
					return err
				}
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if err = o.files.materialize(o.files.parentChannel); err != nil {
		return err
	}
	return o.files.materialize(o.files.childChannel)
}

const FixedCoreRole = mesh.ResidentP1CoreRole

// The actual image object/command are retained before native proof inspection.
// Only this fixed same-binary role and these exact inherited originals may start.
func (p *ParentPending) BindOriginalCommand(ctx context.Context, debit *OriginalDebit, command *exec.Cmd, image *mesh.RuntimeExecutable) (resultErr error) {
	op, err := p.begin("command-binding")
	if err != nil {
		return err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o := p.owner
	o.mu.Lock()
	resultErr = debit.validateOriginalLocked(o)
	o.command = command
	o.image = image
	o.mu.Unlock()
	if resultErr == nil {
		resultErr = o.bindCommandBody(ctx, command, image)
	}
	normal = true
	return resultErr
}
func (o *owner) bindCommandBody(ctx context.Context, command *exec.Cmd, image *mesh.RuntimeExecutable) error {
	if ctx == nil || command == nil || image == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.validateCommandSelectors(command); err != nil {
		return err
	}
	// Direct regular-file stdio (or Go's default null stream) keeps logs alive
	// after the launcher returns; no unretained copy goroutine/pipe/EPIPE lifetime.
	for _, writer := range []any{command.Stdout, command.Stderr} {
		if writer == nil {
			continue
		}
		file, ok := writer.(*os.File)
		if !ok {
			return ErrInvalid
		}
		var stat unix.Stat_t
		if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
			return err
		}
		if uint32(stat.Mode) != uint32(unix.S_IFREG)|0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			return ErrInvalid
		}
		for _, owned := range []*retainedFD{o.files.lock, o.files.journal} {
			if nativeID(&stat) == owned.id {
				return ErrInvalid
			}
		}
	}

	proof, err := image.Proof()
	if err != nil {
		return err
	}
	if proof.SHA256 != o.build.SHA256 || proof.Size != o.build.Size {
		return ErrInvalid
	}
	path, err := image.VerifiedPath()
	if err != nil {
		return err
	}
	if command.Path != path {
		return ErrInvalid
	}
	return nil
}
func (o *owner) validateCommandSelectors(command *exec.Cmd) error {
	if command != nil && (command.Dir != "/" || !reflect.DeepEqual(command.SysProcAttr, &syscall.SysProcAttr{Setsid: true})) {
		return ErrInvalid
	}
	if command == nil || len(command.Args) != 2 || command.Args[0] != command.Path || command.Args[1] != FixedCoreRole || command.Cancel != nil || command.WaitDelay != 0 || command.Stdin != nil {
		return ErrInvalid
	}
	if o.files.childChannel == nil || len(command.ExtraFiles) != 4 {
		return ErrInvalid
	}
	want := []*os.File{o.files.childChannel.file, o.files.home.file, o.files.lock.file, o.files.journal.file}
	for i, actual := range command.ExtraFiles {
		if actual != want[i] {
			return ErrInvalid
		}
	}
	return nil
}

// The retained owner calls actual Cmd.Start itself: a caller-supplied nil error
// or Process{Pid:...} cannot manufacture an observed original process birth.
// There is no caller-context automatic kill or lifetime deadline on this Cmd.
func (p *ParentPending) StartOriginalCommand(ctx context.Context, command *exec.Cmd) (resultErr error) {
	op, err := p.begin("start")
	if err != nil {
		return err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o := p.owner
	if ctx == nil {
		resultErr = ErrInvalid
	} else if resultErr = ctx.Err(); resultErr == nil {
		if command != o.command || command.Process != nil || command.ProcessState != nil {
			resultErr = ErrInvalid
		} else if resultErr = o.bindCommandBody(ctx, command, o.image); resultErr == nil {
			o.files.progress("actual_original_core_Cmd_Start_pending")
			resultErr = command.Start()
			o.mu.Lock()
			o.startRecorded = true
			o.startError = resultErr
			o.mu.Unlock()
			if resultErr == nil && command.Process == nil {
				resultErr = ErrUnresolved
			}
		}
	}
	normal = true
	return resultErr
}

func (p *ParentPending) SendOriginalWriterHandoff(ctx context.Context, debit *OriginalDebit) (resultErr error) {
	op, err := p.begin("writer-handoff")
	if err != nil {
		return err
	}
	normal := false
	defer p.owner.finish(op, &normal, &resultErr)
	o := p.owner
	o.mu.Lock()
	resultErr = debit.validateOriginalLocked(o)
	o.mu.Unlock()
	if resultErr == nil {
		body := bootstrap{Schema: 1, Home: o.path, Build: o.build, DebitID: debit.record.ID, LaunchKey: debit.record.LaunchKey, Secret: o.secret}
		payload, err := json.Marshal(body)
		if err != nil {
			resultErr = err
		} else {
			resultErr = o.sendBootstrap(ctx, payload)
		}
	}
	normal = true
	return resultErr
}

func (o *owner) sendBootstrap(ctx context.Context, payload []byte) error {
	if ctx == nil || len(payload) == 0 || len(payload) > MaxPayloadBytes {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame := make([]byte, 4+32+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	sum := sha256.Sum256(payload)
	copy(frame[4:36], sum[:])
	copy(frame[36:], payload)
	call := &channelOperation{owner: o, bytes: frame}
	o.mu.Lock()
	o.channel = call
	o.writerWithdrawn = true
	o.writerSent = true
	o.mu.Unlock() // permanently withdraw parent writer BEFORE ANY send
	for at, calls := 0, 0; at < len(frame); calls++ {
		if err := ctx.Err(); err != nil {
			return o.channelFailure(call, err)
		}
		if calls >= 32 {
			return o.channelFailure(call, io.ErrShortWrite)
		}
		o.files.progress("original_writer_handoff_channel_write_pending")
		o.mu.Lock()
		call.writes++
		o.mu.Unlock()
		n, err := o.files.parentChannel.file.Write(frame[at:])
		at += n
		o.mu.Lock()
		call.transferred = at
		o.mu.Unlock()
		if err != nil {
			return o.channelFailure(call, err)
		}
		if n == 0 {
			return o.channelFailure(call, io.ErrNoProgress)
		}
	}
	o.mu.Lock()
	call.writeComplete = true
	o.mu.Unlock()
	return nil
}
func (o *owner) channelFailure(call *channelOperation, err error) error {
	o.mu.Lock()
	if call.firstError == nil {
		call.firstError = err
	}
	o.mu.Unlock()
	return err
}

func NewFixedChildPending() *ChildPending {
	c := &ChildPending{}
	o := &owner{child: c}
	c.owner = o
	o.files = &nativeFiles{owner: o}
	return c
}

func (c *ChildPending) ReceiveOriginal(ctx context.Context) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c {
		return ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	if o.p1Child != nil {
		o.mu.Unlock()
		return ErrInvalid
	}
	if o.active != nil {
		o.mu.Unlock()
		return ErrPending
	}
	if o.receiveTried {
		o.mu.Unlock()
		return ErrInvalid
	}
	o.receiveTried = true
	op := &operation{owner: o, kind: "receive", phase: "registered_before_fixed_descriptor_effect"}
	o.active = op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.receiveBody(ctx)
	normal = true
	return resultErr
}

func (o *owner) receiveBody(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := residentrefs.ClaimLegacyReceiverExclusion(); err != nil {
		return err
	}
	// Fixed slots correspond EXACTLY to parent's actual Cmd.ExtraFiles order.
	// Every raw inherited original is registered before materializing any one.
	o.files.childChannel = o.files.registerRaw(3, "original-inherited-service-channel")
	o.files.home = o.files.registerRaw(4, "original-inherited-service-home")
	o.files.lock = o.files.registerRaw(5, "original-inherited-service-lock")
	o.files.journal = o.files.registerRaw(6, "original-inherited-service-journal")
	// Original inherited descriptors become CLOEXEC before they can reach any
	// later worker/helper exec; only the trusted core retains these references.
	if err := func() error {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		for _, original := range []*retainedFD{o.files.childChannel, o.files.home, o.files.lock, o.files.journal} {
			if _, err := unix.FcntlInt(uintptr(original.number), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
				return err
			}
		}
		return nil
	}(); err != nil {
		return err
	}
	for _, original := range []*retainedFD{o.files.childChannel, o.files.home, o.files.lock, o.files.journal} {
		if err := o.files.materialize(original); err != nil {
			return err
		}
	}
	if o.files.childChannel.mode&unix.S_IFMT != unix.S_IFSOCK {
		return ErrInvalid
	}
	for _, item := range []struct {
		f   *retainedFD
		dir bool
	}{{o.files.home, true}, {o.files.lock, false}, {o.files.journal, false}} {
		if err := privateNative(item.f, item.dir); err != nil {
			return err
		}
	}
	// NEVER Flock, reopen, create, reset or adopt by pathname here. Original FD
	// lineage comes from fixed trusted parent/core entry, authenticated below.
	payload, err := o.readBootstrap(ctx)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var boot bootstrap
	if err = decoder.Decode(&boot); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	canonical, err := json.Marshal(boot)
	if err != nil || !bytes.Equal(canonical, payload) || boot.Schema != 1 {
		return ErrInvalid
	}
	o.path, o.build = boot.Home, boot.Build
	journal, err := o.readOriginalJournal(ctx)
	if err != nil {
		return err
	}
	if len(journal.records) != 2 {
		return ErrUnresolved
	}
	grant, record := journal.records[0].Grant, journal.records[1].Debit
	if grant.Intent.Home != boot.Home || grant.Intent.HomeID != o.files.home.id || grant.Intent.Profile.Build != boot.Build || record.ID != boot.DebitID || record.LaunchKey != boot.LaunchKey || record.SecretCommitment != digest(boot.Secret[:]) || record.LockID != o.files.lock.id || record.JournalID != o.files.journal.id {
		return ErrInvalid
	}
	if boot.Build.GOOS != runtime.GOOS || boot.Build.GOARCH != runtime.GOARCH || boot.Build.GoBuild != runtime.Version() {
		return ErrInvalid
	}
	if err = o.files.validateCurrent(o.path); err != nil {
		return err
	}
	o.mu.Lock()
	o.journal = journal
	o.grant = grant
	o.secret = boot.Secret
	o.debit = &OriginalDebit{owner: o, record: record}
	o.prepared = true
	o.opened = true
	o.mu.Unlock()
	return nil
}

func (o *owner) readBootstrap(ctx context.Context) ([]byte, error) {
	call := &channelOperation{owner: o, bytes: make([]byte, 36)}
	o.mu.Lock()
	o.channel = call
	o.mu.Unlock()
	read := func(want int) error {
		for calls := 0; call.transferred < want; calls++ {
			if err := ctx.Err(); err != nil {
				return o.channelFailure(call, err)
			}
			if calls >= 32 {
				return o.channelFailure(call, io.ErrNoProgress)
			}
			o.files.progress("original_writer_handoff_channel_read_pending")
			o.mu.Lock()
			at := call.transferred
			target := call.bytes
			o.mu.Unlock()
			n, err := o.files.childChannel.file.Read(target[at:want])
			o.mu.Lock()
			call.transferred += n
			o.mu.Unlock()
			if err != nil {
				return o.channelFailure(call, err)
			}
			if n == 0 {
				return o.channelFailure(call, io.ErrNoProgress)
			}
		}
		return nil
	}
	if err := read(36); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(call.bytes[:4])
	if length == 0 || length > MaxPayloadBytes {
		return nil, ErrInvalid
	}
	o.mu.Lock()
	call.bytes = append(call.bytes, make([]byte, int(length))...)
	o.mu.Unlock()
	if err := read(36 + int(length)); err != nil {
		return nil, err
	}
	payload := call.bytes[36:]
	sum := sha256.Sum256(payload)
	if !bytes.Equal(sum[:], call.bytes[4:36]) {
		return nil, ErrInvalid
	}
	return payload, nil
}

// Actual image pointer is retained before Proof's native checks. A record/build
// hash or caller-supplied approved flag cannot substitute this concrete image.
func (c *ChildPending) BindOriginalRuntime(ctx context.Context, image *mesh.RuntimeExecutable) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c || image == nil {
		return ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	if o.active != nil {
		o.mu.Unlock()
		return ErrPending
	}
	if o.firstError != nil || !o.prepared || o.image != nil {
		o.mu.Unlock()
		return ErrInvalid
	}
	o.image = image
	op := &operation{owner: o, kind: "runtime", phase: "actual_runtime_binding_pending"}
	o.active = op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	if ctx == nil {
		resultErr = ErrInvalid
	} else if resultErr = ctx.Err(); resultErr == nil {
		proof, err := image.Proof()
		resultErr = err
		if resultErr == nil && (proof.SHA256 != o.build.SHA256 || proof.Size != o.build.Size) {
			resultErr = ErrInvalid
		}
		if resultErr == nil {
			o.mu.Lock()
			o.runtimeBound = true
			o.mu.Unlock()
		}
	}
	normal = true
	return resultErr
}

func (c *ChildPending) ClaimOriginalManagerCharge() (*ManagerCharge, error) {
	if c == nil || c.owner == nil || c.owner.child != c {
		return nil, ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrPending
	}
	if o.firstError != nil || !o.prepared || !o.runtimeBound || o.charge != nil {
		return nil, ErrInvalid
	}
	charge := &ManagerCharge{owner: o, child: c}
	o.charge = charge
	return charge, nil
}

// This is a durable observation written by the SAME inherited original writer.
// The trusted retained root must validate its actual native/Manager/database/
// Server/image bindings before supplying this observation. The serialized marker
// is neither Ready nor handoff permission and creates no additional debit/grant.
func (c *ChildPending) CommitRootBoundObservation(ctx context.Context, generation, commitmentSHA string) (resultErr error) {
	if c == nil || c.owner == nil || c.owner.child != c {
		return ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	if o.active != nil {
		o.mu.Unlock()
		return ErrPending
	}
	if o.firstError != nil {
		err := o.firstError
		o.mu.Unlock()
		return err
	}
	if !o.prepared || !o.runtimeBound || o.charge == nil || len(o.journal.records) != 2 || o.append != nil {
		o.mu.Unlock()
		return ErrInvalid
	}
	op := &operation{owner: o, kind: "root-observation", phase: "registered_before_original_root_observation"}
	o.active = op
	record := Record{Schema: 1, Sequence: 3, Kind: "root-bound", Root: &RootBoundRecord{DebitID: o.debit.record.ID, LaunchKey: o.debit.record.LaunchKey, Generation: generation, CommitmentSHA256: commitmentSHA}}
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	if ctx == nil {
		resultErr = ErrInvalid
	} else if resultErr = ctx.Err(); resultErr == nil {
		resultErr = o.appendOriginal(ctx, record)
	}
	normal = true
	return resultErr
}

// Pure original-bound observation for Manager.mu admission: no native syscall,
// DB read or new service grant. Failure retains the full prior promise; it only
// closes future admission. Existing reporting/settlement does not use this gate.
func (c *ManagerCharge) ObserveOriginal() (ChargeObservation, error) {
	if c == nil || c.owner == nil {
		return ChargeObservation{}, ErrInvalid
	}
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.child != c.child || o.charge != c || o.grant == nil || o.debit == nil {
		return ChargeObservation{}, ErrInvalid
	}
	v := ChargeObservation{Home: o.path, HomeID: o.serviceHomeOriginalLocked(), DebitID: o.debit.record.ID, ProfileSHA256: o.debit.record.ProfileSHA256, Resources: o.grant.Intent.Profile.Resources, NativeOutcome: "unknown"}
	v.AdmissionAvailable = o.prepared && o.runtimeBound && o.firstError == nil && o.active == nil
	if o.p1Child != nil {
		shared := o.p1Child.InspectOriginal()
		v.AdmissionAvailable = v.AdmissionAvailable && shared.Delegated && shared.Materialized == 3 && shared.FirstError == nil && !shared.OperationPending
	}
	if !v.AdmissionAvailable {
		return v, ErrUnresolved
	}
	return v, nil
}

// There is no parent reference-release shortcut, root ACK-as-grant, journal
// repair, new-debit reconstruction, lifetime expiry or native/resource Closed.
