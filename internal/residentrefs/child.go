// Package residentrefs retains the SAME fixed inherited child entries through
// one-way component delegation. No DTO or caller FD can construct an original.
package residentrefs

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const inheritedCount = 13

var ErrOriginal = errors.New("original fixed resident child custody required")
var ErrBusy = errors.New("original child reference waiter capacity reached")
var ErrPending = errors.New("original resident child reference effect pending; retain and inspect")

type ChildPending struct{ state *childState }
type ServiceOriginal struct {
	state *recipientState
	self  *ServiceOriginal
}
type FenceOriginal struct {
	state *recipientState
	self  *FenceOriginal
}
type ImageOriginal struct {
	state *recipientState
	self  *ImageOriginal
}
type ControlOriginal struct {
	state *recipientState
	self  *ControlOriginal
}

type originalWaiter struct{ identity byte }

const maxOriginalWaiters = 64

type childState struct {
	waiters                                     map[*originalWaiter]struct{}
	mu                                          sync.Mutex // pure registry publication, never held through native effects
	self                                        *ChildPending
	entries                                     [inheritedCount]*originalEntry
	recipients                                  [4]*recipientState
	service                                     *ServiceOriginal
	fence                                       *FenceOriginal
	image                                       *ImageOriginal
	control                                     *ControlOriginal
	active                                      *originalOperation
	recipientsTried, delegationTried, delegated bool
	rootCloseRetired, sealTried, sealed         bool
	firstError                                  error
}
type recipientState struct {
	census                         *childState
	original                       any // exact concrete original owner pointer, never caller-selected data
	role                           string
	entries                        [6]*originalEntry
	count                          int
	materializeTried, materialized bool
	firstError                     error
	journal                        *JournalView
	imageView                      *ImageView
	last                           *originalOperation
	readTried, writeTried          bool
}
type originalEntry struct {
	self                            *originalEntry
	census                          *childState
	number                          int // private fixed3..15 ONLY; no descriptor getter
	recipient, reservedRecipient    *recipientState
	file                            *os.File // exactly one wrapper, under the retained sole recipient
	materializeTried                bool
	identity                        Identity
	info                            os.FileInfo
	cloexecTried, cloexecReturned   bool
	nonblockTried, nonblockReturned bool
	closeTried, closeReturned       bool
	firstError                      error
}
type originalOperation struct {
	done                chan struct{}
	census              *childState
	recipient           *recipientState
	ctx                 context.Context
	kind, phase         string
	abnormal            bool
	cause               any
	entry               *originalEntry
	bytes               []byte
	offset              int64
	requested, returned int
	ioError             error
}
type Identity struct {
	Device, Inode  uint64
	UID, GID, Mode uint32
}
type Inspection struct {
	Registered, DelegatedEntries, MaterializedEntries                      int
	RecipientsRegistered, DelegationAttempted, RootCloseAuthorityWithdrawn bool
	Delegated, SealAttempted, Sealed, OperationPending, OperationAbnormal  bool
	OperationKind, OperationPhase                                          string
	FirstError                                                             error
	NativeOutcome                                                          string // always unknown, never native Closed/resource credit
}
type abnormalOriginal struct{ cause any }

func (e *abnormalOriginal) Error() string {
	return "original child reference operation abnormal; custody unresolved"
}
func (e *abnormalOriginal) Unwrap() error { err, _ := e.cause.(error); return err }

// Pure fixed-role entry allocation. ALL thirteen raw entries exist before the
// caller may invoke any native operation, wrapper, stat, service/image/native
// constructor or legacy receiver. Parent-process reference owners are distinct.
var processChildClaim struct {
	mu        sync.Mutex
	attempted bool
	original  *ChildPending
}

