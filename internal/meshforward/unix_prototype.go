//go:build arm_forward_review && arm_forward_unix_prototype && (darwin || linux)

package meshforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// UnixPrototype is an isolated source-side data-path experiment. There is no
// public dispatch or durable Manager lease for it. A production adapter must
// reserve that lease before binding and preserve uncertain cleanup on restart.
type UnixPrototype struct {
	key      string
	name     string
	homePath string
	home     *os.File
	parent   *os.File
	dir      *os.File
	homeID   unixPrototypeID
	parentID unixPrototypeID
	dirID    unixPrototypeID
	socketID unixPrototypeID
	listener *net.UnixListener
	open     func(context.Context) (net.Conn, error)
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	active   map[*net.UnixConn]net.Conn
	joined   bool
	err      error
}

type unixPrototypeID struct {
	dev, ino uint64
	uid      uint32
	mode     uint32
}

func unixPrototypeStat(fd int, name string) (unixPrototypeID, error) {
	var st unix.Stat_t
	var err error
	if name == "" {
		err = unix.Fstat(fd, &st)
	} else {
		err = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		return unixPrototypeID{}, err
	}
	return unixPrototypeID{uint64(st.Dev), st.Ino, st.Uid, uint32(st.Mode)}, nil
}

func unixPrototypePrivate(id unixPrototypeID, kind uint32, mode uint32) bool {
	return id.uid == uint32(os.Geteuid()) && id.mode&unix.S_IFMT == kind && id.mode&07777 == mode
}

// StartUnixPrototype binds a fresh socket under an existing private mesh
// directory. open must perform the existing pinned source TLS and token READY
// admission; no Unix client byte is read before it succeeds. A duplicate key
// or a path left by a crash is refused, never adopted or unlinked.
func StartUnixPrototype(ctx context.Context, home, key string, expires time.Time, open func(context.Context) (net.Conn, error)) (*UnixPrototype, error) {
	if ctx == nil || !hexID(key, 16) || open == nil || !expires.After(time.Now()) || expires.After(time.Now().Add(15*time.Minute)) {
		return nil, errors.New("exact bounded source Unix adapter authority required")
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || canonical != filepath.Clean(home) {
		return nil, errors.New("canonical source home required")
	}
	homeFD, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	p := &UnixPrototype{key: key, name: "forward-unix-" + key, homePath: home, home: os.NewFile(uintptr(homeFD), "source-home"), open: open, done: make(chan struct{}), active: map[*net.UnixConn]net.Conn{}}
	fail := func(e error) (*UnixPrototype, error) {
		// An uncertain setup never removes a path. It remains visible for exact
		// operator inspection and a subsequent start refuses the same key.
		if p.listener != nil {
			p.listener.SetUnlinkOnClose(false)
			_ = p.listener.Close()
		}
		if p.dir != nil {
			_ = p.dir.Close()
		}
		if p.parent != nil {
			_ = p.parent.Close()
		}
		_ = p.home.Close()
		return nil, e
	}
	p.homeID, err = unixPrototypeStat(int(p.home.Fd()), "")
	if err != nil || !unixPrototypePrivate(p.homeID, unix.S_IFDIR, 0700) {
		return fail(errors.New("private source home required"))
	}
	parentFD, err := unix.Openat(int(p.home.Fd()), "mesh", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(err)
	}
	p.parent = os.NewFile(uintptr(parentFD), "mesh")
	p.parentID, err = unixPrototypeStat(int(p.parent.Fd()), "")
	if err != nil || !unixPrototypePrivate(p.parentID, unix.S_IFDIR, 0700) {
		return fail(errors.New("private source mesh directory required"))
	}
	if actual, e := unixPrototypeStat(int(p.home.Fd()), "mesh"); e != nil || actual != p.parentID {
		return fail(errors.New("source mesh directory replaced"))
	}
	if err = unix.Mkdirat(int(p.parent.Fd()), p.name, 0700); err != nil {
		return fail(fmt.Errorf("fresh Unix adapter directory required: %w", err))
	}
	dirFD, err := unix.Openat(int(p.parent.Fd()), p.name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(err)
	}
	p.dir = os.NewFile(uintptr(dirFD), p.name)
	p.dirID, err = unixPrototypeStat(int(p.dir.Fd()), "")
	if err != nil || !unixPrototypePrivate(p.dirID, unix.S_IFDIR, 0700) {
		return fail(errors.New("new Unix adapter directory identity invalid"))
	}
	if actual, e := unixPrototypeStat(int(p.parent.Fd()), p.name); e != nil || actual != p.dirID {
		return fail(errors.New("new Unix adapter directory replaced"))
	}
	if _, e := unixPrototypeStat(int(p.dir.Fd()), "service.sock"); !errors.Is(e, unix.ENOENT) {
		return fail(errors.New("Unix adapter socket name already exists or cannot be inspected"))
	}
	path := filepath.Join(home, "mesh", p.name, "service.sock")
	p.listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fail(err)
	}
	p.listener.SetUnlinkOnClose(false)
	p.socketID, err = unixPrototypeStat(int(p.dir.Fd()), "service.sock")
	if err != nil || p.socketID.uid != uint32(os.Geteuid()) || p.socketID.mode&unix.S_IFMT != unix.S_IFSOCK {
		return fail(errors.New("new Unix adapter socket identity invalid"))
	}
	if err = unix.Fchmodat(int(p.dir.Fd()), "service.sock", 0600, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fail(err)
	}
	initialSocket := p.socketID
	if p.socketID, err = unixPrototypeStat(int(p.dir.Fd()), "service.sock"); err != nil || !unixPrototypePrivate(p.socketID, unix.S_IFSOCK, 0600) || p.socketID.dev != initialSocket.dev || p.socketID.ino != initialSocket.ino {
		return fail(errors.New("new Unix adapter socket permissions invalid"))
	}
	runCtx, cancel := context.WithDeadline(ctx, expires)
	p.cancel = cancel
	go p.run(runCtx)
	return p, nil
}

