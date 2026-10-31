package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// This parent-side pending owner must be funded/retained by original birth
// BEFORE PrepareOriginal. It grants nothing and starts no process. All files,
// partial effects and errors remain retained; there is no error cleanup API.
type ResidentImagePinPending struct{ *residentImagePinState }
type residentImagePinState struct {
	p1Binding                                       *ResidentImageP1Pending
	p1ValidationCount                               uint8
	mu                                              sync.Mutex
	self                                            *ResidentImagePinPending
	home, debit, expectedSHA                        string
	expectedSize                                    int64
	source                                          *ResidentRuntimePending
	active                                          *residentPinOperation
	tried, ready                                    bool
	firstError                                      error
	files                                           []*residentPinFile
	homeFile, images, directory, writable, readonly *residentPinFile
	path                                            string
	copied                                          int64
	buffer                                          []byte
	copyHash, verifyHash                            hash.Hash
	verified                                        int64
	currentRead                                     *residentPinRead
	currentWrite                                    *residentPinWrite
	observed                                        ResidentImagePinInspection
	p1InventoryTried                                bool
}
type residentPinRead struct {
	offset              int64
	requested, returned int
	err                 error
}
type residentPinWrite struct {
	offset              int64
	requested, returned int
	err                 error
}

type residentPinOperation struct {
	phase    string
	abnormal bool
	cause    any
}
type residentPinFile struct {
	fd                 int
	file               *os.File
	before             os.FileInfo
	closeTried, closed bool
	closeError         error
}
type ResidentImagePinInspection struct {
	PrepareAttempted, Prepared, OperationPending, OperationAbnormal bool
	Phase                                                           string
	FilesRetained, ReferenceCloseAttempts, ReferenceCloses          int
	CopiedBytes, VerifiedBytes                                      int64
	ReadonlyRetained, WritableCloseKnown, SourceReadonlyCloseKnown  bool
	FirstError                                                      error
	NativeOutcome                                                   string
}

var errResidentPinOriginal = errors.New("original resident image pin required; preserve pending custody")

