package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"getarm.org/internal/buildinfo"
	"golang.org/x/sys/unix"
)

// ResidentRuntimePending retains the original image capture before any path,
// descriptor, stat or hash effect. Root composition must fund it before Capture.
// It issues no service authority, immutable pin or native closure observation.
// The actual RuntimeExecutable stays private; no borrower receives Close.
type ResidentRuntimePending struct{ *residentRuntimeState }

type residentRuntimeState struct {
	mu                                   sync.Mutex // publication only, never filesystem I/O
	self                                 *ResidentRuntimePending
	active                               *residentRuntimeOperation
	captureTried, captured               bool
	pinSourceCloseTried, pinSourceClosed bool
	pinSourceCloseError                  error
	firstError                           error
	path, openPath                       string
	rawFD                                int
	rawRetained                          bool
	file                                 *os.File
	before, after, named                 os.FileInfo
	buffer                               []byte
	hasher                               hash.Hash
	image                                *RuntimeExecutable
	observed                             ResidentRuntimeInspection
}
type residentRuntimeOperation struct {
	kind, phase string
	abnormal    bool
	cause       any
}
type ResidentRuntimeInspection struct {
	CaptureAttempted, Captured                               bool
	RawDescriptorRetained, FileRetained, ImageRetained       bool
	SourceReferenceCloseAttempted, SourceReferenceCloseKnown bool
	OperationPending, OperationAbnormal                      bool
	OperationKind, OperationPhase                            string
	BytesRead                                                int64
	ReadCalls                                                uint64
	FirstError                                               error
	NativeOutcome                                            string
}

var errResidentRuntimeOriginal = errors.New("original resident runtime owner required")
var ErrResidentRuntimePending = errors.New("original resident runtime operation pending; retain and inspect")

type residentRuntimeAbnormal struct{ cause any }

func (e *residentRuntimeAbnormal) Error() string {
	return "original resident runtime operation abnormal; custody unresolved"
}
func (e *residentRuntimeAbnormal) Unwrap() error { err, _ := e.cause.(error); return err }

