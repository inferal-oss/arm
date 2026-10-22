package sshservice

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Proxy is the fixed stdio helper used ONLY with a private per-invocation
// socket. It does not resolve nodes, accept a host/port, open a mesh stream,
// retry, or grant SSH authentication. The caller owns input and may close it.
func Proxy(ctx context.Context, path string, input io.ReadCloser, output io.Writer) error {
	if !safePath(path) || len(path) > 100 || input == nil || output == nil {
		return fmt.Errorf("bounded private SSH proxy socket required")
	}
	dir := filepath.Dir(path)
	d, err := privateDirectory(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	var before unix.Stat_t
	if err = unix.Fstatat(int(d.Fd()), filepath.Base(path), &before, unix.AT_SYMLINK_NOFOLLOW); err != nil || before.Mode&unix.S_IFMT != unix.S_IFSOCK || before.Mode&0077 != 0 || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 {
		return fmt.Errorf("SSH proxy socket must be private and same-user")
	}
	dial, cancel := context.WithTimeout(ctx, 5*time.Second)
	c, err := (&net.Dialer{}).DialContext(dial, "unix", path)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	defer input.Close()
	u, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("SSH proxy requires a local socket")
	}
	uid, err := peerUID(u)
	if err != nil || uid != uint32(os.Geteuid()) {
		return fmt.Errorf("SSH proxy peer is not the current account")
	}
	var after unix.Stat_t
	if validateDirectory(d, dir) != nil || unix.Fstatat(int(d.Fd()), filepath.Base(path), &after, unix.AT_SYMLINK_NOFOLLOW) != nil || before.Dev != after.Dev || before.Ino != after.Ino || after.Mode != before.Mode {
		return fmt.Errorf("SSH proxy socket changed")
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close(); _ = input.Close() })
	defer stop()
	upload := make(chan error, 1)
	go func() { _, err := io.CopyBuffer(c, input, make([]byte, 32<<10)); _ = u.CloseWrite(); upload <- err }()
	_, readErr := io.CopyBuffer(output, c, make([]byte, 32<<10))
	_ = c.Close()
	_ = input.Close()
	select {
	case writeErr := <-upload:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
		return writeErr
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Second):
		return fmt.Errorf("SSH proxy input did not stop")
	}
}
