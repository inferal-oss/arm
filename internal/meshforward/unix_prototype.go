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
	key            string
	name           string
	homePath       string
	home           *os.File
	parent         *os.File
	dir            *os.File
	homeID         unixPrototypeID
	parentID       unixPrototypeID
	dirID          unixPrototypeID
	socketID       unixPrototypeID
	listener       *net.UnixListener
	open           func(context.Context) (net.Conn, error)
	cancel         context.CancelFunc
	done           chan struct{}
	mu             sync.Mutex
	active         map[*net.UnixConn]*unixPrototypePair
	joined         bool
	watcherJoined  bool
	cleanupErr     error // original native close, accept, or path proof
	applicationErr error // stream outcome; a joined adapter may still be cleaned up
	listenerOnce   sync.Once
	finishOnce     sync.Once
	finishErr      error
}

// One accepted pair owns each original native close exactly once. A watcher
// may request abort before remote admission completes; setRemote then closes
// the newly returned remote without exposing application bytes.
type unixPrototypePair struct {
	parent     *UnixPrototype
	local      *net.UnixConn
	mu         sync.Mutex
	remote     net.Conn
	aborted    bool
	localOnce  sync.Once
	remoteOnce sync.Once
}

func (q *unixPrototypePair) setRemote(remote net.Conn) {
	q.mu.Lock()
	q.remote = remote
	aborted := q.aborted
	q.mu.Unlock()
	if aborted {
		q.closeRemote()
	}
}

func (q *unixPrototypePair) closeLocal() {
	q.localOnce.Do(func() { q.parent.recordCleanup(q.local.Close()) })
}

func (q *unixPrototypePair) closeRemote() {
	q.mu.Lock()
	remote := q.remote
	q.mu.Unlock()
	if remote != nil {
		q.remoteOnce.Do(func() { q.parent.recordCleanup(remote.Close()) })
	}
}