func (p *UnixPrototype) Path() string {
	return filepath.Join(p.homePath, "mesh", p.name, "service.sock")
}

func (p *UnixPrototype) intact() error {
	for _, check := range []struct {
		fd       int
		name     string
		expected unixPrototypeID
	}{
		{int(p.home.Fd()), "", p.homeID},
		{int(p.home.Fd()), "mesh", p.parentID},
		{int(p.parent.Fd()), "", p.parentID},
		{int(p.parent.Fd()), p.name, p.dirID},
		{int(p.dir.Fd()), "", p.dirID},
		{int(p.dir.Fd()), "service.sock", p.socketID},
	} {
		actual, err := unixPrototypeStat(check.fd, check.name)
		if err != nil || actual != check.expected {
			return errors.New("source Unix adapter path identity changed")
		}
	}
	return nil
}

func (p *UnixPrototype) run(ctx context.Context) {
	defer close(p.done)
	var workers sync.WaitGroup
	go func() {
		<-ctx.Done()
		_ = p.listener.Close()
		p.mu.Lock()
		for local, remote := range p.active {
			_ = local.Close()
			if remote != nil {
				_ = remote.Close()
			}
		}
		p.mu.Unlock()
	}()
	for {
		local, err := p.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				p.record(err)
			}
			break
		}
		workers.Add(1)
		p.mu.Lock()
		p.active[local] = nil
		p.mu.Unlock()
		go func() {
			defer workers.Done()
			defer local.Close()
			defer func() { p.mu.Lock(); delete(p.active, local); p.mu.Unlock() }()
			uid, err := unixPrototypePeerUID(local)
			if err != nil || uid != uint32(os.Geteuid()) || p.intact() != nil || ctx.Err() != nil {
				return
			}
			remote, err := p.open(ctx)
			if err != nil || remote == nil {
				return
			}
			defer remote.Close()
			p.mu.Lock()
			p.active[local] = remote
			p.mu.Unlock()
			if ctx.Err() != nil || p.intact() != nil {
				return
			}
			finished := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(remote, local)
				if half, ok := remote.(interface{ CloseWrite() error }); ok {
					_ = half.CloseWrite()
				}
				finished <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(local, remote)
				_ = local.CloseWrite()
				finished <- struct{}{}
			}()
			<-finished
			<-finished
		}()
	}
	workers.Wait()
	p.mu.Lock()
	p.joined = true
	p.mu.Unlock()
}

func (p *UnixPrototype) record(err error) {
	p.mu.Lock()
	p.err = errors.Join(p.err, err)
	p.mu.Unlock()
}

// Close reports uncertainty if positive join or exact original cleanup fails.
// The prototype has no durable ledger, so callers must not present this as a
// production recovery proof.
func (p *UnixPrototype) Close() error {
	p.cancel()
	if err := p.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		p.record(err)
	}
	<-p.done
	p.mu.Lock()
	joined, prior := p.joined, p.err
	p.mu.Unlock()
	if !joined || prior != nil {
		return errors.Join(prior, errors.New("Unix adapter join or close uncertain; original path retained"))
	}
	if err := p.intact(); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(p.dir.Fd()), "service.sock", 0); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(p.parent.Fd()), p.name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	if err := p.parent.Sync(); err != nil {
		return err
	}
	if err := p.dir.Close(); err != nil {
		return err
	}
	if err := p.parent.Close(); err != nil {
		return err
	}
	return p.home.Close()
}