// The claim is process-wide and consumed before allocation. Failed/abnormal
// construction cannot make inherited originals available to a second registry.
// The caller must retain its destination before calling. Publish the actual
// original there before later allocation, so abnormal return cannot lose it.
// A nil/occupied destination refuses without consuming or replacing custody.
func NewFixedChildPending(destination **ChildPending) error {
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	if destination == nil || *destination != nil || processChildClaim.attempted {
		return ErrOriginal
	}
	processChildClaim.attempted = true
	c := &ChildPending{}
	processChildClaim.original = c
	*destination = c // actual original published BEFORE later state/entry allocation
	s := &childState{self: c}
	c.state = s
	for i := range s.entries {
		e := &originalEntry{census: s, number: i + 3}
		e.self = e
		s.entries[i] = e
	}
	return nil
}

// ClaimLegacyReceiverExclusion consumes the same process-wide exclusion for
// the legacy fixed4 receiver. It creates/returns no descriptor census, custody,
// wrapper or authority. A later fixed13 factory must refuse, and vice versa.
// The legacy owner calls this once after context validation and before raw
// registration; its original operation retains its existing fixed4 lineage.
func ClaimLegacyReceiverExclusion() error {
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	if processChildClaim.attempted {
		return ErrOriginal
	}
	processChildClaim.attempted = true
	return nil
}

func (c *ChildPending) ownsProcessClaim() bool {
	processChildClaim.mu.Lock()
	defer processChildClaim.mu.Unlock()
	return processChildClaim.attempted && processChildClaim.original == c
}

// Pure creation of the actual recipient pending owners, before delegation or
// native effects. Root retains these in the real component pending holders.
// This is a constructor, not a getter for an already-adopted borrower's closer.
func (c *ChildPending) NewOriginalRecipients() (service *ServiceOriginal, fence *FenceOriginal, image *ImageOriginal, control *ControlOriginal, resultErr error) {
	if c == nil || c.state == nil {
		return nil, nil, nil, nil, ErrOriginal
	}
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self != c || s.recipientsTried || s.active != nil || s.firstError != nil {
		return nil, nil, nil, nil, ErrOriginal
	}
	s.recipientsTried = true
	service = &ServiceOriginal{state: &recipientState{census: s, role: "service"}}
	service.self = service
	service.state.original = service
	fence = &FenceOriginal{state: &recipientState{census: s, role: "fence"}}
	fence.self = fence
	fence.state.original = fence
	image = &ImageOriginal{state: &recipientState{census: s, role: "image"}}
	image.self = image
	image.state.original = image
	control = &ControlOriginal{state: &recipientState{census: s, role: "control"}}
	control.self = control
	control.state.original = control
	s.service, s.fence, s.image, s.control = service, fence, image, control
	s.recipients = [4]*recipientState{service.state, fence.state, image.state, control.state}
	// Fixed mapping only. No arbitrary caller FD/index/role registry is exposed.
	for role, indices := range [4][]int{{1, 2, 3}, {4, 5, 9, 10, 11, 12}, {6, 7, 8}, {0}} {
		recipient := s.recipients[role]
		recipient.count = len(indices)
		for at, index := range indices {
			entry := s.entries[index]
			recipient.entries[at] = entry
			entry.reservedRecipient = recipient
		}
	}
	return service, fence, image, control, nil
}

