// Package residentbirth owns the original composed parent's fresh namespace.
// This source supplies no launch, standing grant, child ACK or native closure.
package residentbirth

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"getarm.org/internal/mesh"
	"getarm.org/internal/server/internal/residentservice"
	"golang.org/x/sys/unix"
)

const workerMeshCoreRole = mesh.ResidentWorkerCoreRole
const maxWorkerFenceFDs = 8

// Four <=65-open canonical walks, the initial tree census/creations, and
// three bounded post-create inventories require at most 280 recorded opens.
const maxWorkerFenceHistory = 288
const maxWorkerIntentBytes = (16 << 10) - 36

type WorkerFreshFencePending struct{ owner *workerFenceOwner }
type WorkerPreparedFence struct{ owner *workerFenceOwner }
type workerFenceOwner struct {
	parentReferences                                            *WorkerFenceParentReferenceOwner
	parentRetirement                                            *workerFenceParentRetirement
	mu                                                          sync.Mutex // publication only, never held through native calls
	self                                                        *WorkerFreshFencePending
	path                                                        string
	uid                                                         uint32
	group                                                       *residentservice.WorkerServiceGroup
	pin                                                         *mesh.WorkerResidentImagePinPending
	command                                                     *exec.Cmd
	extra                                                       []*os.File
	service                                                     residentservice.WorkerServiceSelectorsData
	image                                                       mesh.ResidentImageInventory
	state                                                       *workerFenceNative
	active                                                      *workerFenceOperation
	prepared                                                    *WorkerPreparedFence
	prepareTried, bindTried, bound, beforeStartTried            bool
	journalIssued, writeTried, intentCommitted, writerWithdrawn bool
	shortSealTried                                              bool
	withdrawOriginal, shortSealOriginal                         *workerFenceOperation
	postStartTried                                              bool
	postStart                                                   *workerParentPostStartCall
	journal                                                     *WorkerLaunchJournal
	intent                                                      []byte
	verification                                                []byte
	writes                                                      []*workerFenceWrite
	written                                                     int
	firstError                                                  error
	opened, closed                                              uint64
	live                                                        int
	createdDirectory, createdLedger                             bool
}
type workerFenceOperation struct {
	owner          *workerFenceOwner
	ctx            context.Context
	kind, phase    string
	abnormal       bool
	normalFinished bool // final original metadata, not native closure
	finalError     error
	cause          any
}
type workerFenceFD struct {
	file               *os.File
	number, slot       int
	id                 fenceIdentity
	closeTried, closed bool
	closeError         error
}
type workerFenceNative struct {
	owner                                            *workerFenceOwner
	slots                                            [maxWorkerFenceFDs]*workerFenceFD
	history                                          []*workerFenceFD // bounded retained originals, including known auxiliary closes
	home, launch, daemon, directory, ledger, journal *workerFenceFD
	opened, closed                                   uint64
	createdDirectory, createdLedger                  bool
}
type WorkerFenceInspection struct {
	PrepareAttempted, Prepared, Bound, WriterWithdrawn   bool
	IntentCommitted, OperationPending, OperationAbnormal bool
	OperationKind, OperationPhase                        string
	Opened, Closed                                       uint64
	LastPublishedLiveFDs, WrittenBytes                   int
	DirectoryCreated, LedgerCreated                      bool
	FirstError                                           error
	NativeCensusUncertain                                bool
	NativeOutcome                                        string
}
type workerFenceAbnormal struct{ cause any }

func (e *workerFenceAbnormal) Error() string {
	return "original composed fence operation abnormal; custody unresolved"
}
func (e *workerFenceAbnormal) Unwrap() error { e2, _ := e.cause.(error); return e2 }

var ErrWorkerFencePending = errors.New("original composed fence operation pending; retain and inspect")
var errOriginalWorkerFence = errors.New("original composed fence holder required")

