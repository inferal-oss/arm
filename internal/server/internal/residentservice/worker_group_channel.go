package residentservice

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func (p *WorkerServiceGroup) PrepareOriginalChannel(ctx context.Context) (resultErr error) {
	op, err := p.beginOriginal("worker-group-channel")
	if err != nil {
		return err
	}
	w := p.parent.custody
	normal := false
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.prepareChannelBodyOriginal(ctx)
	if resultErr == nil {
		w.raw.mu.Lock()
		p.channelReady = true
		w.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) prepareChannelBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	n := p.parent.custody.raw.files
	if err := n.capacity(2); err != nil {
		return err
	}
	// No legacy owner/group fields are installed to select this path. Both raw
	// originals are retained before either CLOEXEC/nonblock or wrapper can fail.
	n.progress("worker_original_socketpair_pending")
	err := func() error {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		if err != nil {
			return err
		}
		n.parentChannel = n.registerRaw(pair[0], "worker-original-parent-channel")
		n.childChannel = n.registerRaw(pair[1], "worker-original-child-channel")
		for _, fd := range pair {
			if err := ctx.Err(); err != nil {
				return err
			}
			n.progress("worker_original_socketpair_CLOEXEC_pending")
			if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := unix.SetNonblock(fd, true); err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if err = n.materialize(n.parentChannel); err != nil {
		return err
	}
	return n.materialize(n.childChannel)
}

// The service and launch writers must both be withdrawn by the actual parent
// root. This checks held originals only; it does not observe native Cmd.Start.
func (p *WorkerServiceGroup) ShortSealOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.self != p || command != p.command {
		return ErrInvalid
	}
	op, err := p.beginOriginal("worker-group-short-seal")
	if err != nil {
		return err
	}
	normal := false
	defer p.parent.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.shortSealBodyOriginal(ctx)
	if resultErr == nil {
		p.parent.custody.raw.mu.Lock()
		p.shortSealed = true
		p.parent.custody.raw.mu.Unlock()
	}
	normal = true
	return resultErr
}

func (p *WorkerServiceGroup) shortSealBodyOriginal(ctx context.Context) error {
	if err := workerContextData(ctx); err != nil {
		return err
	}
	if err := p.commandSelectorsOriginal(true); err != nil {
		return err
	}
	if p.command.Stdout != nil || p.command.Stderr != nil {
		return ErrInvalid
	}
	n := p.parent.custody.raw.files
	for _, entry := range []*retainedFD{n.childChannel, n.home, n.lock, n.journal, n.parentChannel} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry == nil || entry.file == nil || entry.closeTried {
			return ErrInvalid
		}
		n.progress("worker_original_short_native_stat_pending")
		var st unix.Stat_t
		if err := unix.Fstat(entry.number, &st); err != nil {
			return err
		}
		if nativeID(&st) != entry.id || uint32(st.Mode) != entry.mode || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 {
			return ErrInvalid
		}
		if entry == n.lock || entry == n.journal {
			if uint32(st.Mode) != uint32(unix.S_IFREG)|0600 || st.Nlink != 1 {
				return ErrInvalid
			}
		}
		if entry == n.home && uint32(st.Mode) != uint32(unix.S_IFDIR)|0700 {
			return ErrInvalid
		}
		flags, err := unix.FcntlInt(uintptr(entry.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
		flags, err = unix.FcntlInt(uintptr(entry.number), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		want := unix.O_RDWR
		if entry == n.home {
			want = unix.O_RDONLY
		}
		if flags&unix.O_ACCMODE != want {
			return ErrInvalid
		}
		if (entry == n.parentChannel || entry == n.childChannel) && flags&unix.O_NONBLOCK == 0 {
			return ErrInvalid
		}
	}
	return p.channelBodyOriginal(ctx)
}
