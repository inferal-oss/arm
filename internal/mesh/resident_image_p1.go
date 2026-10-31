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

// ResidentP1CoreRole is the same binary's fixed private entry, not a user role.
// Integration must alias the old service constant and replace its old4-FD path.
const ResidentP1CoreRole = "--arm-resident-core-v1"

// This concrete nonclosing binding lends no image/File/Start/Close to a worker.
// The composed root alone retains the supplied actual command and validates the
// actual service debit and ALL13 groups before its ONE native Start. This leaf
// binds only the three image originals; it issues no funding/process authority.
type ResidentImageP1Pending struct{ *residentImageP1State }
type residentImageP1State struct {
	extraFiles   []*os.File
	boundFiles   [13]*os.File
	mu           sync.Mutex
	self         *ResidentImageP1Pending
	pin          *ResidentImagePinPending
	command      *exec.Cmd
	active       *residentImageP1Operation
	tried, bound bool
	firstError   error
	proof        RuntimeProof
}
type residentImageP1Operation struct {
	kind     string
	abnormal bool
	cause    any
}
type ResidentImageP1Inspection struct {
	BindAttempted, Bound, OperationPending, OperationAbnormal bool
	FirstError                                                error
	NativeOutcome                                             string
}

var errResidentImageP1Original = errors.New("original resident P1 image binding required; retain original custody")

// Pure allocation/identity registration only, before any validation/native I/O.
// The caller must retain this and the actual pin/command under real root custody.
func NewResidentImageP1Pending(pin *ResidentImagePinPending, command *exec.Cmd) (*ResidentImageP1Pending, error) {
	if pin == nil || pin.residentImagePinState == nil || pin.self != pin || command == nil || len(command.ExtraFiles) != 13 {
		return nil, errResidentImageP1Original
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.p1Binding != nil {
		return nil, errResidentImageP1Original
	}
	p := &ResidentImageP1Pending{&residentImageP1State{pin: pin, command: command, extraFiles: command.ExtraFiles}}
	p.self = p
	pin.p1Binding = p
	return p, nil
}
func (p *ResidentImageP1Pending) begin(kind string) (*residentImageP1Operation, error) {
	if p == nil || p.residentImageP1State == nil || p.self != p {
		return nil, errResidentImageP1Original
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.firstError != nil {
		return nil, p.firstError
	}
	if p.active != nil {
		return nil, errResidentImageP1Original
	}
	if kind == "bind" {
		if p.tried {
			return nil, errResidentImageP1Original
		}
		p.tried = true
	} else if kind != "validate" || !p.bound {
		return nil, errResidentImageP1Original
	}
	op := &residentImageP1Operation{kind: kind}
	p.active = op
	return op, nil
}
func (p *ResidentImageP1Pending) finish(op *residentImageP1Operation, normal *bool, result *error) {
	cause := recover()
	p.mu.Lock()
	if p.active != op {
		p.mu.Unlock()
		panic("original P1 image operation identity lost")
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
func (p *ResidentImageP1Pending) BindOriginal(ctx context.Context) (resultErr error) {
	op, err := p.begin("bind")
	if err != nil {
		return err
	}
	normal := false
	defer p.finish(op, &normal, &resultErr)
	resultErr = p.validate(ctx, false)
	if resultErr == nil {
		// No duplicate wrapper, reopen or borrower-owned descriptor is created.
		p.command.ExtraFiles[6] = p.pin.readonly.file
		p.command.ExtraFiles[7] = p.pin.images.file
		p.command.ExtraFiles[8] = p.pin.directory.file
		copy(p.boundFiles[:], p.command.ExtraFiles)
	}
	normal = true
	return resultErr
}

// Proof is DATA only; current validation does NOT observe Cmd.Start or grant
// native closure. Composed root must retain authority through actual Start.
func (p *ResidentImageP1Pending) ValidateOriginal(ctx context.Context, command *exec.Cmd) (proof RuntimeProof, resultErr error) {
	if p == nil || p.residentImageP1State == nil || p.self != p || command != p.command {
		return proof, errResidentImageP1Original
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
func (p *ResidentImageP1Pending) validate(ctx context.Context, bound bool) error {
	if ctx == nil {
		return errResidentImageP1Original
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pin := p.pin
	if pin == nil || pin.self != pin {
		return errResidentImageP1Original
	}
	pin.mu.Lock()
	if pin.p1Binding != p || !pin.ready || pin.firstError != nil || pin.active != nil || pin.p1ValidationCount >= 2 {
		pin.mu.Unlock()
		return errResidentImageP1Original
	}
	pin.p1ValidationCount++
	op := &residentPinOperation{phase: "original_P1_image_validation_registered_before_effect"}
	pin.active = op
	pin.mu.Unlock()
	normal := false
	var resultErr error
	defer pin.finish(op, &normal, &resultErr)
	resultErr = p.validateBody(ctx, bound)
	normal = true
	return resultErr
}
func (p *ResidentImageP1Pending) validateBody(ctx context.Context, bound bool) error {
	pin := p.pin
	pin.source.mu.Lock()
	sourceClosed := pin.source.pinSourceClosed && pin.source.pinSourceCloseTried && pin.source.pinSourceCloseError == nil
	pin.source.mu.Unlock()
	if !sourceClosed || pin.writable == nil || !pin.writable.closed || pin.readonly == nil || pin.readonly.closed || pin.readonly.closeTried {
		return errResidentImageP1Original
	}
	command := p.command
	if command == nil || command.Process != nil || command.ProcessState != nil || command.Cancel != nil || command.WaitDelay != 0 || command.Dir != "/" || command.Stdin != nil || !reflect.DeepEqual(command.SysProcAttr, &syscall.SysProcAttr{Setsid: true}) {
		return errResidentImageP1Original
	}
	if command.Path != pin.path || len(command.Args) != 2 || command.Args[0] != pin.path || command.Args[1] != ResidentP1CoreRole || len(command.ExtraFiles) != 13 {
		return errResidentImageP1Original
	}
	if len(p.extraFiles) != 13 || &command.ExtraFiles[0] != &p.extraFiles[0] {
		return errResidentImageP1Original
	}
	if bound {
		for i, file := range command.ExtraFiles {
			if file != p.boundFiles[i] {
				return errResidentImageP1Original
			}
		}
	}
	want := []*os.File{pin.readonly.file, pin.images.file, pin.directory.file}
	for i, file := range command.ExtraFiles {
		if i >= 6 && i <= 8 {
			if bound && file != want[i-6] || !bound && file != nil {
				return errResidentImageP1Original
			}
		} else if file == nil {
			return errResidentImageP1Original
		}
	}
	for _, writer := range []any{command.Stdout, command.Stderr} {
		if writer != nil {
			if _, ok := writer.(*os.File); !ok {
				return errResidentImageP1Original
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
func (p *ResidentImageP1Pending) InspectOriginal() ResidentImageP1Inspection {
	if p == nil || p.residentImageP1State == nil || p.self != p {
		return ResidentImageP1Inspection{FirstError: errResidentImageP1Original, NativeOutcome: "unknown"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	v := ResidentImageP1Inspection{BindAttempted: p.tried, Bound: p.bound, FirstError: p.firstError, NativeOutcome: "unknown"}
	if p.active != nil {
		v.OperationPending = true
		v.OperationAbnormal = p.active.abnormal
	}
	return v
}
