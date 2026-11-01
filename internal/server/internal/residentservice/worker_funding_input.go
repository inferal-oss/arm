package residentservice

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// This reader belongs to one actual funding operation. It never opens from a
// caller path or returns a descriptor/closer to a worker. Every successful raw
// open, including a partially wrapped/validated one, remains on the original
// owner. After full comparison, successful private read references close once
// with actual outcomes retained. No Close recycles a slot or credits capacity.
//
// Source scope: two walks, each root plus at most63 parent components, plus
// one evidence file =129 lifetime public open attempts. At most32 ReadAt
// public attempts, one input backing <=MaxEvidenceBytes; decoder/canonical backing is
// separately included in the complete funding source ledger.
const workerFundingOpenAttempts = 129

type workerFundingOpenCall struct {
	parent, fd                    int
	name                          string
	flags                         int
	attempted, returned, wrapped  bool
	err                           error
	file                          *os.File
	closeAttempted, closeReturned bool
	closeError                    error
	info                          os.FileInfo
	native                        unix.Stat_t
}

type workerFundingInputRead struct {
	owner                                   *WorkerStartupFundingPending
	reservation                             *workerStartupVerifierReservation
	op                                      *operation
	input                                   OperatorFileInput
	opens                                   [workerFundingOpenAttempts]workerFundingOpenCall
	openCount                               int
	initialDirectory                        *workerFundingOpenCall
	file                                    *workerFundingOpenCall
	currentDirectory                        *workerFundingOpenCall
	before, after                           os.FileInfo
	named                                   unix.Stat_t
	data                                    []byte
	readBytes                               int
	readCalls                               uint32
	readPending                             bool
	completed                               bool
	retirementAttempted, retirementComplete bool
	firstError                              error
}

// Pure gate only. The actual parent prepaid reservation and child budget
// binding must already have completed under the original grant/debit. This
// routine cannot derive an allowance from the yet-unread evidence input.
func (r *workerFundingInputRead) check(ctx context.Context) error {
	if r == nil || r.owner == nil || r.reservation == nil || r.op == nil {
		return ErrInvalid
	}
	return r.reservation.requireOriginalFundingRead(r.owner, r, r.op, ctx)
}

func (r *workerFundingInputRead) openOriginal(ctx context.Context, parent int, name string, flags int) (*workerFundingOpenCall, error) {
	if err := r.check(ctx); err != nil {
		return nil, err
	}
	if r.openCount >= len(r.opens) {
		return nil, ErrInvalid
	}
	c := &r.opens[r.openCount]
	r.openCount++
	c.parent, c.name, c.flags, c.fd = parent, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, -1
	c.attempted = true
	fd, err := unix.Openat(parent, name, c.flags, 0)
	// Record the actual successful raw return BEFORE NewFile/Fstat/progress.
	c.fd, c.err, c.returned = fd, err, true
	if err != nil {
		return c, err
	}
	c.file = os.NewFile(uintptr(fd), "worker-original-funding-input")
	if c.file == nil {
		return c, ErrInvalid
	}
	c.wrapped = true
	if err = unix.Fstat(fd, &c.native); err != nil {
		return c, err
	}
	c.info, err = c.file.Stat()
	return c, err
}

func (r *workerFundingInputRead) walkOriginal(ctx context.Context) (*workerFundingOpenCall, error) {
	if !validInputPath(r.input.Path) {
		return nil, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Dir(r.input.Path), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}
	if len(parts) > 63 {
		return nil, ErrInvalid
	}
	current, err := r.openOriginal(ctx, unix.AT_FDCWD, "/", unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return current, err
	}
	for _, part := range parts {
		current, err = r.openOriginal(ctx, current.fd, part, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return current, err
		}
	}
	if current.native.Mode != unix.S_IFDIR|0700 || current.native.Uid != uint32(os.Geteuid()) {
		return current, ErrInvalid
	}
	return current, nil
}

