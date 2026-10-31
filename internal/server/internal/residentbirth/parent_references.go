package residentbirth

import (
	"context"
	"os/exec"

	"getarm.org/internal/mesh"
	"getarm.org/internal/server/internal/residentservice"
	"golang.org/x/sys/unix"
)

// FenceParentReferenceOwner owns only the original parent's six fence refs.
// Construction issues it once; no borrowed fence can acquire another closer.
type FenceParentReferenceOwner struct {
	self  *FenceParentReferenceOwner
	fence *FreshFence
}

type fenceParentClose struct {
	file                *fenceFD
	attempted, returned bool
	err                 error
}

type fenceParentRetirement struct {
	owner    *FenceParentReferenceOwner
	op       *fenceOperation
	command  *exec.Cmd
	closes   [6]fenceParentClose
	returned bool
	err      error
}

func NewFreshFenceWithParentReferences(group *residentservice.P1ServiceGroup, pin *mesh.ResidentImagePinPending, command *exec.Cmd) (*FreshFence, *FenceParentReferenceOwner, error) {
	f, err := NewFreshFencePending(group, pin, command)
	if err != nil {
		return f, nil, err
	}
	r := &FenceParentReferenceOwner{fence: f}
	r.self = r
	f.owner.parentReferences = r
	return f, r, nil
}

func (r *FenceParentReferenceOwner) beginOriginal(ctx context.Context, command *exec.Cmd) (*fenceParentRetirement, error) {
	if r == nil || r.self != r || r.fence == nil || r.fence.owner == nil || r.fence.owner.self != r.fence {
		return nil, errOriginalFence
	}
	o := r.fence.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.parentReferences != r || o.parentRetirement != nil || o.active != nil || o.firstError != nil || command == nil || command != o.command || command.Process == nil || !o.bound || !o.writerWithdrawn || !o.intentCommitted || o.postStart == nil || !o.postStartTried || !o.completedPostStartLocked(o.postStart, o.prepared, command, o.postStart.service) {
		return nil, errOriginalFence
	}
	op := &fenceOperation{owner: o, ctx: ctx, kind: "retire-parent-fence", phase: "registered_before_parent_reference_disposition"}
	c := &fenceParentRetirement{owner: r, op: op, command: command}
	o.parentRetirement, o.active = c, op
	return c, nil
}

func (r *FenceParentReferenceOwner) RetireOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	c, err := r.beginOriginal(ctx, command)
	if err != nil {
		return err
	}
	o := r.fence.owner
	normal := false
	defer o.finish(c.op, &normal, &resultErr)
	resultErr = r.retireBody(c)
	c.err, c.returned = resultErr, true
	normal = true
	return resultErr
}

func (r *FenceParentReferenceOwner) retireBody(c *fenceParentRetirement) error {
	o := r.fence.owner
	n := o.state
	if n == nil || n.owner != o || c.owner != r || o.parentRetirement != c {
		return errOriginalFence
	}
	want := [6]*fenceFD{n.home, n.launch, n.daemon, n.directory, n.ledger, n.journal}
	seen := [6]bool{}
	for i, f := range want {
		if f == nil || f.file == nil || f.closed || f.closeTried {
			return errOriginalFence
		}
		for j := 0; j < i; j++ {
			if want[j] == f {
				return errOriginalFence
			}
		}
		c.closes[i].file = f
	}
	for _, f := range n.history {
		matched := false
		for i, w := range want {
			if f == w {
				if seen[i] {
					return errOriginalFence
				}
				seen[i], matched = true, true
			}
		}
		if !matched && (f == nil || !f.closeTried || !f.closed || f.closeError != nil) {
			return errOriginalFence
		}
	}
	for _, ok := range seen {
		if !ok {
			return errOriginalFence
		}
	}
	if n.opened < n.closed || n.opened-n.closed != 6 {
		return errOriginalFence
	}
	live := 0
	slotSeen := [6]bool{}
	for _, f := range n.slots {
		if f == nil {
			continue
		}
		matched := false
		for i, w := range want {
			if f == w {
				if slotSeen[i] {
					return errOriginalFence
				}
				slotSeen[i], matched = true, true
			}
		}
		if !matched {
			return errOriginalFence
		}
		live++
	}
	if live != 6 {
		return errOriginalFence
	}
	for i, f := range want {
		directory := i == 0 || i == 3
		if err := n.check(f, directory); err != nil {
			return err
		}
		if err := n.before("parent_fence_access_flags"); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(f.number), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		access := unix.O_RDWR
		if directory {
			access = unix.O_RDONLY
		}
		if flags&unix.O_ACCMODE != access {
			return errOriginalFence
		}
	}
	// The child's SQL ledger and RootBound footer may have grown. Their size is
	// deliberately not compared with the parent's pre-Start observations.
	for i := range c.closes {
		if err := n.before("parent_fence_reference_close"); err != nil {
			return err
		}
		close := &c.closes[i]
		close.attempted = true
		close.err = n.closeAux(close.file)
		close.returned = true
		if close.err != nil {
			return close.err
		}
	}
	return c.op.ctx.Err()
}
