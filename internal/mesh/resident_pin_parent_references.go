package mesh

import (
	"context"
	"os/exec"

	"golang.org/x/sys/unix"
)

// PinParentReferenceOwner is issued only with a new pin. It owns disposition
// of that pin's original parent references; it is not evidence of handoff or
// child exit. The composed parent must check its own handoff before invoking it.
type PinParentReferenceOwner struct {
	self *PinParentReferenceOwner
	pin  *ResidentImagePinPending
}

type pinParentClose struct {
	file                *residentPinFile
	attempted, returned bool
	err                 error
}

type pinParentRetirement struct {
	owner    *PinParentReferenceOwner
	op       *residentPinOperation
	command  *exec.Cmd
	closes   [4]pinParentClose
	returned bool
	err      error
}

// No later getter can issue this capability for a borrowed or existing pin.
func NewResidentImagePinForHomeWithParentReferences(home, debit, sha string, size int64, expected ResidentImagePinHomeData) (*ResidentImagePinPending, *PinParentReferenceOwner, error) {
	p, err := NewResidentImagePinForHomeDataPending(home, debit, sha, size, expected)
	if err != nil {
		return p, nil, err
	}
	r := &PinParentReferenceOwner{pin: p}
	r.self = r
	p.parentReferences = r
	return p, r, nil
}

func (r *PinParentReferenceOwner) beginOriginal(command *exec.Cmd) (*pinParentRetirement, error) {
	if r == nil || r.self != r || r.pin == nil || r.pin.residentImagePinState == nil || r.pin.self != r.pin {
		return nil, errResidentPinOriginal
	}
	p := r.pin
	// The image owner serializes its native operations independently of the pin.
	b := p.p1Binding
	if b == nil || b.residentImageP1State == nil || b.self != b {
		return nil, errResidentPinOriginal
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.parentReferences != r || p.parentRetirement != nil || p.active != nil || p.firstError != nil || !p.ready || !p.expectedHomeChecked || p.p1ValidationCount != 2 || b.pin != p || b.command != command || command == nil || command.Process == nil || b.active != nil || b.firstError != nil || b.parentRetired || !b.bound || !b.validated || !b.shortSealTried {
		return nil, errResidentPinOriginal
	}
	op := &residentPinOperation{phase: "parent_pin_retirement_registered_before_effect"}
	c := &pinParentRetirement{owner: r, op: op, command: command}
	p.parentRetirement, p.active = c, op
	b.parentRetired = true // permanently seal both original native entry points
	return c, nil
}

func (r *PinParentReferenceOwner) RetireOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, err := r.beginOriginal(command)
	if err != nil {
		return err
	}
	p := r.pin
	normal := false
	defer p.finish(c.op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c)
	c.err, c.returned = resultErr, true
	normal = true
	return resultErr
}

func (r *PinParentReferenceOwner) retireBody(ctx context.Context, c *pinParentRetirement) error {
	p := r.pin
	if ctx == nil || p.parentRetirement != c || c.owner != r {
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