func (r *workerFundingInputRead) readOriginal(ctx context.Context) (resultErr error) {
	if err := r.check(ctx); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil && r.firstError == nil {
			r.firstError = resultErr
		}
	}()
	if r.completed || r.openCount != 0 || r.input.Size <= 0 || r.input.Size > MaxEvidenceBytes || !validDigest(r.input.SHA256) {
		return ErrInvalid
	}
	var err error
	r.initialDirectory, err = r.walkOriginal(ctx)
	if err != nil {
		return err
	}
	var before unix.Stat_t
	if err = unix.Fstatat(r.initialDirectory.fd, filepath.Base(r.input.Path), &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if before.Mode != unix.S_IFREG|0600 || before.Nlink != 1 || before.Uid != uint32(os.Geteuid()) || nativeID(&before) != r.input.ID || before.Size != r.input.Size {
		return ErrInvalid
	}
	r.file, err = r.openOriginal(ctx, r.initialDirectory.fd, filepath.Base(r.input.Path), unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return err
	}
	if r.file.native.Mode != before.Mode || r.file.native.Nlink != 1 || nativeID(&r.file.native) != r.input.ID || r.file.info.Size() != r.input.Size {
		return ErrInvalid
	}
	r.before = r.file.info
	r.data = make([]byte, int(r.input.Size))
	for r.readBytes < len(r.data) {
		if err = r.check(ctx); err != nil {
			return err
		}
		if r.readCalls >= 32 {
			return ErrInvalid
		}
		r.readCalls++
		r.readPending = true
		n, readErr := r.file.file.ReadAt(r.data[r.readBytes:], int64(r.readBytes))
		r.readPending = false
		if n < 0 || n > len(r.data)-r.readBytes {
			return ErrInvalid
		}
		r.readBytes += n
		if readErr != nil && !(errors.Is(readErr, io.EOF) && r.readBytes == len(r.data)) {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	r.after, err = r.file.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(r.before, r.after) || r.before.Size() != r.after.Size() || !r.before.ModTime().Equal(r.after.ModTime()) || digest(r.data) != r.input.SHA256 {
		return ErrInvalid
	}
	r.currentDirectory, err = r.walkOriginal(ctx)
	if err != nil {
		return err
	}
	if nativeID(&r.currentDirectory.native) != nativeID(&r.initialDirectory.native) {
		return ErrInvalid
	}
	if err = unix.Fstatat(r.currentDirectory.fd, filepath.Base(r.input.Path), &r.named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if nativeID(&r.named) != r.input.ID || r.named.Mode != unix.S_IFREG|0600 || r.named.Nlink != 1 || r.named.Size != r.input.Size {
		return ErrInvalid
	}
	if err = r.check(ctx); err != nil {
		return err
	}
	r.completed = true
	return nil
}

// Retire only these exact original read-only input references after the whole
// pending funding comparison. It does not close service/native claims, release
// standing resources, remove any record or recycle its lifetime slot. A partial
// or unreturned close is retained, never retried or replaced by process exit.
func (r *workerFundingInputRead) retireReadReferencesOriginal(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	if !r.completed || r.firstError != nil || r.retirementAttempted || r.openCount <= 0 || r.openCount > len(r.opens) {
		return ErrInvalid
	}
	r.retirementAttempted = true
	for i := r.openCount - 1; i >= 0; i-- {
		c := &r.opens[i]
		if !c.attempted || !c.returned || c.err != nil || c.fd < 0 || !c.wrapped || c.file == nil || int(c.file.Fd()) != c.fd || c.closeAttempted {
			return ErrInvalid
		}
		if err := r.check(ctx); err != nil {
			return err
		}
		c.closeAttempted = true // retain BEFORE actual Close and abnormal unwind
		c.closeError = c.file.Close()
		c.closeReturned = true
		if c.closeError != nil {
			return c.closeError
		}
	}
	if err := r.check(ctx); err != nil {
		return err
	}
	r.retirementComplete = true
	return nil
}