func (q *unixPrototypePair) abortAndClose() {
	q.mu.Lock()
	q.aborted = true
	q.mu.Unlock()
	q.closeLocal()
	q.closeRemote()
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

func unixPrototypeNamedStat(path string) (unixPrototypeID, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || canonical != filepath.Clean(home) {
		return nil, errors.New("canonical source home required")
	}
	homeFD, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	p := &UnixPrototype{key: key, name: "forward-unix-" + key, homePath: home, home: os.NewFile(uintptr(homeFD), "source-home"), open: open, done: make(chan struct{}), active: map[*net.UnixConn]*unixPrototypePair{}}
	fail := func(e error) (*UnixPrototype, error) {
		// An uncertain setup never removes a path. It remains visible for exact
		// operator inspection and a subsequent start refuses the same key.
		if p.listener != nil {
			p.listener.SetUnlinkOnClose(false)
			e = errors.Join(e, p.listener.Close())
		}
		if p.dir != nil {
			e = errors.Join(e, p.dir.Close())
		}
		if p.parent != nil {
			e = errors.Join(e, p.parent.Close())
		}
		e = errors.Join(e, p.home.Close())
		return nil, e
	}
	p.homeID, err = unixPrototypeStat(int(p.home.Fd()), "")
	if err != nil || !unixPrototypePrivate(p.homeID, unix.S_IFDIR, 0700) {
		return fail(errors.New("private source home required"))
	}
	if actual, e := unixPrototypeNamedStat(home); e != nil || actual != p.homeID {
		return fail(errors.New("named source home changed before binding"))
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
	if err = ctx.Err(); err != nil {
		return fail(err)
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
	if err = ctx.Err(); err != nil {
		return fail(err)
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
	canonical, err := filepath.EvalSymlinks(p.homePath)
	if err != nil || canonical != filepath.Clean(p.homePath) {
		return errors.New("named source home changed")
	}
	if actual, err := unixPrototypeNamedStat(p.homePath); err != nil || actual != p.homeID {
		return errors.New("named source home identity changed")
	}
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
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		<-ctx.Done()
		p.closeListener()
		p.mu.Lock()
		pairs := make([]*unixPrototypePair, 0, len(p.active))
		for _, pair := range p.active {
			pairs = append(pairs, pair)
		}
		p.mu.Unlock()
		for _, pair := range pairs {
			pair.abortAndClose()
		}
	}()
	for {
		local, err := p.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() == nil {
				p.recordCleanup(err)
			}
			break
		}
		pair := &unixPrototypePair{parent: p, local: local}
		workers.Add(1)
		p.mu.Lock()
		p.active[local] = pair
		p.mu.Unlock()
		go p.servePair(ctx, pair, &workers)
	}
	// An unexpected accept error also terminates active streams. The watcher
	// must finish its original closes before the workers can be called joined.
	p.cancel()
	<-watchDone
	workers.Wait()
	p.mu.Lock()
	p.joined = true
	p.watcherJoined = true
	p.mu.Unlock()
}

func (p *UnixPrototype) servePair(ctx context.Context, pair *unixPrototypePair, workers *sync.WaitGroup) {
	defer func() {
		pair.abortAndClose()
		p.mu.Lock()
		delete(p.active, pair.local)
		p.mu.Unlock()
		workers.Done()
	}()
	uid, err := unixPrototypePeerUID(pair.local)
	if err != nil || uid != uint32(os.Geteuid()) || ctx.Err() != nil {
		return
	}
	if err = p.intact(); err != nil {
		p.recordCleanup(err)
		return
	}
	remote, err := p.open(ctx)
	// Admission callbacks can return a partial connection alongside an error.
	// Own and close that original even when no application copy may start.
	if remote != nil {
		pair.setRemote(remote)
	}
	if err != nil {
		return // admission failed before any Unix client byte was read
	}
	if remote == nil {
		p.recordCleanup(errors.New("source admission returned no connection"))
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err = p.intact(); err != nil {
		p.recordCleanup(err)
		return
	}
	finished := make(chan error, 2)
	copyDirection := func(dst io.Writer, src io.Reader, closeWrite func() error) {
		_, copyErr := io.Copy(dst, src)
		if copyErr == nil && closeWrite != nil {
			copyErr = closeWrite()
		}
		if copyErr != nil {
			// A cancellation can cause an ordinary closed-connection error, but
			// must not erase an independent application EIO returned afterward.
			if ctx.Err() == nil || !unixPrototypeCanceledIO(copyErr) {
				p.recordApplication(copyErr)
			}
			pair.abortAndClose() // wake the other direction without replay
		}
		finished <- copyErr
	}
	var remoteHalf func() error
	if half, ok := remote.(interface{ CloseWrite() error }); ok {
		remoteHalf = half.CloseWrite
	}
	go copyDirection(remote, pair.local, remoteHalf)
	go copyDirection(pair.local, remote, pair.local.CloseWrite)
	<-finished
	<-finished
}

func (p *UnixPrototype) closeListener() {
	p.listenerOnce.Do(func() { p.recordCleanup(p.listener.Close()) })
}

func (p *UnixPrototype) recordCleanup(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.cleanupErr = errors.Join(p.cleanupErr, err)
	p.mu.Unlock()
}

func (p *UnixPrototype) recordApplication(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.applicationErr = errors.Join(p.applicationErr, err)
	p.mu.Unlock()
}

func unixPrototypeCanceledIO(err error) bool {
	return unixPrototypeOnlyCanceledIO(err, 0)
}

// errors.Is reports true when any joined leaf matches. For a mixed
// application/closed error that would erase the independent application
// failure after cancellation, so every leaf must be an explicit close error.
func unixPrototypeOnlyCanceledIO(err error, depth int) bool {
	if err == nil || depth > 32 { // a cyclic or unbounded wrapper is uncertain
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !unixPrototypeOnlyCanceledIO(child, depth+1) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return unixPrototypeOnlyCanceledIO(wrapped.Unwrap(), depth+1)
	}
	return err == context.Canceled || err == net.ErrClosed ||
		err == os.ErrClosed || err == io.ErrClosedPipe
}

// Close reports uncertainty if positive join or exact original cleanup fails.
// The prototype has no durable ledger, so callers must not present this as a
// production recovery proof.
func (p *UnixPrototype) Close() error {
	p.finishOnce.Do(func() { p.finishErr = p.finish() })
	return p.finishErr
}

func (p *UnixPrototype) closeDescriptors() error {
	return errors.Join(p.dir.Close(), p.parent.Close(), p.home.Close())
}

func (p *UnixPrototype) finish() error {
	p.cancel()
	p.closeListener()
	<-p.done
	p.mu.Lock()
	joined, watcherJoined, prior, applicationErr := p.joined, p.watcherJoined, p.cleanupErr, p.applicationErr
	p.mu.Unlock()
	if !joined || !watcherJoined || prior != nil {
		return errors.Join(applicationErr, prior, errors.New("Unix adapter join or close uncertain; original path retained"), p.closeDescriptors())
	}
	if err := p.intact(); err != nil {
		return errors.Join(applicationErr, err, p.closeDescriptors())
	}
	if err := unix.Unlinkat(int(p.dir.Fd()), "service.sock", 0); err != nil {
		return errors.Join(applicationErr, err, p.closeDescriptors())
	}
	if err := unix.Unlinkat(int(p.parent.Fd()), p.name, unix.AT_REMOVEDIR); err != nil {
		return errors.Join(applicationErr, err, p.closeDescriptors())
	}
	// Unlink has already succeeded. A durability or descriptor-close failure
	// remains an uncertain result, but must not strand the other original FDs.
	syncErr := p.parent.Sync()
	return errors.Join(applicationErr, syncErr, p.closeDescriptors())
}