// Pure allocation after the REAL service group/debit and actual image pending
// have been retained by root. Selectors alone cannot construct this holder.
// This first cut binds CONFIG ABSENCE: a config.toml entry refuses. A future
// parsed-config owner must be reviewed before optional config support is added.
func NewWorkerFreshFencePending(group *residentservice.WorkerServiceGroup, pin *mesh.WorkerResidentImagePinPending, command *exec.Cmd) (*WorkerFreshFencePending, error) {
	if pin == nil || group == nil || group.CheckOriginalCommand(command) != nil || group.CheckOriginalImagePin(pin) != nil {
		return nil, errOriginalWorkerFence
	}
	selectors, err := group.SelectorsOriginal()
	if err != nil {
		return nil, err
	}
	home := selectors.Home
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || len(home) > 4096 || strings.ContainsRune(home, 0) || len(strings.Split(strings.TrimPrefix(home, "/"), "/")) > 64 {
		return nil, errOriginalWorkerFence
	}
	h := &WorkerFreshFencePending{}
	o := &workerFenceOwner{self: h, path: home, uid: uint32(os.Geteuid()), group: group, pin: pin, command: command, extra: command.ExtraFiles, service: selectors}
	h.owner = o
	o.state = &workerFenceNative{owner: o}
	return h, nil
}
func (h *WorkerFreshFencePending) begin(ctx context.Context, kind string, prepared *WorkerPreparedFence) (*workerFenceOperation, error) {
	if h == nil || h.owner == nil {
		return nil, errOriginalWorkerFence
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self != h {
		return nil, errOriginalWorkerFence
	}
	if o.active != nil {
		return nil, ErrWorkerFencePending
	}
	if o.parentRetirement != nil {
		return nil, errOriginalWorkerFence
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	if kind == "prepare" {
		if o.prepareTried {
			return nil, errOriginalWorkerFence
		}
		o.prepareTried = true
	} else {
		if prepared == nil || prepared != o.prepared || prepared.owner != o {
			return nil, errOriginalWorkerFence
		}
		switch kind {
		case "bind":
			if o.bindTried {
				return nil, errOriginalWorkerFence
			}
			o.bindTried = true
		case "journal":
			if o.journalIssued || o.writerWithdrawn {
				return nil, errOriginalWorkerFence
			}
			o.journalIssued = true
		case "write":
			if !o.journalIssued || o.writeTried || o.writerWithdrawn {
				return nil, errOriginalWorkerFence
			}
			o.writeTried = true
		case "withdraw":
			if !o.intentCommitted || o.writerWithdrawn {
				return nil, errOriginalWorkerFence
			}
			o.writerWithdrawn = true // permanent BEFORE subsequent context/native checks
		case "validate_before_start":
			if !o.bound || !o.intentCommitted || !o.writerWithdrawn || o.beforeStartTried {
				return nil, errOriginalWorkerFence
			}
			o.beforeStartTried = true
		case "short-seal":
			if !o.bound || !o.intentCommitted || !o.writerWithdrawn || !o.beforeStartTried || o.shortSealTried {
				return nil, errOriginalWorkerFence
			}
			o.shortSealTried = true
		case "worker-post-start":
			if o.postStartTried || o.postStart != nil || !o.postStartPreparedLocked(prepared) {
				return nil, errOriginalWorkerFence
			}
			o.postStartTried = true
		default:
			return nil, errOriginalWorkerFence
		}
	}
	op := &workerFenceOperation{owner: o, ctx: ctx, kind: kind, phase: "registered_before_effect"}
	if kind == "withdraw" {
		o.withdrawOriginal = op
	}
	if kind == "short-seal" {
		o.shortSealOriginal = op
	}
	o.active = op
	return op, nil
}
func (n *workerFenceNative) before(phase string) error {
	o := n.owner
	o.mu.Lock()
	op := o.active
	if op == nil {
		o.mu.Unlock()
		panic("fence effect without original operation")
	}
	op.phase = phase
	ctx := op.ctx
	o.mu.Unlock()
	if ctx == nil {
		return errOriginalWorkerFence
	}
	return ctx.Err()
}
func (n *workerFenceNative) publish(phase string) {
	o := n.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("fence effect without original operation")
	}
	o.active.phase = phase
	o.opened, o.closed = n.opened, n.closed
	o.live = 0
	for _, f := range n.slots {
		if f != nil {
			o.live++
		}
	}
	o.createdDirectory, o.createdLedger = n.createdDirectory, n.createdLedger
}
func (o *workerFenceOwner) finish(op *workerFenceOperation, normal *bool, resultErr *error) {
	cause := recover()
	if *normal && *resultErr == nil {
		if op.ctx == nil {
			*resultErr = errOriginalWorkerFence
		} else {
			*resultErr = op.ctx.Err()
		}
	}
	o.mu.Lock()
	if o.active != op || op.owner != o {
		o.mu.Unlock()
		panic("original fence operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &workerFenceAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && o.firstError == nil {
			o.firstError = *resultErr
		}
		o.active = nil
	}
	o.opened, o.closed = o.state.opened, o.state.closed
	o.live = 0
	for _, f := range o.state.slots {
		if f != nil {
			o.live++
		}
	}
	o.createdDirectory, o.createdLedger = o.state.createdDirectory, o.state.createdLedger
	if *normal && !op.abnormal {
		op.normalFinished, op.finalError = true, *resultErr
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (n *workerFenceNative) capacity() error {
	if len(n.history) >= maxWorkerFenceHistory {
		return errOriginalWorkerFence
	}
	for _, f := range n.slots {
		if f == nil {
			return nil
		}
	}
	return errOriginalWorkerFence
}
func (n *workerFenceNative) retain(fd int, name string) (*workerFenceFD, error) {
	for index, f := range n.slots {
		if f == nil {
			actual := &workerFenceFD{number: fd, slot: index}
			n.slots[index] = actual
			n.history = append(n.history, actual)
			n.opened++
			n.publish("original_raw_FD_registered_before_NewFile")
			actual.file = os.NewFile(uintptr(fd), name)
			if actual.file == nil {
				return actual, errOriginalWorkerFence
			}
			// Raw identity and file object are retained even if cancellation/stat fails.
			if err := n.before("original_retained_FD_stat"); err != nil {
				return actual, err
			}
			var st unix.Stat_t
			if err := unix.Fstat(fd, &st); err != nil {
				return actual, err
			}
			actual.id = fenceID(&st)
			return actual, nil
		}
	}
	panic("original fence raw FD capacity violated")
}
func (n *workerFenceNative) openDirectory(parent *workerFenceFD, name string) (*workerFenceFD, error) {
	if err := n.capacity(); err != nil {
		return nil, err
	}
	if err := n.before("original_directory_open"); err != nil {
		return nil, err
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	fd, err := -1, error(nil)
	if parent == nil {
		fd, err = unix.Open(name, flags, 0)
	} else {
		fd, err = unix.Openat(parent.number, name, flags, 0)
	}
	if fd >= 0 {
		f, e := n.retain(fd, name)
		return f, errors.Join(err, e)
	}
	return nil, err
}
func (n *workerFenceNative) closeAux(f *workerFenceFD) error {
	if f == nil || f.closeTried {
		return errOriginalWorkerFence
	}
	f.closeTried = true
	n.publish("original_auxiliary_close_pending")
	if f.file != nil {
		f.closeError = f.file.Close()
	} else {
		f.closeError = unix.Close(f.number)
	}
	if f.closeError != nil {
		return f.closeError
	}
	f.closed = true
	n.closed++
	n.slots[f.slot] = nil
	n.publish("original_auxiliary_close_returned")
	return nil
}
func (n *workerFenceNative) openHome() (*workerFenceFD, error) {
	current, err := n.openDirectory(nil, "/")
	if err != nil {
		return current, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(n.owner.path, "/"), "/") {
		if err = n.before("original_home_component_stat"); err != nil {
			return current, err
		}
		var st unix.Stat_t
		if err = unix.Fstatat(current.number, part, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return current, err
		}
		if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
			return current, errOriginalWorkerFence
		}
		next, e := n.openDirectory(current, part)
		if e != nil {
			return next, e
		}
		if next.id != fenceID(&st) {
			return next, errOriginalWorkerFence
		}
		if err = n.closeAux(current); err != nil {
			return next, err
		}
		current = next
	}
	return current, n.check(current, true)
}
func (n *workerFenceNative) check(f *workerFenceFD, directory bool) error {
	if f == nil || f.file == nil || f.closeTried {
		return errOriginalWorkerFence
	}
	if err := n.before("original_private_reference_stat"); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstat(f.number, &st); err != nil {
		return err
	}
	mode := uint32(unix.S_IFREG) | 0600
	if directory {
		mode = uint32(unix.S_IFDIR) | 0700
	}
	if fenceID(&st) != f.id || st.Uid != n.owner.uid || uint32(st.Mode) != mode || st.Nlink == 0 || !directory && st.Nlink != 1 {
		return errOriginalWorkerFence
	}
	if err := n.before("original_private_reference_flags"); err != nil {
		return err
	}
	flags, err := unix.FcntlInt(uintptr(f.number), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return errOriginalWorkerFence
	}
	return nil
}
func (n *workerFenceNative) privateFile(parent *workerFenceFD, name string) (*workerFenceFD, error) {
	if err := n.capacity(); err != nil {
		return nil, err
	}
	if err := n.before("original_exclusive_file_create"); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(parent.number, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if fd < 0 {
		return nil, err
	}
	f, e := n.retain(fd, name)
	if err = errors.Join(err, e); err != nil {
		return f, err
	}
	if err = n.before("original_created_file_mode"); err != nil {
		return f, err
	}
	if err = unix.Fchmod(fd, 0600); err != nil {
		return f, err
	}
	if err = n.before("original_created_file_mode_stat"); err != nil {
		return f, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return f, err
	}
	f.id = fenceID(&st)
	return f, n.check(f, false)
}
func (n *workerFenceNative) named(parent *workerFenceFD, name string, expected fenceIdentity) error {
	if err := n.before("original_exact_name_stat"); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(parent.number, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if fenceID(&st) != expected || st.Nlink == 0 || uint32(st.Mode)&unix.S_IFMT == unix.S_IFREG && st.Nlink != 1 {
		return errOriginalWorkerFence
	}
	return nil
}

// List only a fresh original cursor, at most allowed-count plus one entry. Names
// are DATA checked against exact current original identities, never adopted.
func (n *workerFenceNative) listCursor(f *workerFenceFD, allowed map[string]fenceIdentity) error {
	if err := n.before("original_bounded_directory_listing"); err != nil {
		return err
	}
	names, err := f.file.Readdirnames(len(allowed) + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) != len(allowed) {
		return errOriginalWorkerFence
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		id, ok := allowed[name]
		if !ok || seen[name] {
			return errOriginalWorkerFence
		}
		seen[name] = true
		if err = n.named(f, name, id); err != nil {
			return err
		}
	}
	if err = n.before("original_directory_EOF_check"); err != nil {
		return err
	}
	extra, err := f.file.Readdirnames(1)
	if len(extra) != 0 || !errors.Is(err, io.EOF) {
		return errOriginalWorkerFence
	}
	return nil
}
func (n *workerFenceNative) listOriginal(parent *workerFenceFD, allowed map[string]fenceIdentity) (resultErr error) {
	cursor, err := n.openDirectory(parent, ".")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, n.closeAux(cursor)) }()
	if cursor.id != parent.id {
		return errOriginalWorkerFence
	}
	return n.listCursor(cursor, allowed)
}
func (n *workerFenceNative) inventory(created bool) (resultErr error) {
	o := n.owner
	allowed := map[string]fenceIdentity{
		"resident-service.lock":    {device: o.service.LockID.Device, inode: o.service.LockID.Inode, uid: o.service.LockID.UID, gid: n.home.id.gid, mode: uint32(unix.S_IFREG) | 0600},
		"resident-service.journal": {device: o.service.JournalID.Device, inode: o.service.JournalID.Inode, uid: o.service.JournalID.UID, gid: n.home.id.gid, mode: uint32(unix.S_IFREG) | 0600},
		"resident-images":          imageID(o.image.ImagesID),
	}
	// Service NativeID has no group field. Compare its real uid/dev/inode/mode
	// below while binding the actual current group value rather than guessing it.
	for _, name := range []string{"resident-service.lock", "resident-service.journal"} {
		if err := n.before("original_service_companion_group_stat"); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstatat(n.home.number, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		expected := allowed[name]
		expected.gid = st.Gid
		allowed[name] = expected
	}
	if created {
		allowed["resident-launch.lock"] = n.launch.id
		allowed["resident-birth.json"] = n.journal.id
		allowed["daemon.lock"] = n.daemon.id
		allowed["state.db"] = n.directory.id
	}
	if err := n.listOriginal(n.home, allowed); err != nil {
		return err
	}
	images, err := n.openDirectory(n.home, "resident-images")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, n.closeAux(images)) }()
	if images.id != imageID(o.image.ImagesID) {
		return errOriginalWorkerFence
	}
	pinName := "pin-" + o.service.DebitID
	if err = n.listCursor(images, map[string]fenceIdentity{pinName: imageID(o.image.DirectoryID)}); err != nil {
		return err
	}
	pin, err := n.openDirectory(images, pinName)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, n.closeAux(pin)) }()
	if pin.id != imageID(o.image.DirectoryID) {
		return errOriginalWorkerFence
	}
	return n.listCursor(pin, map[string]fenceIdentity{"arm": imageID(o.image.ImageID)})
}
func (h *WorkerFreshFencePending) PrepareOriginal(ctx context.Context) (prepared *WorkerPreparedFence, resultErr error) {
	op, err := h.begin(ctx, "prepare", nil)
	if err != nil {
		return nil, err
	}
	o := h.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.state.prepareBody()
	if resultErr == nil {
		prepared = &WorkerPreparedFence{owner: o}
		o.mu.Lock()
		o.prepared = prepared
		o.mu.Unlock()
	}
	normal = true
	return prepared, resultErr
}
func (n *workerFenceNative) prepareBody() (err error) {
	o := n.owner
	if err = n.before("original_funding_selector_check"); err != nil {
		return err
	}
	if uint32(os.Geteuid()) != o.uid || o.group.CheckOriginalCommand(o.command) != nil {
		return errOriginalWorkerFence
	}
	selectors, err := o.group.SelectorsOriginal()
	if err != nil {
		return err
	}
	if selectors != o.service {
		return errOriginalWorkerFence
	}
	o.image, err = o.pin.InventoryOriginal(o.active.ctx)
	if err != nil {
		return err
	}
	if o.image.Home != o.path || o.image.DebitID != o.service.DebitID || o.image.Path != filepath.Join(o.path, "resident-images", "pin-"+o.service.DebitID, "arm") {
		return errOriginalWorkerFence
	}
	n.home, err = n.openHome()
	if err != nil {
		return err
	}
	if n.home.id != imageID(o.image.HomeID) || serviceID(n.home.id) != o.service.HomeID {
		return errOriginalWorkerFence
	}
	if err = n.inventory(false); err != nil {
		return err
	}
	n.launch, err = n.privateFile(n.home, "resident-launch.lock")
	if err != nil {
		return err
	}
	if err = n.before("original_launch_flock"); err != nil {
		return err
	}
	if err = unix.Flock(n.launch.number, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	n.daemon, err = n.privateFile(n.home, "daemon.lock")
	if err != nil {
		return err
	}
	n.journal, err = n.privateFile(n.home, "resident-birth.json")
	if err != nil {
		return err
	}
	if err = n.before("original_exclusive_state_directory_create"); err != nil {
		return err
	}
	if err = unix.Mkdirat(n.home.number, "state.db", 0700); err != nil {
		return err
	}
	n.createdDirectory = true
	n.publish("original_state_directory_created")
	n.directory, err = n.openDirectory(n.home, "state.db")
	if err != nil {
		return err
	}
	if err = n.check(n.directory, true); err != nil {
		return err
	}
	n.ledger, err = n.privateFile(n.directory, "ledger.sqlite")
	if err != nil {
		return err
	}
	n.createdLedger = true
	n.publish("original_empty_ledger_created")
	for _, f := range []*workerFenceFD{n.ledger, n.journal, n.launch, n.daemon, n.directory, n.home} {
		if err = n.before("original_namespace_sync"); err != nil {
			return err
		}
		if err = f.file.Sync(); err != nil {
			return err
		}
	}
	return n.validate()
}
func (n *workerFenceNative) validate() (resultErr error) {
	current, err := n.openHome()
	if err != nil {
		return err
	}
	if current.id != n.home.id {
		return errOriginalWorkerFence
	}
	if err = n.closeAux(current); err != nil {
		return err
	}
	if uint32(os.Geteuid()) != n.owner.uid {
		return errOriginalWorkerFence
	}
	for _, entry := range []struct {
		parent    *workerFenceFD
		name      string
		f         *workerFenceFD
		directory bool
	}{
		{nil, "", n.home, true}, {n.home, "resident-launch.lock", n.launch, false}, {n.home, "daemon.lock", n.daemon, false},
		{n.home, "resident-birth.json", n.journal, false}, {n.home, "state.db", n.directory, true}, {n.directory, "ledger.sqlite", n.ledger, false},
	} {
		if err = n.check(entry.f, entry.directory); err != nil {
			return err
		}
		if entry.parent != nil {
			if err = n.named(entry.parent, entry.name, entry.f.id); err != nil {
				return err
			}
		}
	}
	if err = n.before("original_empty_ledger_stat"); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(n.ledger.number, &st); err != nil {
		return err
	}
	if st.Size != 0 {
		return errOriginalWorkerFence
	}
	if err = n.listOriginal(n.directory, map[string]fenceIdentity{"ledger.sqlite": n.ledger.id}); err != nil {
		return err
	}
	return n.inventory(true)
}
func (h *WorkerFreshFencePending) InspectOriginal() WorkerFenceInspection {
	if h == nil || h.owner == nil {
		return WorkerFenceInspection{FirstError: errOriginalWorkerFence, NativeOutcome: "unknown", NativeCensusUncertain: true}
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self != h {
		return WorkerFenceInspection{FirstError: errOriginalWorkerFence, NativeOutcome: "unknown", NativeCensusUncertain: true}
	}
	v := WorkerFenceInspection{PrepareAttempted: o.prepareTried, Prepared: o.prepared != nil, Bound: o.bound, WriterWithdrawn: o.writerWithdrawn, IntentCommitted: o.intentCommitted, Opened: o.opened, Closed: o.closed, LastPublishedLiveFDs: o.live, WrittenBytes: o.written, DirectoryCreated: o.createdDirectory, LedgerCreated: o.createdLedger, FirstError: o.firstError, NativeOutcome: "unknown"}
	if o.active != nil {
		v.OperationPending = true
		v.OperationAbnormal = o.active.abnormal
		v.OperationKind = o.active.kind
		v.OperationPhase = o.active.phase
		v.NativeCensusUncertain = true
	}
	return v
}
