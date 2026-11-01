package mesh

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
)

// ResidentWorkerCoreRole is the same binary's fixed private entry, not a user role.
// It is distinct from the legacy role and selected before native materialization.
const ResidentWorkerCoreRole = "--arm-resident-worker-core-v1"

// This concrete nonclosing binding lends no image/File/Start/Close to a worker.
// The composed root alone retains the supplied actual command and validates the
// actual service debit and ALL13 groups before its ONE native Start. This leaf
// binds only the three image originals; it issues no funding/process authority.
type WorkerImageBindingPending struct{ *workerImageBindingState }
type workerImageBindingState struct {
	extraFiles                []*os.File
	boundFiles                [13]*os.File
	mu                        sync.Mutex
	self                      *WorkerImageBindingPending
	pin                       *WorkerResidentImagePinPending
	command                   *exec.Cmd
	active                    *workerImageBindingOperation
	tried, bound              bool
	validated, shortSealTried bool
	firstError                error
	parentRetired             bool
	proof                     RuntimeProof
}
type workerImageBindingOperation struct {
	kind     string
	abnormal bool
	cause    any
}
type WorkerImageBindingInspection struct {
	BindAttempted, Bound, OperationPending, OperationAbnormal bool
	FirstError                                                error
	NativeOutcome                                             string
}

var errWorkerImageBindingOriginal = errors.New("original worker image binding required; retain original custody")

