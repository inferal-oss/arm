package residentservice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"getarm.org/internal/residentrefs"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
)

// ShortSealOriginal checks the actual group's native originals after all long
// validation. No paths are reopened, journal reread, wrappers created or FD lent.
func (p *P1ServiceGroup) ShortSealOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || command != p.command {
		return ErrInvalid
	}
	op, err := p.begin("p1-short-seal")
	if err != nil {
		return err
	}
	o := p.parent.owner
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = p.shortSealBody(ctx)
	normal = true
	return resultErr
}
func (p *P1ServiceGroup) shortSealBody(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.commandSelectors(true); err != nil {
		return err
	}
	if p.command.Stdout != nil || p.command.Stderr != nil {
		return ErrInvalid
	}
	o := p.parent.owner
	for _, e := range []*retainedFD{o.files.childChannel, o.files.home, o.files.lock, o.files.journal, o.files.parentChannel} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e == nil || e.file == nil || e.closeTried {
			return ErrInvalid
		}
		o.files.progress("original_P1_short_native_stat")
		var st unix.Stat_t
		if err := unix.Fstat(e.number, &st); err != nil {
			return err
		}
		if nativeID(&st) != e.id || uint32(st.Mode) != e.mode || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 {
			return ErrInvalid
		}
		if e == o.files.lock || e == o.files.journal {
			if uint32(st.Mode) != uint32(unix.S_IFREG)|0600 || st.Nlink != 1 {
				return ErrInvalid
			}
		}
		if e == o.files.home && uint32(st.Mode) != uint32(unix.S_IFDIR)|0700 {
			return ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(e.number), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return ErrInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err = unix.FcntlInt(uintptr(e.number), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		want := unix.O_RDWR
		if e == o.files.home {
			want = unix.O_RDONLY
		}
		if flags&unix.O_ACCMODE != want {
			return ErrInvalid
		}
		if (e == o.files.childChannel || e == o.files.parentChannel) && (uint32(st.Mode)&unix.S_IFMT != unix.S_IFSOCK || flags&unix.O_NONBLOCK == 0) {
			return ErrInvalid
		}
	}
	if err := p.checkChannel(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// Data transfer on the group's actual parent endpoint only. This does not
// observe/assert Cmd.Start, ACK, Ready or closure. Only the composed root's own
// retained Start call may decide to invoke it. Both writers must already have
// been withdrawn by that same root. It accepts a bounded canonical FULL frame,
// never silently adds another header or resends a partial outcome.
func (p *P1ServiceGroup) SendOriginalP1Frame(ctx context.Context, command *exec.Cmd, frame []byte) (resultErr error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || command != p.command || ctx == nil {
		return ErrInvalid
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrInvalid
	}
	if err := validateP1ParentFrameData(frame); err != nil {
		return err
	}
	op, err := p.begin("p1-transfer")
	if err != nil {
		return err
	}
	o := p.parent.owner
	call, err := p.registerParentControlCall(op, parentBootstrap, nil)
	if err != nil {
		normal := true
		o.finish(op, &normal, &err)
		return err
	}
	normal := false
	defer p.publishParentControlResult(call, &normal, &resultErr)
	defer o.finish(op, &normal, &resultErr)
	bootstrap := &channelOperation{owner: o}
	o.mu.Lock()
	o.channel = bootstrap
	call.bootstrap = bootstrap
	o.mu.Unlock()
	resultErr = p.writeParentControl(ctx, call, frame)
	o.mu.Lock()
	bootstrap.writeComplete = call.bytesComplete
	o.writerSent = projectP1TransferData(bootstrap).FullWrite
	o.mu.Unlock()
	if resultErr != nil {
		o.channelFailure(bootstrap, resultErr)
	}
	normal = true
	return resultErr
}

// Pure full-frame parser. Its result is never a birth/ACK/native authority.
func validateP1ParentFrameData(frame []byte) error {
	if len(frame) <= 36 || len(frame) > residentrefs.MaxWireBytes || int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-36 {
		return ErrInvalid
	}
	sum := sha256.Sum256(frame[36:])
	if !bytes.Equal(sum[:], frame[4:36]) {
		return ErrInvalid
	}
	return nil
}
