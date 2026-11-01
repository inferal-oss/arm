package residentservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"

	"getarm.org/internal/mesh"
	"golang.org/x/sys/unix"
)

// The raw owner is used only for existing native-file bookkeeping and the
// original operation defer. The caller retains the pending owner through all
// partial/unknown outcomes until actual owned teardown. Every legacy authority
// field stays empty. Worker
// journal, grant, debit and executable custody live on this distinct owner.
type workerCustody struct {
	self     *workerCustody
	raw      *owner
	operator *WorkerOperatorPending
	parent   *WorkerParentPending
	child    *WorkerChildPending
	path     string
	image    *workerRuntimeImage
	journal  workerJournalData
	grant    *WorkerGrantRecord
	debit    *WorkerOriginalDebit
	secret   [32]byte
	reads    []*workerNativeRead
	append   *workerNativeAppend
}

const workerImageBufferBytes = 256 << 10
const workerImageProofCalls = 8

// Linux captures the kernel's self image. Darwin captures an open startup-file
// snapshot and rechecks its public name. Neither method proves future stability
// or grants launch authority. No raw file, descriptor or pathname escapes.
type workerRuntimeProofData struct {
	SHA256 string
	Size   int64
	Method string
}

type workerRuntimeImage struct {
	self                                                                        *workerRuntimeImage
	owner                                                                       *workerCustody
	captureOp                                                                   *operation
	path, openPath, phase                                                       string
	captureTried                                                                bool
	openTried, rawRecorded, wrapTried, statTried, afterStatTried, nameStatTried bool
	rawFD                                                                       int
	openReturnedFD                                                              int
	openError                                                                   error
	file                                                                        *os.File
	beforeNative                                                                unix.Stat_t
	before, after, named                                                        os.FileInfo
	buffer                                                                      []byte
	hash                                                                        hash.Hash
	sum                                                                         []byte
	readCalls                                                                   uint32
	readBytes                                                                   int64
	readPending, hashPending                                                    bool
	complete                                                                    bool
	proof                                                                       workerRuntimeProofData
	proofCalls                                                                  [workerImageProofCalls]*workerImageProofCall
	firstError                                                                  error
}

type workerImageProofCall struct {
	owner                *workerRuntimeImage
	op                   *operation
	statTried, nameTried bool
	fileInfo, named      os.FileInfo
	complete             bool
	firstError           error
}

func workerImageSizeData(size int64) bool {
	return size > 0 && size <= mesh.MaxArtifactBytes
}

func (w *workerCustody) newRuntimeImagePendingOriginal() (*workerRuntimeImage, error) {
	if w == nil || w.self != w || w.raw == nil {
		return nil, ErrInvalid
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if !w.typedOriginalLocked() || !w.legacyEmptyLocked() || w.raw.firstError != nil || w.raw.active == nil || w.image != nil {
		return nil, ErrInvalid
	}
	if w.raw.active.kind != "worker-operator-plan" && w.raw.active.kind != "worker-parent-open" {
		return nil, ErrInvalid
	}
	i := &workerRuntimeImage{owner: w, captureOp: w.raw.active, rawFD: -1, openReturnedFD: -1, phase: "registered_before_image_effects"}
	i.self = i
	w.image = i
	return i, nil
}

func (w *workerCustody) captureRuntimeImageOriginal(ctx context.Context) (*workerRuntimeImage, error) {
	i, err := w.newRuntimeImagePendingOriginal()
	if err != nil {
		return nil, err
	}
	// The exact holder is already registered, including every error return.
	err = i.captureBodyOriginal(ctx)
	if err != nil {
		w.raw.mu.Lock()
		if i.firstError == nil {
			i.firstError = err
		}
		w.raw.mu.Unlock()
	}
	return i, err
}

func (i *workerRuntimeImage) phaseOriginal(phase string) {
	i.owner.raw.mu.Lock()
	i.phase = phase
	i.owner.raw.mu.Unlock()
	i.owner.raw.files.progress(phase)
}

func sameWorkerImageStatData(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime()) && x.Uid == y.Uid && x.Nlink == y.Nlink
}

