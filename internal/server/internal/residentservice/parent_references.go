package residentservice

import (
	"context"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// ServiceParentReferenceOwner is issued once by construction. It can retire
// only this parent's originals, with the control endpoints deliberately last.
// It does not establish child exit, handoff, or released resource capacity.
type ServiceParentReferenceOwner struct {
	self   *ServiceParentReferenceOwner
	parent *ParentPending
}

type serviceParentClose struct {
	file                *retainedFD
	attempted, returned bool
	err                 error
}

type serviceParentRetirement struct {
	owner                            *ServiceParentReferenceOwner
	command                          *exec.Cmd
	service, control                 *operation
	closes                           [5]serviceParentClose
	serviceReturned, controlReturned bool
	serviceError, controlError       error
}

func NewP1ExistingGrantParentWithReferences(home string) (*ParentPending, *ServiceParentReferenceOwner, error) {
	p, err := NewP1ExistingGrantParentPending(home)
	if err != nil {
		return p, nil, err
	}
	r := &ServiceParentReferenceOwner{parent: p}
	r.self = r
	p.owner.parentReferences = r
	return p, r, nil
}

func (r *ServiceParentReferenceOwner) originalOwner() (*owner, error) {
	if r == nil || r.self != r || r.parent == nil || r.parent.owner == nil || r.parent.owner.parent != r.parent || r.parent.owner.parentReferences != r {
		return nil, ErrInvalid
	}
	return r.parent.owner, nil
}

func (r *ServiceParentReferenceOwner) beginOriginal(command *exec.Cmd, control bool) (*serviceParentRetirement, *operation, error) {
	o, err := r.originalOwner()
	if err != nil {
		return nil, nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	p := o.p1
	if o.active != nil || o.firstError != nil || p == nil || p.self != p || p.parent != r.parent || command == nil || command != p.command || command.Process == nil || o.files == nil {
		return nil, nil, ErrInvalid
	}
	c := o.parentRetirement
	if !control {
		if c != nil || !p.parentProtocolPhasesLocked(4) || p.parentProofAuth == nil || !p.completedParentProtocolLocked(&p.parentProofAuth.p1ParentProtocolCall, "p1-parent-proof-auth") {
			return nil, nil, ErrInvalid
		}
		op := &operation{owner: o, kind: "retire-parent-service", phase: "registered_before_parent_reference_disposition"}
		c = &serviceParentRetirement{owner: r, command: command, service: op}
		o.parentRetirement, o.active = c, op // permanently seal every ordinary entry
		return c, op, nil
	}
	if c == nil || c.owner != r || c.command != command || c.service == nil || !c.service.normalFinished || c.service.abnormal || !c.serviceReturned || c.serviceError != nil || c.control != nil {
		return nil, nil, ErrInvalid
	}
	for i := 0; i < 3; i++ {
		if !c.closes[i].attempted || !c.closes[i].returned || c.closes[i].err != nil {
			return nil, nil, ErrInvalid
		}
	}
	op := &operation{owner: o, kind: "retire-parent-control", phase: "registered_before_control_reference_disposition"}
	c.control, o.active = op, op
	return c, op, nil
}

func (r *ServiceParentReferenceOwner) RetireServiceOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, op, err := r.beginOriginal(command, false)
	if err != nil {
		return err
	}
	o := r.parent.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c, false)
	c.serviceError, c.serviceReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *ServiceParentReferenceOwner) RetireControlOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, op, err := r.beginOriginal(command, true)
	if err != nil {
		return err
	}
	o := r.parent.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c, true)
	c.controlError, c.controlReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *ServiceParentReferenceOwner) retireBody(ctx context.Context, c *serviceParentRetirement, control bool) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	n := r.parent.owner.files
	want := [5]*retainedFD{n.home, n.lock, n.journal, n.childChannel, n.parentChannel}
	if !control {
		// All prior temporary closes removed their own slots only on success.
		if n.opened < n.closed || n.opened-n.closed != 5 {
			return ErrInvalid
		}
		seen := [5]bool{}
		for _, f := range n.slots {
			if f == nil {
				continue
			}
			matched := false
			for i, w := range want {
				if f == w {
					if seen[i] {
						return ErrInvalid
					}
					seen[i], matched = true, true
				}
			}
			if !matched {
				return ErrInvalid
			}
		}
		for i, f := range want {
			if !seen[i] || f == nil || f.file == nil || f.closeTried {
				return ErrInvalid
			}
			for j := 0; j < i; j++ {
				if want[j] == f {
					return ErrInvalid
				}
			}
			c.closes[i].file = f
		}
	}
	start, end := 0, 3
	if control {
		start, end = 3, 5
	}
	for i := start; i < end; i++ {
		f := c.closes[i].file
		if f == nil || f != want[i] || f.file == nil || f.closeTried {
			return ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var st unix.Stat_t
		n.progress("parent_reference_native_identity")
		if err := unix.Fstat(f.number, &st); err != nil {
			return err
		}
		if nativeID(&st) != f.id || uint32(st.Mode) != f.mode || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 {
			return ErrInvalid
		}
		if (i == 1 || i == 2) && st.Nlink != 1 {
			return ErrInvalid
		}
		flags, err := unix.FcntlInt(uintptr(f.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
		flags, err = unix.FcntlInt(uintptr(f.number), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		access := unix.O_RDWR
		if i == 0 {
			access = unix.O_RDONLY
		}
		if flags&unix.O_ACCMODE != access || i >= 3 && (uint32(st.Mode)&unix.S_IFMT != unix.S_IFSOCK || flags&unix.O_NONBLOCK == 0) {
			return ErrInvalid
		}
	}
	for i := start; i < end; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		close := &c.closes[i]
		close.attempted = true
		close.err = n.closeAuxiliary(close.file)
		close.returned = true
		if close.err != nil {
			return close.err
		}
	}
	return ctx.Err()
}
