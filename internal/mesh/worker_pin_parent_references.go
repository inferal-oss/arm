package mesh

import (
	"context"
	"os/exec"

	"golang.org/x/sys/unix"
)

// WorkerPinParentReferenceOwner is issued only with a new pin. It owns disposition
// of that pin's original parent references; it is not evidence of handoff or
// child exit. The composed parent must check its own handoff before invoking it.
type WorkerPinParentReferenceOwner struct {
	self *WorkerPinParentReferenceOwner
	pin  *WorkerResidentImagePinPending
}

type workerPinParentClose struct {
	file                *residentPinFile
	attempted, returned bool
	err                 error
}

type workerPinParentRetirement struct {
	owner    *WorkerPinParentReferenceOwner
	op       *residentPinOperation
	command  *exec.Cmd
	closes   [4]workerPinParentClose
	returned bool
	err      error
}

func (r *WorkerPinParentReferenceOwner) beginOriginal(command *exec.Cmd) (*workerPinParentRetirement, error) {
	if r == nil || r.self != r || r.pin == nil || r.pin.self != r.pin || r.pin.inner == nil || r.pin.inner.residentImagePinState == nil || r.pin.inner.self != r.pin.inner {
		return nil, errResidentPinOriginal
	}
	p := r.pin.inner
	// The image owner serializes its native operations independently of the pin.
	b := r.pin.binding
	if b == nil || b.workerImageBindingState == nil || b.self != b {
		return nil, errResidentPinOriginal
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.coreScope != workerResidentImagePinCore || p.p1Binding != nil || p.parentReferences != nil || p.parentRetirement != nil || r.pin.references != r || r.pin.retirement != nil || p.active != nil || p.firstError != nil || !p.ready || !p.expectedHomeChecked || r.pin.validationCount != 2 || b.pin != r.pin || b.command != command || command == nil || command.Process == nil || b.active != nil || b.firstError != nil || b.parentRetired || !b.bound || !b.validated || !b.shortSealTried {
		return nil, errResidentPinOriginal
	}
	op := &residentPinOperation{phase: "parent_pin_retirement_registered_before_effect"}
	c := &workerPinParentRetirement{owner: r, op: op, command: command}
	r.pin.retirement, p.active = c, op
	b.parentRetired = true // permanently seal both original native entry points
	return c, nil
}

func (r *WorkerPinParentReferenceOwner) RetireOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, err := r.beginOriginal(command)
	if err != nil {
		return err
	}
	p := r.pin.inner
	normal := false
	defer p.finish(c.op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c)
	c.err, c.returned = resultErr, true
	normal = true
	return resultErr
}

func (r *WorkerPinParentReferenceOwner) retireBody(ctx context.Context, c *workerPinParentRetirement) error {
	p := r.pin.inner
	if ctx == nil || r.pin.retirement != c || c.owner != r {
		return errResidentPinOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Every historical auxiliary reference must have a known successful close.
	want := [4]*residentPinFile{p.homeFile, p.images, p.directory, p.readonly}
	for i, f := range want {
		if f == nil || f.file == nil || f.closed || f.closeTried {
			return errResidentPinOriginal
		}
		for j := 0; j < i; j++ {
			if want[j] == f {
				return errResidentPinOriginal
			}
		}
		c.closes[i].file = f
	}
	seen := [4]bool{}
	for _, f := range p.files {
		matched := false
		for i, w := range want {
			if f == w {
				if seen[i] {
					return errResidentPinOriginal
				}
				seen[i], matched = true, true
			}
		}
		if !matched && (f == nil || !f.closeTried || !f.closed || f.closeError != nil) {
			return errResidentPinOriginal
		}
	}
	for _, ok := range seen {
		if !ok {
			return errResidentPinOriginal
		}
	}
	if p.writable == nil || !p.writable.closed || p.writable.closeError != nil || p.source == nil {
		return errResidentPinOriginal
	}
	p.source.mu.Lock()
	sourceKnown := p.source.pinSourceClosed && p.source.pinSourceCloseTried && p.source.pinSourceCloseError == nil
	p.source.mu.Unlock()
	if !sourceKnown {
		return errResidentPinOriginal
	}
	// Validate held originals only. Never reopen paths or hash growing child data.
	for i, f := range want {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := f.before.Mode().Perm()
		if err := p.private(f, i != 3, mode); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(f.fd), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return errResidentPinOriginal
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err = unix.FcntlInt(uintptr(f.fd), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		if flags&unix.O_ACCMODE != unix.O_RDONLY {
			return errResidentPinOriginal
		}
	}
	for i := range c.closes {
		if err := ctx.Err(); err != nil {
			return err
		}
		close := &c.closes[i]
		close.attempted = true // retained before this exact original Close
		close.err = p.closeReference(close.file)
		close.returned = true
		if close.err != nil {
			return close.err
		}
	}
	return ctx.Err()
}