func (i *workerRuntimeImage) captureBodyOriginal(ctx context.Context) error {
	if i == nil || i.self != i || i.owner == nil || i.owner.raw == nil {
		return ErrInvalid
	}
	w := i.owner
	r := w.raw
	r.mu.Lock()
	valid := w.image == i && w.typedOriginalLocked() && w.legacyEmptyLocked() && r.active == i.captureOp && !i.captureTried && !i.openTried && !i.complete && i.firstError == nil && r.firstError == nil
	if valid {
		i.captureTried = true
	}
	r.mu.Unlock()
	if !valid {
		return ErrInvalid
	}
	if err := workerContextData(ctx); err != nil {
		return err
	}
	i.phaseOriginal("worker_image_original_executable_path_pending")
	path, err := os.Executable()
	r.mu.Lock()
	i.path = path
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if !boundedText(path, 4096) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalid
	}
	openPath, method := path, "startup-open-file-snapshot"
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW
	if runtime.GOOS == "linux" {
		openPath, method = "/proc/self/exe", "kernel-self-executable"
		flags &^= unix.O_NOFOLLOW
	}
	r.mu.Lock()
	i.openPath = openPath
	i.openTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_open_pending")
	fd, err := unix.Open(openPath, flags, 0)
	// Keep any returned original descriptor BEFORE wrapping, stat or allocation.
	r.mu.Lock()
	i.openReturnedFD, i.openError = fd, err
	if err == nil && fd >= 0 {
		i.rawFD, i.rawRecorded = fd, true
	}
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if fd < 0 {
		return ErrInvalid
	}
	r.mu.Lock()
	i.wrapTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_wrap_pending")
	file := os.NewFile(uintptr(fd), "worker-resident-runtime-image")
	r.mu.Lock()
	i.file = file
	r.mu.Unlock()
	if file == nil {
		return ErrInvalid
	}
	r.mu.Lock()
	i.statTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_stat_pending")
	var native unix.Stat_t
	err = unix.Fstat(fd, &native)
	r.mu.Lock()
	i.beforeNative = native
	r.mu.Unlock()
	if err != nil {
		return err
	}
	before, err := file.Stat()
	r.mu.Lock()
	i.before = before
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if uint32(native.Mode)&unix.S_IFMT != unix.S_IFREG || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || !workerImageSizeData(before.Size()) || native.Size != before.Size() {
		return ErrInvalid
	}
	nativeInfo, ok := before.Sys().(*syscall.Stat_t)
	if !ok || uint64(nativeInfo.Dev) != uint64(native.Dev) || uint64(nativeInfo.Ino) != uint64(native.Ino) || nativeInfo.Uid != native.Uid || nativeInfo.Nlink != native.Nlink || uint32(nativeInfo.Mode) != uint32(native.Mode) {
		return ErrInvalid
	}
	buffer := make([]byte, workerImageBufferBytes)
	r.mu.Lock()
	i.buffer = buffer
	r.mu.Unlock()
	hasher := sha256.New()
	r.mu.Lock()
	i.hash = hasher
	r.mu.Unlock()
	for at := int64(0); at < before.Size(); {
		if err = ctx.Err(); err != nil {
			return err
		}
		r.mu.Lock()
		if int64(i.readCalls) >= mesh.MaxArtifactBytes/workerImageBufferBytes+1 {
			r.mu.Unlock()
			return ErrInvalid
		}
		i.readCalls++
		i.readPending = true
		r.mu.Unlock()
		amount := int64(len(buffer))
		if left := before.Size() - at; left < amount {
			amount = left
		}
		i.phaseOriginal("worker_image_original_read_pending")
		n, readErr := file.ReadAt(buffer[:int(amount)], at)
		at += int64(n)
		r.mu.Lock()
		i.readBytes = at
		i.readPending = false
		r.mu.Unlock()
		if readErr != nil && !(errors.Is(readErr, io.EOF) && at == before.Size()) {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
		r.mu.Lock()
		i.hashPending = true
		r.mu.Unlock()
		i.phaseOriginal("worker_image_original_hash_pending")
		written, hashErr := hasher.Write(buffer[:n])
		r.mu.Lock()
		i.hashPending = false
		r.mu.Unlock()
		if hashErr != nil {
			return hashErr
		}
		if written != n {
			return io.ErrShortWrite
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	i.afterStatTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_after_stat_pending")
	after, err := file.Stat()
	r.mu.Lock()
	i.after = after
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if !sameWorkerImageStatData(before, after) {
		return ErrInvalid
	}
	r.mu.Lock()
	i.nameStatTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_public_name_pending")
	named, err := os.Stat(path)
	r.mu.Lock()
	i.named = named
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if !sameWorkerImageStatData(before, named) {
		return ErrInvalid
	}
	sum := hasher.Sum(nil)
	r.mu.Lock()
	i.sum = sum
	r.mu.Unlock()
	if len(sum) != sha256.Size {
		return ErrInvalid
	}
	proof := workerRuntimeProofData{SHA256: hex.EncodeToString(sum), Size: before.Size(), Method: method}
	r.mu.Lock()
	i.proof = proof
	i.complete = true
	i.phase = "snapshot_complete_no_launch_authority"
	r.mu.Unlock()
	return nil
}

func (i *workerRuntimeImage) proofOriginal(ctx context.Context) (proof workerRuntimeProofData, resultErr error) {
	if i == nil || i.self != i || i.owner == nil || i.owner.self != i.owner || i.owner.raw == nil {
		return proof, ErrInvalid
	}
	w := i.owner
	r := w.raw
	r.mu.Lock()
	if w.image != i || !w.typedOriginalLocked() || !w.legacyEmptyLocked() {
		r.mu.Unlock()
		return proof, ErrInvalid
	}
	if i.firstError != nil {
		err := i.firstError
		r.mu.Unlock()
		return proof, err
	}
	if r.firstError != nil {
		err := r.firstError
		r.mu.Unlock()
		return proof, err
	}
	if r.active == nil || !i.complete || !i.rawRecorded || i.file == nil || !validDigest(i.proof.SHA256) || !workerImageSizeData(i.proof.Size) {
		r.mu.Unlock()
		return proof, ErrInvalid
	}
	var call *workerImageProofCall
	for at, prior := range i.proofCalls {
		if prior == nil {
			call = &workerImageProofCall{owner: i, op: r.active}
			i.proofCalls[at] = call
			break
		}
		if prior.owner != i || !prior.complete || prior.firstError != nil {
			r.mu.Unlock()
			return proof, ErrInvalid
		}
	}
	r.mu.Unlock()
	if call == nil {
		return proof, ErrInvalid
	}
	fail := func(err error) (workerRuntimeProofData, error) {
		r.mu.Lock()
		call.firstError = err
		if i.firstError == nil {
			i.firstError = err
		}
		r.mu.Unlock()
		return workerRuntimeProofData{}, err
	}
	if err := workerContextData(ctx); err != nil {
		return fail(err)
	}
	r.mu.Lock()
	call.statTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_reproof_stat_pending")
	info, err := i.file.Stat()
	r.mu.Lock()
	call.fileInfo = info
	r.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if !sameWorkerImageStatData(i.before, info) {
		return fail(ErrInvalid)
	}
	r.mu.Lock()
	call.nameTried = true
	r.mu.Unlock()
	i.phaseOriginal("worker_image_original_reproof_name_pending")
	named, err := os.Stat(i.path)
	r.mu.Lock()
	call.named = named
	r.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	if !sameWorkerImageStatData(i.before, named) {
		return fail(ErrInvalid)
	}
	r.mu.Lock()
	call.complete = true
	proof = i.proof
	r.mu.Unlock()
	return proof, nil
}

func newWorkerCustodyData(home string) *workerCustody {
	w := &workerCustody{path: home, raw: &owner{path: home}}
	w.self = w
	w.raw.files = &nativeFiles{owner: w.raw}
	return w
}

// Caller holds raw.mu. No old owner method can find its required old issuer,
// parent, child, grant or charge through this raw bookkeeping holder.
func (w *workerCustody) legacyEmptyLocked() bool {
	if w == nil || w.self != w || w.raw == nil {
		return false
	}
	r := w.raw
	if r.files == nil || r.files.owner != r || r.path != w.path {
		return false
	}
	if r.active != nil {
		if r.active.owner != r {
			return false
		}
		if !workerOperationKindData(r.active.kind) {
			return false
		}
	}
	// Refuse every other field, including future additions, unless zero. The
	// finite original owner type fixes the reflection walk's size. No legacy
	// journal, secret, native recipient, image, protocol or issuer is installed.
	value := reflect.ValueOf(r).Elem()
	for i := 0; i < value.NumField(); i++ {
		switch value.Type().Field(i).Name {
		case "mu", "path", "files", "active", "firstError", "openedFDs", "closedFDs", "liveFDs":
		default:
			if !value.Field(i).IsZero() {
				return false
			}
		}
	}
	return true
}

func (w *workerCustody) operationLocked(kind string) (*operation, error) {
	if !workerOperationKindData(kind) || !w.typedOriginalLocked() || !w.legacyEmptyLocked() {
		return nil, ErrInvalid
	}
	r := w.raw
	if r.active != nil {
		return nil, ErrPending
	}
	if r.firstError != nil {
		return nil, r.firstError
	}
	op := &operation{owner: r, kind: kind, phase: "worker_registered_before_native_effect"}
	r.active = op
	return op, nil
}

func (w *workerCustody) typedOriginalLocked() bool {
	if w == nil || w.self != w || w.raw == nil {
		return false
	}
	if p := w.operator; p != nil {
		return w.parent == nil && w.child == nil && p.self == p && p.custody == w
	}
	p := w.parent
	if p != nil {
		return w.child == nil && p.self == p && p.custody == w
	}
	c := w.child
	return c != nil && c.self == c && c.custody == w && c.source != nil && c.journal != nil
}

func workerOperationKindData(kind string) bool {
	switch kind {
	case "worker-operator-plan", "worker-operator-apply", "worker-operator-inspect", "worker-parent-open", "worker-parent-debit":
		return true
	case "worker-child-receive", "worker-child-runtime", "worker-child-environment", "worker-child-charge", "worker-charge-current", "worker-child-birth", "worker-birth-current":
		return true
	default:
		return false
	}
}

// Concrete journal sources only. No caller interface/factory or raw getter can
// replace original parent/operator custody or the delegated child's view.
func (w *workerCustody) journalSourcePresentLocked() bool {
	if w.child != nil {
		return w.child.journal != nil && w.child.source != nil
	}
	return w.raw.files.journal != nil
}
func (w *workerCustody) journalStatOriginal(ctx context.Context) (os.FileInfo, error) {
	if w.child != nil {
		return w.child.journal.StatOriginal(ctx)
	}
	return w.raw.files.journal.file.Stat()
}
func (w *workerCustody) journalReadOriginal(ctx context.Context, data []byte, offset int64) (int, error) {
	if w.child != nil {
		return w.child.journal.ReadAtOriginal(ctx, data, offset)
	}
	return w.raw.files.journal.file.ReadAt(data, offset)
}
func (w *workerCustody) journalWriteOriginal(ctx context.Context, data []byte, offset int64) (int, error) {
	if w.child != nil {
		return w.child.journal.WriteAtOriginal(ctx, data, offset)
	}
	return w.raw.files.journal.file.WriteAt(data, offset)
}
func (w *workerCustody) journalSyncOriginal(ctx context.Context) error {
	if w.child != nil {
		return w.child.journal.SyncOriginal(ctx)
	}
	return w.raw.files.journal.file.Sync()
}
func (w *workerCustody) validateJournalSourceOriginal(ctx context.Context) error {
	if c := w.child; c != nil {
		if err := c.source.RequireOriginalWorkerCore(); err != nil {
			return err
		}
		ids, err := c.source.ValidateOriginal(ctx)
		if err != nil {
			return err
		}
		if ids != c.identities {
			return ErrInvalid
		}
		return nil
	}
	return privateNative(w.raw.files.journal, false)
}

func workerContextData(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return ctx.Err()
}

// DATA inspection never reconstructs an owner, releases a debit, closes native
// custody or establishes that a worker-enabled runtime was admitted.
type WorkerCustodyObservation struct {
	OperationPending               bool   `json:"operation_pending"`
	OperationKind                  string `json:"operation_kind"`
	OperationPhase                 string `json:"operation_phase"`
	OperationAbnormal              bool   `json:"operation_abnormal"`
	FirstError                     error  `json:"-"`
	LastPublishedRawFileReferences int    `json:"last_published_raw_file_references"`
	NativeCensusUncertain          bool   `json:"native_census_uncertain"`
	ImageAttempted                 bool   `json:"image_attempted"`
	ImageSnapshotComplete          bool   `json:"image_snapshot_complete"`
	JournalReads                   int    `json:"journal_reads"`
	AppendAttempted                bool   `json:"append_attempted"`
	AppendWritten                  int    `json:"append_written"`
	AppendSyncAttempted            bool   `json:"append_sync_attempted"`
	AppendCommitted                bool   `json:"append_committed"`
	NativeOutcome                  string `json:"native_outcome"`
}

func (w *workerCustody) observeData() WorkerCustodyObservation {
	invalid := WorkerCustodyObservation{FirstError: ErrInvalid, NativeOutcome: "unknown"}
	if w == nil || w.self != w || w.raw == nil {
		return invalid
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if !w.legacyEmptyLocked() {
		return invalid
	}
	o := WorkerCustodyObservation{FirstError: w.raw.firstError, JournalReads: len(w.reads), NativeOutcome: "unknown", LastPublishedRawFileReferences: w.raw.liveFDs, NativeCensusUncertain: true}
	if w.image != nil {
		o.ImageAttempted, o.ImageSnapshotComplete = true, w.image.complete
	}
	if op := w.raw.active; op != nil {
		o.OperationPending, o.OperationKind, o.OperationPhase, o.OperationAbnormal = true, op.kind, op.phase, op.abnormal
	}
	// Native slots change on their sole operation's goroutine. Read only the
	// mutex-published census; it may lag a pending native effect and never
	// enumerates image/runtime internals or proves closure/capacity credit.
	if a := w.append; a != nil {
		o.AppendAttempted, o.AppendWritten, o.AppendSyncAttempted, o.AppendCommitted = true, a.written, a.syncTried, a.committed
	}
	return o
}