// Pure allocation/identity registration only, before any validation/native I/O.
// The caller must retain this and the actual pin/command under real root custody.
func NewWorkerImageBindingPending(pin *WorkerResidentImagePinPending, command *exec.Cmd) (*WorkerImageBindingPending, error) {
	if command == nil || len(command.ExtraFiles) != 13 {
		return nil, errWorkerImageBindingOriginal
	}
	inner, err := pin.originalInnerData()
	if err != nil {
		return nil, err
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.coreScope != workerResidentImagePinCore || inner.p1Binding != nil || inner.parentReferences != nil || pin.binding != nil {
		return nil, errWorkerImageBindingOriginal
	}
	p := &WorkerImageBindingPending{&workerImageBindingState{pin: pin, command: command, extraFiles: command.ExtraFiles}}
	p.self = p
	pin.binding = p
	return p, nil
}
func (p *WorkerImageBindingPending) begin(kind string) (*workerImageBindingOperation, error) {
	if p == nil || p.workerImageBindingState == nil || p.self != p {
		return nil, errWorkerImageBindingOriginal
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.parentRetired {
		return nil, errWorkerImageBindingOriginal
	}
	if p.firstError != nil {
		return nil, p.firstError
	}
	if p.active != nil {
		return nil, errWorkerImageBindingOriginal
	}
	if kind == "bind" {
		if p.tried {
			return nil, errWorkerImageBindingOriginal
		}
		p.tried = true
	} else if kind == "short-seal" {
		if !p.bound || !p.validated || p.shortSealTried {
			return nil, errWorkerImageBindingOriginal
		}
		p.shortSealTried = true
	} else if kind != "validate" || !p.bound {
		return nil, errWorkerImageBindingOriginal
	}
	op := &workerImageBindingOperation{kind: kind}
	p.active = op
	return op, nil
}
func (p *WorkerImageBindingPending) finish(op *workerImageBindingOperation, normal *bool, result *error) {
	cause := recover()
	p.mu.Lock()
	if p.active != op {
		p.mu.Unlock()
		panic("original worker image operation identity lost")
	}
	if !*normal {
		op.abnormal = true
		op.cause = cause
		if p.firstError == nil {
			p.firstError = &residentRuntimeAbnormal{cause: cause}
		}
	} else {
		if *result != nil && p.firstError == nil {
			p.firstError = *result
		}
		if *result == nil && op.kind == "validate" {
			p.validated = true
		}
		if *result == nil && op.kind == "bind" {
			p.bound = true
		}
		p.active = nil
	}
	p.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (p *WorkerImageBindingPending) BindOriginal(ctx context.Context) (resultErr error) {
	op, err := p.begin("bind")
	if err != nil {
		return err
	}
	normal := false
	defer p.finish(op, &normal, &resultErr)
	resultErr = p.validate(ctx, false)
	if resultErr == nil {
		// No duplicate wrapper, reopen or borrower-owned descriptor is created.
		p.command.ExtraFiles[6] = p.pin.inner.readonly.file
		p.command.ExtraFiles[7] = p.pin.inner.images.file
		p.command.ExtraFiles[8] = p.pin.inner.directory.file
		copy(p.boundFiles[:], p.command.ExtraFiles)
	}
	normal = true
	return resultErr
}

// Proof is DATA only; current validation does NOT observe Cmd.Start or grant
// native closure. Composed root must retain authority through actual Start.
func (p *WorkerImageBindingPending) ValidateOriginal(ctx context.Context, command *exec.Cmd) (proof RuntimeProof, resultErr error) {
	if p == nil || p.workerImageBindingState == nil || p.self != p || command != p.command {
		return proof, errWorkerImageBindingOriginal
	}
	op, err := p.begin("validate")
	if err != nil {
		return proof, err
	}
	normal := false
	defer p.finish(op, &normal, &resultErr)
	resultErr = p.validate(ctx, true)
	if resultErr == nil {
		proof = p.proof
	}
	normal = true
	return proof, resultErr
}
func (p *WorkerImageBindingPending) validate(ctx context.Context, bound bool) error {
	if ctx == nil {
		return errWorkerImageBindingOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pin, err := p.pin.originalInnerData()
	if err != nil {
		return errWorkerImageBindingOriginal
	}
	pin.mu.Lock()
	if p.pin.binding != p || pin.coreScope != workerResidentImagePinCore || pin.p1Binding != nil || !pin.ready || pin.firstError != nil || pin.active != nil || p.pin.validationCount >= 2 {
		pin.mu.Unlock()
		return errWorkerImageBindingOriginal
	}
	p.pin.validationCount++
	op := &residentPinOperation{phase: "original_worker_image_validation_registered_before_effect"}
	pin.active = op
	pin.mu.Unlock()
	normal := false
	var resultErr error
	defer pin.finish(op, &normal, &resultErr)
	resultErr = p.validateBody(ctx, bound)
	normal = true
	return resultErr
}
func (p *WorkerImageBindingPending) validateBody(ctx context.Context, bound bool) error {
	pin := p.pin.inner
	pin.source.mu.Lock()
	sourceClosed := pin.source.pinSourceClosed && pin.source.pinSourceCloseTried && pin.source.pinSourceCloseError == nil
	pin.source.mu.Unlock()
	if !sourceClosed || pin.writable == nil || !pin.writable.closed || pin.readonly == nil || pin.readonly.closed || pin.readonly.closeTried {
		return errWorkerImageBindingOriginal
	}
	command := p.command
	if command == nil || command.Process != nil || command.ProcessState != nil || command.Cancel != nil || command.WaitDelay != 0 || command.Dir != "/" || command.Stdin != nil || !reflect.DeepEqual(command.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) {
		return errWorkerImageBindingOriginal
	}
	if command.Path != pin.path || len(command.Args) != 2 || command.Args[0] != pin.path || command.Args[1] != ResidentWorkerCoreRole || len(command.ExtraFiles) != 13 {
		return errWorkerImageBindingOriginal
	}
	if len(p.extraFiles) != 13 || &command.ExtraFiles[0] != &p.extraFiles[0] {
		return errWorkerImageBindingOriginal
	}
	if bound {
		for i, file := range command.ExtraFiles {
			if file != p.boundFiles[i] {
				return errWorkerImageBindingOriginal
			}
		}
	}
	want := []*os.File{pin.readonly.file, pin.images.file, pin.directory.file}
	for i, file := range command.ExtraFiles {
		if i >= 6 && i <= 8 {
			if bound && file != want[i-6] || !bound && file != nil {
				return errWorkerImageBindingOriginal
			}
		} else if file == nil {
			return errWorkerImageBindingOriginal
		}
	}
	for _, writer := range []any{command.Stdout, command.Stderr} {
		if writer != nil {
			if _, ok := writer.(*os.File); !ok {
				return errWorkerImageBindingOriginal
			}
		}
	}
	// Each of at most TWO validations retains its own traversal results. No
	// unbounded repeated directory-open/close record accumulation is possible.
	pin.verified = 0
	pin.publish()
	if err := pin.verifyReadonly(ctx); err != nil {
		return err
	}
	if err := pin.validateCurrentNames(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.proof = pin.source.image.proof
	p.proof.Method = "retained-private-readonly-pin"
	return nil
}
func (p *WorkerImageBindingPending) InspectOriginal() WorkerImageBindingInspection {
	if p == nil || p.workerImageBindingState == nil || p.self != p {
		return WorkerImageBindingInspection{FirstError: errWorkerImageBindingOriginal, NativeOutcome: "unknown"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v := WorkerImageBindingInspection{BindAttempted: p.tried, Bound: p.bound, FirstError: p.firstError, NativeOutcome: "unknown"}
	if p.active != nil {
		v.OperationPending = true
		v.OperationAbnormal = p.active.abnormal
	}
	return v
}
