package residentservice

import (
	"context"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// WorkerServiceParentReferenceOwner is issued once by construction. It can retire
// only this parent's originals, with the control endpoints deliberately last.
// It does not establish child exit, handoff, or released resource capacity.
type WorkerServiceParentReferenceOwner struct {
	self  *WorkerServiceParentReferenceOwner
	group *WorkerServiceGroup
}

type workerServiceParentClose struct {
	file                *retainedFD
	attempted, returned bool
	err                 error
}

type workerServiceParentRetirement struct {
	owner                                   *WorkerServiceParentReferenceOwner
	command                                 *exec.Cmd
	service, control                        *operation
	closes                                  [5]workerServiceParentClose
	serviceReturned, controlReturned        bool
	serviceError, controlError              error
	image                                   *workerRuntimeImage
	imageCloseAttempted, imageCloseReturned bool
	imageCloseError                         error
}

func (r *WorkerServiceParentReferenceOwner) originalOwner() (*workerCustody, error) {
	if r == nil || r.self != r || r.group == nil || r.group.self != r.group || r.group.references != r || r.group.parent == nil || r.group.parent.self != r.group.parent || r.group.parent.group != r.group || r.group.parent.custody == nil || r.group.parent.custody.raw == nil {
		return nil, ErrInvalid
	}
	return r.group.parent.custody, nil
}

func (r *WorkerServiceParentReferenceOwner) beginOriginal(command *exec.Cmd, control bool) (*workerServiceParentRetirement, *operation, error) {
	w, err := r.originalOwner()
	if err != nil {
		return nil, nil, err
	}
	o := w.raw
	o.mu.Lock()
	defer o.mu.Unlock()
	p := r.group
	if o.active != nil || o.firstError != nil || !w.originalWorkerDebitLocked(p.debit) || command == nil || command != p.command || command.Process == nil || o.files == nil {
		return nil, nil, ErrInvalid
	}
	c := p.retirement
	if !control {
		if c != nil || !p.proofCompletedLocked() {
			return nil, nil, ErrInvalid
		}
		op := &operation{owner: o, kind: "worker-parent-retire-service", phase: "registered_before_parent_reference_disposition"}
		c = &workerServiceParentRetirement{owner: r, command: command, service: op}
		p.retirement, o.active = c, op // permanently seal every ordinary entry
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
	if !c.imageCloseAttempted || !c.imageCloseReturned || c.imageCloseError != nil {
		return nil, nil, ErrInvalid
	}
	op := &operation{owner: o, kind: "worker-parent-retire-control", phase: "registered_before_control_reference_disposition"}
	c.control, o.active = op, op
	return c, op, nil
}

func (r *WorkerServiceParentReferenceOwner) RetireServiceOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, op, err := r.beginOriginal(command, false)
	if err != nil {
		return err
	}
	o := r.group.parent.custody.raw
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c, false)
	c.serviceError, c.serviceReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *WorkerServiceParentReferenceOwner) RetireControlOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, op, err := r.beginOriginal(command, true)
	if err != nil {
		return err
	}
	o := r.group.parent.custody.raw
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = r.retireBody(ctx, c, true)
	c.controlError, c.controlReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *WorkerServiceParentReferenceOwner) retireBody(ctx context.Context, c *workerServiceParentRetirement, control bool) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	n := r.group.parent.custody.raw.files
	want := [5]*retainedFD{n.home, n.lock, n.journal, n.childChannel, n.parentChannel}
	if !control {
		image := r.group.parent.custody.image
		if image == nil || image.self != image || image.owner != r.group.parent.custody || !image.complete || image.firstError != nil || !image.rawRecorded || image.file == nil || image.rawFD < 0 || int(image.file.Fd()) != image.rawFD || image.captureOp == nil || !image.captureOp.normalFinished || image.captureOp.abnormal {
			return ErrInvalid
		}
		for _, proof := range image.proofCalls {
			if proof != nil && (proof.owner != image || !proof.complete || proof.firstError != nil || proof.op == nil || !proof.op.normalFinished || proof.op.abnormal) {
				return ErrInvalid
			}
		}
		c.image = image
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
	if !control {
		if err := ctx.Err(); err != nil {
			return err
		}
		n.progress("worker_parent_runtime_image_reference_identity_pending")
		info, err := c.image.file.Stat()
		if err != nil {
			return err
		}
		if !sameWorkerImageStatData(c.image.before, info) {
			return ErrInvalid
		}
		flags, err := unix.FcntlInt(uintptr(c.image.rawFD), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
		flags, err = unix.FcntlInt(uintptr(c.image.rawFD), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		if flags&unix.O_ACCMODE != unix.O_RDONLY {
			return ErrInvalid
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
	if !control {
		if err := ctx.Err(); err != nil {
			return err
		}
		n.progress("worker_parent_runtime_image_reference_close_pending")
		c.imageCloseAttempted = true
		c.imageCloseError = c.image.file.Close()
		c.imageCloseReturned = true
		if c.imageCloseError != nil {
			return c.imageCloseError
		}
	}
	return ctx.Err()
}