func NewResidentImagePinPending(home, debit, sha string, size int64) (*ResidentImagePinPending, error) {
	valid := func(s string) bool {
		if len(s) != 64 || strings.ToLower(s) != s {
			return false
		}
		_, e := hex.DecodeString(s)
		return e == nil
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || len(home) > 4096 || strings.ContainsRune(home, 0) || len(strings.Split(strings.TrimPrefix(home, "/"), "/")) > 64 || !valid(debit) || !valid(sha) || size < 1 || size > MaxArtifactBytes {
		return nil, errResidentPinOriginal
	}
	o := &ResidentImagePinPending{&residentImagePinState{home: home, debit: debit, expectedSHA: sha, expectedSize: size, source: NewResidentRuntimePending()}}
	o.self = o
	return o, nil
}
func (o *ResidentImagePinPending) phase(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("pin effect without original operation")
	}
	o.active.phase = s
}
func (o *ResidentImagePinPending) publish() {
	o.mu.Lock()
	defer o.mu.Unlock()
	v := &o.observed
	v.FilesRetained = len(o.files)
	v.CopiedBytes, v.VerifiedBytes = o.copied, o.verified
	v.ReadonlyRetained = o.readonly != nil
	v.WritableCloseKnown = o.writable != nil && o.writable.closed
	v.SourceReadonlyCloseKnown = o.source.InspectOriginal().SourceReferenceCloseKnown
	v.ReferenceCloseAttempts = 0
	v.ReferenceCloses = 0
	for _, f := range o.files {
		if f.closeTried {
			v.ReferenceCloseAttempts++
		}
		if f.closed {
			v.ReferenceCloses++
		}
	}
}
func (o *ResidentImagePinPending) finish(op *residentPinOperation, normal *bool, err *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op {
		o.mu.Unlock()
		panic("pin operation identity lost")
	}
	if !*normal {
		op.abnormal = true
		op.cause = cause
		if o.firstError == nil {
			o.firstError = &residentRuntimeAbnormal{cause: cause}
		}
	} else {
		if *err != nil && o.firstError == nil {
			o.firstError = *err
		}
		o.ready = *err == nil
		o.active = nil
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (o *ResidentImagePinPending) retain(fd int) (*residentPinFile, error) {
	f := &residentPinFile{fd: fd}
	o.files = append(o.files, f)
	o.publish() // raw first
	o.phase("original_pin_file_materialization")
	f.file = os.NewFile(uintptr(fd), "original resident pin reference")
	if f.file == nil {
		return f, errResidentPinOriginal
	}
	var err error
	f.before, err = f.file.Stat()
	return f, err
}
func (o *ResidentImagePinPending) open(ctx context.Context, parent int, name string, flags int) (*residentPinFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	live := 0
	for _, f := range o.files {
		if !f.closed {
			live++
		}
	}
	if live >= 8 {
		return nil, errResidentPinOriginal
	}
	o.phase("original_pin_open")
	fd, err := unix.Openat(parent, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if fd >= 0 {
		f, e := o.retain(fd)
		return f, errors.Join(err, e)
	}
	return nil, err
}
func (o *ResidentImagePinPending) closeReference(f *residentPinFile) error {
	if f == nil || f.closeTried {
		return errResidentPinOriginal
	}
	f.closeTried = true
	o.publish()
	o.phase("original_pin_reference_close")
	f.closeError = f.file.Close()
	f.closed = f.closeError == nil
	o.publish()
	return f.closeError
}
func (o *ResidentImagePinPending) private(f *residentPinFile, dir bool, mode os.FileMode) error {
	if f == nil || f.file == nil || f.closed || f.closeTried {
		return errResidentPinOriginal
	}
	info, err := f.file.Stat()
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(f.fd, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != mode || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.IsDir() != dir || !dir && (!info.Mode().IsRegular() || st.Nlink != 1) || !os.SameFile(f.before, info) {
		return errResidentPinOriginal
	}
	return nil
}
func (o *ResidentImagePinPending) walkHome(ctx context.Context) (*residentPinFile, error) {
	current, err := o.open(ctx, unix.AT_FDCWD, "/", unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return current, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(o.home, "/"), "/") {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		next, e := o.open(ctx, current.fd, part, unix.O_RDONLY|unix.O_DIRECTORY)
		if e != nil {
			return next, e
		}
		if e = o.closeReference(current); e != nil {
			return next, e
		}
		current = next
	}
	return current, o.private(current, true, 0700)
}
func (o *ResidentImagePinPending) mkdir(ctx context.Context, parent *residentPinFile, name string) (*residentPinFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.phase("original_pin_exclusive_directory_create")
	if err := unix.Mkdirat(parent.fd, name, 0700); err != nil {
		return nil, err
	}
	f, err := o.open(ctx, parent.fd, name, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return f, err
	}
	return f, o.private(f, true, 0700)
}
func (o *ResidentImagePinPending) PrepareOriginal(ctx context.Context) (resultErr error) {
	if o == nil || o.residentImagePinState == nil || o.self != o {
		return errResidentPinOriginal
	}
	o.mu.Lock()
	if o.firstError != nil {
		e := o.firstError
		o.mu.Unlock()
		return e
	}
	if o.tried || o.active != nil {
		o.mu.Unlock()
		return errResidentPinOriginal
	}
	o.tried = true
	op := &residentPinOperation{phase: "registered_before_effect"}
	o.active = op
	o.mu.Unlock()
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.prepare(ctx)
	normal = true
	return resultErr
}
func (o *ResidentImagePinPending) prepare(ctx context.Context) error {
	if ctx == nil {
		return errResidentPinOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.source.CaptureOriginal(ctx); err != nil {
		return err
	}
	proof, err := o.source.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	if proof.SHA256 != o.expectedSHA || proof.Size != o.expectedSize {
		return errResidentPinOriginal
	}
	o.homeFile, err = o.walkHome(ctx)
	if err != nil {
		return err
	}
	o.images, err = o.mkdir(ctx, o.homeFile, "resident-images")
	if err != nil {
		return err
	} // EEXIST never adopted
	o.directory, err = o.mkdir(ctx, o.images, "pin-"+o.debit)
	if err != nil {
		return err
	}
	o.writable, err = o.open(ctx, o.directory.fd, "arm", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	o.phase("original_pin_created_file_mode")
	if err = unix.Fchmod(o.writable.fd, 0600); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = o.private(o.writable, false, 0600); err != nil {
		return err
	}
	o.buffer = make([]byte, 64<<10)
	o.copyHash = sha256.New()
	for o.copied < o.expectedSize {
		if err = ctx.Err(); err != nil {
			return err
		}
		want := min(int64(len(o.buffer)), o.expectedSize-o.copied)
		o.phase("original_pin_source_read")
		o.currentRead = &residentPinRead{offset: o.copied, requested: int(want)}
		n, e := o.source.file.ReadAt(o.buffer[:int(want)], o.copied)
		o.currentRead.returned, o.currentRead.err = n, e
		if e != nil && !(errors.Is(e, io.EOF) && o.copied+int64(n) == o.expectedSize) {
			return e
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		if _, err = o.copyHash.Write(o.buffer[:n]); err != nil {
			return err
		}
		for at, calls := 0, 0; at < n; calls++ {
			if err = ctx.Err(); err != nil {
				return err
			}
			if calls >= 32 {
				return io.ErrShortWrite
			}
			o.phase("original_pin_write")
			o.currentWrite = &residentPinWrite{offset: o.copied + int64(at), requested: n - at}
			written, e := o.writable.file.WriteAt(o.buffer[at:n], o.copied+int64(at))
			o.currentWrite.returned, o.currentWrite.err = written, e
			at += written
			if e != nil {
				return e
			}
			if written == 0 {
				return io.ErrNoProgress
			}
		}
		o.copied += int64(n)
		o.publish()
	}
	if hex.EncodeToString(o.copyHash.Sum(nil)) != o.expectedSHA {
		return errResidentPinOriginal
	}
	if _, err = o.source.ValidateOriginal(ctx); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	o.phase("original_pin_file_sync")
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = o.writable.file.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = unix.Fchmod(o.writable.fd, 0500); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = o.writable.file.Sync(); err != nil {
		return err
	}
	o.readonly, err = o.open(ctx, o.directory.fd, "arm", unix.O_RDONLY)
	o.publish()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = o.private(o.readonly, false, 0500); err != nil {
		return err
	}
	a, err := o.writable.file.Stat()
	if err != nil {
		return err
	}
	b, err := o.readonly.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) || a.Size() != o.expectedSize {
		return errResidentPinOriginal
	}
	// Actual successful writable close is mandatory, before any exec is possible.
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = o.closeReference(o.writable); err != nil {
		return err
	}
	o.phase("original_pin_namespace_sync")
	for _, f := range []*residentPinFile{o.directory, o.images, o.homeFile} {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = f.file.Sync(); err != nil {
			return err
		}
	}
	if err = o.verifyReadonly(ctx); err != nil {
		return err
	}
	if err = o.validateCurrentNames(ctx); err != nil {
		return err
	}
	if err = o.source.retirePinSourceOriginal(ctx, o); err != nil {
		return err
	}
	o.publish()
	if err = ctx.Err(); err != nil {
		return err
	}
	o.path = filepath.Join(o.home, "resident-images", "pin-"+o.debit, "arm")
	// No executable/FD/Close/Start lending exists in this cut. P1 sealing must
	// independently bind these SAME originals, current names and actual image.
	return nil
}

// verifyReadonly hashes the actual retained destination after the sole writable
// original has positively closed. Matching source write bytes alone is not enough.
func (o *ResidentImagePinPending) verifyReadonly(ctx context.Context) error {
	if !o.writable.closed || o.readonly == nil {
		return errResidentPinOriginal
	}
	if err := o.private(o.readonly, false, 0500); err != nil {
		return err
	}
	o.phase("original_pin_readonly_flags")
	flags, err := unix.FcntlInt(uintptr(o.readonly.fd), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY {
		return errResidentPinOriginal
	}
	before, err := o.readonly.file.Stat()
	if err != nil {
		return err
	}
	if before.Size() != o.expectedSize {
		return errResidentPinOriginal
	}
	o.verifyHash = sha256.New()
	for o.verified < o.expectedSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := min(int64(len(o.buffer)), o.expectedSize-o.verified)
		o.phase("original_pin_readonly_verification_read")
		o.currentRead = &residentPinRead{offset: o.verified, requested: int(want)}
		n, err := o.readonly.file.ReadAt(o.buffer[:int(want)], o.verified)
		o.currentRead.returned, o.currentRead.err = n, err
		if err != nil && !(errors.Is(err, io.EOF) && o.verified+int64(n) == o.expectedSize) {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		if _, err := o.verifyHash.Write(o.buffer[:n]); err != nil {
			return err
		}
		o.verified += int64(n)
		o.publish()
	}
	after, err := o.readonly.file.Stat()
	if err != nil {
		return err
	}
	if !sameArtifactStat(before, after) || hex.EncodeToString(o.verifyHash.Sum(nil)) != o.expectedSHA {
		return errResidentPinOriginal
	}
	return nil
}

// Rewalk independently without following symlinks, then bind every current
// namespace component to the SAME original object. These observations are not
// an atomic guarantee against a malicious process with the same OS identity.
func (o *ResidentImagePinPending) validateCurrentNames(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.phase("original_pin_current_home_rewalk")
	current, err := o.walkHome(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(current.before, o.homeFile.before) {
		return errResidentPinOriginal
	}
	checks := []struct {
		name     string
		original *residentPinFile
		dir      bool
		mode     os.FileMode
	}{
		{"resident-images", o.images, true, 0700},
		{"pin-" + o.debit, o.directory, true, 0700},
		{"arm", o.readonly, false, 0500},
	}
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := o.private(check.original, check.dir, check.mode); err != nil {
			return err
		}
		flags := unix.O_RDONLY
		if check.dir {
			flags |= unix.O_DIRECTORY
		}
		next, err := o.open(ctx, current.fd, check.name, flags)
		if err != nil {
			return err
		}
		if err := o.private(next, check.dir, check.mode); err != nil {
			return err
		}
		if !os.SameFile(next.before, check.original.before) {
			return errResidentPinOriginal
		}
		if !check.dir && next.before.Size() != o.expectedSize {
			return errResidentPinOriginal
		}
		if err := o.closeReference(current); err != nil {
			return err
		}
		current = next
	}
	return o.closeReference(current)
}
func (o *ResidentImagePinPending) InspectOriginal() ResidentImagePinInspection {
	if o == nil || o.residentImagePinState == nil || o.self != o {
		return ResidentImagePinInspection{FirstError: errResidentPinOriginal, NativeOutcome: "unknown"}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	v := o.observed
	v.PrepareAttempted, v.Prepared = o.tried, o.ready
	v.FirstError, v.NativeOutcome = o.firstError, "unknown"
	if o.active != nil {
		v.OperationPending = true
		v.OperationAbnormal = o.active.abnormal
		v.Phase = o.active.phase
	}
	return v
}