func NewResidentRuntimePending() *ResidentRuntimePending {
	o := &ResidentRuntimePending{residentRuntimeState: &residentRuntimeState{rawFD: -1}}
	o.self = o
	return o
}
func (o *ResidentRuntimePending) begin(kind string) (*residentRuntimeOperation, error) {
	if o == nil || o.residentRuntimeState == nil || o.self != o {
		return nil, errResidentRuntimeOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active != nil {
		return nil, ErrResidentRuntimePending
	}
	if o.firstError != nil {
		return nil, o.firstError
	}
	switch kind {
	case "capture":
		if o.captureTried {
			return nil, errResidentRuntimeOriginal
		}
		o.captureTried = true
	case "validate":
		if !o.captured || o.pinSourceCloseTried {
			return nil, errResidentRuntimeOriginal
		}
	case "pin-source-close":
		if !o.captured || o.pinSourceCloseTried || o.file == nil || o.image == nil {
			return nil, errResidentRuntimeOriginal
		}
	default:
		return nil, errResidentRuntimeOriginal
	}
	op := &residentRuntimeOperation{kind: kind, phase: "registered_before_effect"}
	o.active = op
	return op, nil
}
func (o *ResidentRuntimePending) phase(phase string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("runtime effect lacks original operation")
	}
	o.active.phase = phase
}
func (o *ResidentRuntimePending) finish(op *residentRuntimeOperation, normal *bool, resultErr *error) {
	cause := recover()
	o.mu.Lock()
	if o.active != op {
		o.mu.Unlock()
		panic("original runtime operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if o.firstError == nil {
			o.firstError = &residentRuntimeAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && o.firstError == nil {
			o.firstError = *resultErr
		}
		if *resultErr == nil && op.kind == "capture" {
			o.captured = true
		}
		o.active = nil
	}
	o.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (o *ResidentRuntimePending) CaptureOriginal(ctx context.Context) (resultErr error) {
	op, err := o.begin("capture")
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.capture(ctx)
	normal = true
	return resultErr
}
func (o *ResidentRuntimePending) capture(ctx context.Context) error {
	if ctx == nil {
		return errResidentRuntimeOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.phase("original_executable_path_pending")
	path, err := os.Executable()
	o.path = path
	if err != nil {
		return err
	}
	if len(path) == 0 || len(path) > 4096 {
		return errResidentRuntimeOriginal
	}
	o.openPath = path
	method := "startup-open-file-snapshot"
	if runtime.GOOS == "linux" {
		o.openPath = "/proc/self/exe"
		method = "kernel-self-executable"
	}
	o.phase("original_executable_open_pending")
	fd, err := unix.Open(o.openPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	// Retain any returned raw original before interpreting partial errors or
	// wrapping/stat/allocation. A failed call never discards a returned handle.
	if fd >= 0 {
		o.rawFD, o.rawRetained = fd, true
		o.mu.Lock()
		o.observed.RawDescriptorRetained = true
		o.mu.Unlock()
	}
	if err != nil {
		return err
	}
	if !o.rawRetained {
		return errResidentRuntimeOriginal
	}
	o.phase("original_executable_materialization_pending")
	o.file = os.NewFile(uintptr(fd), "original resident runtime image")
	if o.file == nil {
		return errResidentRuntimeOriginal
	}
	o.mu.Lock()
	o.observed.FileRetained = true
	o.mu.Unlock()
	o.phase("original_executable_stat_pending")
	o.before, err = o.file.Stat()
	if err != nil {
		return err
	}
	if !o.before.Mode().IsRegular() || o.before.Mode().Perm()&0022 != 0 || o.before.Size() < 1 || o.before.Size() > MaxArtifactBytes {
		return errResidentRuntimeOriginal
	}
	o.buffer = make([]byte, 64<<10)
	o.hasher = sha256.New()
	var offset int64
	var calls uint64
	for offset < o.before.Size() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if calls >= uint64(MaxArtifactBytes/(64<<10))+2 {
			return io.ErrNoProgress
		}
		want := min(int64(len(o.buffer)), o.before.Size()-offset)
		o.phase("original_executable_read_pending")
		n, readErr := o.file.ReadAt(o.buffer[:int(want)], offset)
		calls++
		offset += int64(n)
		o.mu.Lock()
		o.observed.BytesRead, o.observed.ReadCalls = offset, calls
		o.mu.Unlock()
		if n > 0 {
			if _, err := o.hasher.Write(o.buffer[:n]); err != nil {
				return err
			}
		}
		if readErr != nil && !(errors.Is(readErr, io.EOF) && offset == o.before.Size()) {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	o.phase("original_executable_after_stat_pending")
	o.after, err = o.file.Stat()
	if err != nil {
		return err
	}
	if !sameArtifactStat(o.before, o.after) {
		return errResidentRuntimeOriginal
	}
	o.image = &RuntimeExecutable{file: o.file, path: o.path, before: o.before, proof: RuntimeProof{SHA256: hex.EncodeToString(o.hasher.Sum(nil)), Size: offset, Version: buildinfo.Version, Protocol: LifecycleProtocol, Method: method, Captured: time.Now().UTC()}}
	o.mu.Lock()
	o.observed.ImageRetained = true
	o.mu.Unlock()
	return o.validate(ctx)
}
func (o *ResidentRuntimePending) validate(ctx context.Context) error {
	if ctx == nil {
		return errResidentRuntimeOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.phase("original_runtime_file_validation_pending")
	var err error
	o.after, err = o.file.Stat()
	if err != nil {
		return err
	}
	if !sameArtifactStat(o.before, o.after) {
		return errResidentRuntimeOriginal
	}
	o.phase("original_runtime_current_name_validation_pending")
	o.named, err = os.Lstat(o.path)
	if err != nil {
		return err
	}
	if !o.named.Mode().IsRegular() || !sameArtifactStat(o.before, o.named) {
		return errResidentRuntimeOriginal
	}
	return nil
}

// Validation returns DATA only, not a transferable executable or process grant.
// Root's eventual image owner must separately bind the immutable pin and P1
// originals; this original named-path snapshot is insufficient for core launch.
func (o *ResidentRuntimePending) ValidateOriginal(ctx context.Context) (proof RuntimeProof, resultErr error) {
	op, err := o.begin("validate")
	if err != nil {
		return proof, err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.validate(ctx)
	if resultErr == nil {
		proof = o.image.proof
	}
	normal = true
	return proof, resultErr
}
func (o *ResidentRuntimePending) InspectOriginal() ResidentRuntimeInspection {
	if o == nil || o.residentRuntimeState == nil || o.self != o {
		return ResidentRuntimeInspection{FirstError: errResidentRuntimeOriginal, NativeOutcome: "unknown"}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.observed
	out.CaptureAttempted, out.Captured = o.captureTried, o.captured
	out.FirstError, out.NativeOutcome = o.firstError, "unknown"
	if o.active != nil {
		out.OperationPending, out.OperationAbnormal = true, o.active.abnormal
		out.OperationKind, out.OperationPhase = o.active.kind, o.active.phase
	}
	return out
}