// Atomic pure ownership publication for all four exact original recipients.
// Donor close authority is permanently withdrawn BEFORE moving any entry. On
// panic/Goexit, reserved recipients retain their same entries and the whole
// operation stays unresolved; there is no automatic handback, retry or credit.
func (c *ChildPending) DelegateOriginalRecipients(service *ServiceOriginal, fence *FenceOriginal, image *ImageOriginal, control *ControlOriginal) (resultErr error) {
	if c == nil || c.state == nil {
		return ErrOriginal
	}
	s := c.state
	s.mu.Lock()
	if s.self != c || s.active != nil || s.firstError != nil || s.delegationTried || service == nil || fence == nil || image == nil || control == nil || service != s.service || service.self != service || fence != s.fence || fence.self != fence || image != s.image || image.self != image || control != s.control || control.self != control {
		s.mu.Unlock()
		return ErrOriginal
	}
	for _, entry := range s.entries {
		if entry == nil || entry.self != entry || entry.census != s || entry.recipient != nil || entry.reservedRecipient == nil || entry.file != nil {
			s.mu.Unlock()
			return ErrOriginal
		}
	}
	s.delegationTried = true
	op := &originalOperation{census: s, kind: "delegation", done: make(chan struct{}), phase: "original_recipient_pending_before_pure_move"}
	s.active = op
	// Holding this lock makes the whole transition invisible to inspectors/native
	// consumers until complete. Everything used below was already allocated.
	normal := false
	defer func() {
		cause := recover()
		if !normal {
			op.abnormal, op.cause = true, cause
			if s.firstError == nil {
				s.firstError = &abnormalOriginal{cause: cause}
			}
		} else {
			s.active = nil
			close(op.done)
		}
		s.mu.Unlock()
		if cause != nil {
			panic(cause)
		}
	}()
	s.rootCloseRetired = true
	for _, entry := range s.entries {
		entry.recipient = entry.reservedRecipient
	}
	s.delegated = true
	normal = true
	return nil
}
func (c *ChildPending) begin(ctx context.Context, kind string, recipient *recipientState, original any) (*originalOperation, error) {
	if c == nil || c.state == nil || ctx == nil {
		return nil, ErrOriginal
	}
	s := c.state
	waiter := &originalWaiter{}
	queued := false
	defer func() {
		if queued {
			s.mu.Lock()
			delete(s.waiters, waiter)
			s.mu.Unlock()
		}
	}()
	for {
		s.mu.Lock()
		if s.self != c {
			s.mu.Unlock()
			return nil, ErrOriginal
		}
		if err := ctx.Err(); err != nil && (queued || s.active != nil || (kind != "seal" && kind != "materialize")) {
			s.mu.Unlock()
			return nil, err
		}
		if active := s.active; active != nil {
			if !queued {
				if len(s.waiters) >= maxOriginalWaiters {
					s.mu.Unlock()
					return nil, ErrBusy
				}
				if s.waiters == nil {
					s.waiters = make(map[*originalWaiter]struct{})
				}
				s.waiters[waiter] = struct{}{}
				queued = true
			}
			done := active.done
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		if queued {
			delete(s.waiters, waiter)
			queued = false
		}
		if s.firstError != nil {
			s.mu.Unlock()
			return nil, s.firstError
		}
		if !c.ownsProcessClaim() || !s.delegated || !s.rootCloseRetired {
			s.mu.Unlock()
			return nil, ErrOriginal
		}
		if kind == "composed" {
			if recipient != nil || !s.sealed {
				s.mu.Unlock()
				return nil, ErrOriginal
			}
			for _, r := range s.recipients {
				if r == nil || !r.materialized || r.firstError != nil {
					s.mu.Unlock()
					return nil, ErrOriginal
				}
			}
		} else if kind == "seal" {
			if s.sealTried || recipient != nil {
				s.mu.Unlock()
				return nil, ErrOriginal
			}
			s.sealTried = true
		} else {
			if recipient == nil || recipient.census != s || recipient.original != original || !s.sealed || recipient.firstError != nil {
				s.mu.Unlock()
				return nil, ErrOriginal
			}
			if kind == "materialize" {
				if recipient.materializeTried {
					s.mu.Unlock()
					return nil, ErrOriginal
				}
				recipient.materializeTried = true
			} else if !recipient.materialized {
				s.mu.Unlock()
				return nil, ErrOriginal
			}
		}
		op := &originalOperation{census: s, recipient: recipient, ctx: ctx, kind: kind, phase: "registered_before_native_effect", done: make(chan struct{})}
		s.active = op
		s.mu.Unlock()
		return op, nil
	}
}

func (s *childState) before(op *originalOperation, phase string) error {
	s.mu.Lock()
	if s.active != op || op.census != s {
		s.mu.Unlock()
		panic("original child entry operation lost")
	}
	op.phase = phase
	ctx := op.ctx
	s.mu.Unlock()
	if ctx == nil {
		return ErrOriginal
	}
	return ctx.Err()
}
func (s *childState) finish(op *originalOperation, normal *bool, resultErr *error) {
	cause := recover()
	if *normal && *resultErr == nil {
		if op.ctx == nil {
			*resultErr = ErrOriginal
		} else {
			*resultErr = op.ctx.Err()
		}
	}
	s.mu.Lock()
	if s.active != op || op.census != s {
		s.mu.Unlock()
		panic("original child operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if s.firstError == nil {
			s.firstError = &abnormalOriginal{cause: cause}
		}
	} else {
		if *resultErr != nil && !(op.kind == "read_at" && errors.Is(*resultErr, io.EOF)) && s.firstError == nil {
			s.firstError = *resultErr
		}
		if *resultErr == nil {
			if op.kind == "seal" {
				s.sealed = true
			}
			if op.kind == "materialize" {
				op.recipient.materialized = true
			}
		}
		s.active = nil
		close(op.done)
	}
	if op.recipient != nil && s.firstError != nil && op.recipient.firstError == nil {
		op.recipient.firstError = s.firstError
	}
	if op.recipient != nil {
		op.recipient.last = op
	}
	s.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

// Every inherited original is made CLOEXEC before ANY NewFile/stat/component
// native effect. The control OFD alone becomes nonblocking for bounded Go poll
// I/O. Its actual counterpart is a distinct socketpair OFD in the parent.
func (c *ChildPending) SealOriginalDescriptors(ctx context.Context) (resultErr error) {
	op, err := c.begin(ctx, "seal", nil, nil)
	if err != nil {
		return err
	}
	s := c.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = func() error {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		for _, entry := range s.entries {
			s.mu.Lock()
			op.entry = entry
			s.mu.Unlock()
			if err := s.before(op, "original_inherited_CLOEXEC_pending"); err != nil {
				return err
			}
			s.mu.Lock()
			entry.cloexecTried = true
			s.mu.Unlock()
			_, err := unix.FcntlInt(uintptr(entry.number), unix.F_SETFD, unix.FD_CLOEXEC)
			s.mu.Lock()
			entry.cloexecReturned = err == nil
			entry.firstError = err
			s.mu.Unlock()
			if err != nil {
				return err
			}
		}
		entry := s.entries[0]
		s.mu.Lock()
		op.entry = entry
		s.mu.Unlock()
		if err := s.before(op, "original_control_socket_kind_pending"); err != nil {
			return err
		}
		kind, err := unix.GetsockoptInt(entry.number, unix.SOL_SOCKET, unix.SO_TYPE)
		if err != nil {
			return err
		}
		if kind != unix.SOCK_STREAM {
			return ErrOriginal
		}
		if err = s.before(op, "original_control_socket_family_pending"); err != nil {
			return err
		}
		address, err := unix.Getsockname(entry.number)
		if err != nil {
			return err
		}
		if _, ok := address.(*unix.SockaddrUnix); !ok {
			return ErrOriginal
		}
		if err := s.before(op, "original_control_nonblocking_pending"); err != nil {
			return err
		}
		s.mu.Lock()
		entry.nonblockTried = true
		s.mu.Unlock()
		err = unix.SetNonblock(entry.number, true)
		s.mu.Lock()
		entry.nonblockReturned = err == nil
		entry.firstError = err
		s.mu.Unlock()
		return err
	}()
	normal = true
	return resultErr
}
func (r *recipientState) materialize(ctx context.Context, original any) (resultErr error) {
	if r == nil || r.census == nil {
		return ErrOriginal
	}
	s := r.census
	op, err := s.self.begin(ctx, "materialize", r, original)
	if err != nil {
		return err
	}
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = r.materializeBody(op)
	normal = true
	return resultErr
}
func (r *recipientState) materializeBody(op *originalOperation) error {
	s := r.census
	for _, entry := range r.entries[:r.count] {
		s.mu.Lock()
		op.entry = entry
		s.mu.Unlock()
		if entry == nil || entry.self != entry || entry.census != s || entry.recipient != r || entry.reservedRecipient != r || entry.file != nil || entry.materializeTried {
			return ErrOriginal
		}
		if err := s.before(op, "original_recipient_NewFile_pending"); err != nil {
			return err
		}
		s.mu.Lock()
		entry.materializeTried = true
		s.mu.Unlock()
		file := os.NewFile(uintptr(entry.number), "original resident child reference")
		s.mu.Lock()
		entry.file = file
		s.mu.Unlock()
		if file == nil {
			return ErrOriginal
		}
		if err := s.before(op, "original_recipient_native_stat_pending"); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstat(entry.number, &st); err != nil {
			return err
		}
		identity := Identity{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}
		s.mu.Lock()
		entry.identity = identity
		s.mu.Unlock()
		if err := validateFixedKind(entry.number, &st); err != nil {
			return err
		}
		if err := s.before(op, "original_recipient_file_stat_pending"); err != nil {
			return err
		}
		info, err := file.Stat()
		s.mu.Lock()
		entry.info = info
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if err = s.before(op, "original_recipient_CLOEXEC_validation"); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(entry.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrOriginal
		}

		if err = s.before(op, "original_recipient_access_mode_validation"); err != nil {
			return err
		}
		flags, err = unix.FcntlInt(uintptr(entry.number), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		access := unix.O_RDWR
		switch entry.number {
		case 4, 9, 10, 11, 12, 14:
			access = unix.O_RDONLY
		}
		if flags&unix.O_ACCMODE != access || entry.number == 3 && flags&unix.O_NONBLOCK == 0 {
			return ErrOriginal
		}
	}
	return nil
}
func validateFixedKind(number int, st *unix.Stat_t) error {
	if st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 {
		return ErrOriginal
	}
	mode := uint32(unix.S_IFREG) | 0600
	switch number {
	case 3:
		if uint32(st.Mode)&unix.S_IFMT != unix.S_IFSOCK {
			return ErrOriginal
		}
		return nil
	case 4, 10, 11, 12, 14:
		mode = uint32(unix.S_IFDIR) | 0700
	case 9:
		mode = uint32(unix.S_IFREG) | 0500
	}
	if uint32(st.Mode) != mode || mode&unix.S_IFMT == unix.S_IFREG && st.Nlink != 1 {
		return ErrOriginal
	}
	return nil
}
func (r *ServiceOriginal) MaterializeOriginal(ctx context.Context) error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return r.state.materialize(ctx, r)
}
func (r *FenceOriginal) MaterializeOriginal(ctx context.Context) error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return r.state.materialize(ctx, r)
}
func (r *ImageOriginal) MaterializeOriginal(ctx context.Context) error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return r.state.materialize(ctx, r)
}
func (r *ControlOriginal) MaterializeOriginal(ctx context.Context) error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return r.state.materialize(ctx, r)
}
func (c *ChildPending) InspectOriginal() Inspection {
	if c == nil || c.state == nil {
		return Inspection{FirstError: ErrOriginal, NativeOutcome: "unknown"}
	}
	s := c.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self != c {
		return Inspection{FirstError: ErrOriginal, NativeOutcome: "unknown"}
	}
	value := Inspection{Registered: inheritedCount, RecipientsRegistered: s.recipientsTried, DelegationAttempted: s.delegationTried, RootCloseAuthorityWithdrawn: s.rootCloseRetired, Delegated: s.delegated, SealAttempted: s.sealTried, Sealed: s.sealed, FirstError: s.firstError, NativeOutcome: "unknown"}
	for _, entry := range s.entries {
		if entry.recipient != nil {
			value.DelegatedEntries++
		}
		if entry.file != nil {
			value.MaterializedEntries++
		}
	}
	if s.active != nil {
		value.OperationPending = true
		value.OperationAbnormal = s.active.abnormal
		value.OperationKind = s.active.kind
		value.OperationPhase = s.active.phase
	}
	return value
}
