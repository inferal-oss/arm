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
	homeKey        unixPrototypeHomeKey
	homeCharged    bool
	parentID       unixPrototypeID
	dirID          unixPrototypeID
	socketID       unixPrototypeID
	listener       *net.UnixListener
	setupRaw       *os.File // retained original until its one close is known
	setupRawClosed bool
	setupOther     net.Listener
	open           func(context.Context) (net.Conn, error)
	openTimeout    time.Duration // fixed at start; tests shorten it before accept
	openTimerHook  func()        // test barrier, captured before accept
	rejectedClose  func(*net.UnixConn) error
	cancel         context.CancelFunc
	done           chan struct{}
	mu             sync.Mutex
	active         map[*net.UnixConn]*unixPrototypePair
	emergency      *net.UnixConn // at most one accepted FD whose close was uncertain
	closed         bool
	setupTouched   bool
	joined         bool
	watcherJoined  bool
	cleanupErr     error // original native close, accept, or path proof
	applicationErr error // stream outcome; a joined adapter may still be cleaned up
	cleanupLedger  unixPrototypeErrorLedger
	appLedger      unixPrototypeErrorLedger
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
	active     bool // pending and active both own the same pair slot
	uncertain  bool // an original close failed; this pair keeps its slot
	localOnce  sync.Once
	remoteOnce sync.Once
}

const (
	unixPrototypeMaxAdapters        = 8
	unixPrototypeMaxAdaptersProcess = 32 // bounds retained owners across homes
	unixPrototypeMaxPairsPerAdapter = 2
	unixPrototypeMaxPairs           = 16
	unixPrototypeBacklog            = 2
	unixPrototypeSocketBuffer       = 8 << 10
	unixPrototypeSocketBufferCeil   = 64 << 10 // actual kernel readback, not the request
	unixPrototypeCopyBuffer         = 32 << 10
	unixPrototypeErrorSamples       = 8
)

type unixPrototypeErrorLedger struct {
	samples   [unixPrototypeErrorSamples]error
	n         uint8
	omitted   uint64
	saturated bool
}

func (l *unixPrototypeErrorLedger) add(err error) error {
	if err == nil {
		return l.result()
	}
	if int(l.n) < len(l.samples) {
		l.samples[l.n] = err
		l.n++
	} else if l.omitted < ^uint64(0) {
		l.omitted++
	} else {
		l.saturated = true
	}
	return l.result()
}

func (l *unixPrototypeErrorLedger) result() error {
	parts := make([]error, 0, int(l.n)+1)
	for i := uint8(0); i < l.n; i++ {
		parts = append(parts, l.samples[i])
	}
	if l.omitted != 0 || l.saturated {
		parts = append(parts, fmt.Errorf("additional errors omitted: count=%d saturated=%t", l.omitted, l.saturated))
	}
	return errors.Join(parts...)
}

var unixPrototypeSlots struct {
	sync.Mutex
	adapters    map[unixPrototypeHomeKey]int // physical opened home, including failed owners
	allAdapters int
	retained    map[*UnixPrototype]struct{} // actual failed original owners, not counters
	pairs       int                         // pending openers and active streams share this count
}

func unixPrototypeReserveGlobalAdapter() bool {
	unixPrototypeSlots.Lock()
	defer unixPrototypeSlots.Unlock()
	if unixPrototypeSlots.allAdapters >= unixPrototypeMaxAdaptersProcess {
		return false
	}
	unixPrototypeSlots.allAdapters++
	return true
}

func unixPrototypeReleaseGlobalAdapter() {
	unixPrototypeSlots.Lock()
	unixPrototypeSlots.allAdapters--
	unixPrototypeSlots.Unlock()
}

// A physical-home attempt owns one provisional count even when it finds the
// existing count at eight. A proven pre-mutation close returns that count;
// uncertain close retains the actual owner and charge rather than hiding an
// extra opened descriptor behind a nominal cap.
func unixPrototypeReserveHome(p *UnixPrototype) bool {
	unixPrototypeSlots.Lock()
	defer unixPrototypeSlots.Unlock()
	if unixPrototypeSlots.adapters == nil {
		unixPrototypeSlots.adapters = make(map[unixPrototypeHomeKey]int)
	}
	prior := unixPrototypeSlots.adapters[p.homeKey]
	unixPrototypeSlots.adapters[p.homeKey] = prior + 1
	p.homeCharged = true
	return prior < unixPrototypeMaxAdapters
}

func unixPrototypeReleaseOwner(p *UnixPrototype) {
	unixPrototypeSlots.Lock()
	if p.homeCharged {
		unixPrototypeSlots.adapters[p.homeKey]--
		if unixPrototypeSlots.adapters[p.homeKey] == 0 {
			delete(unixPrototypeSlots.adapters, p.homeKey)
		}
		p.homeCharged = false
	}
	unixPrototypeSlots.allAdapters--
	unixPrototypeSlots.Unlock()
}

func unixPrototypeRetainOwner(p *UnixPrototype) {
	unixPrototypeSlots.Lock()
	if unixPrototypeSlots.retained == nil {
		unixPrototypeSlots.retained = make(map[*UnixPrototype]struct{})
	}
	unixPrototypeSlots.retained[p] = struct{}{}
	unixPrototypeSlots.Unlock()
}

func unixPrototypeRetainedOwners() []*UnixPrototype {
	unixPrototypeSlots.Lock()
	defer unixPrototypeSlots.Unlock()
	out := make([]*UnixPrototype, 0, len(unixPrototypeSlots.retained))
	for owner := range unixPrototypeSlots.retained {
		out = append(out, owner)
	}
	return out
}

func unixPrototypeReservePair() bool {
	unixPrototypeSlots.Lock()
	defer unixPrototypeSlots.Unlock()
	if unixPrototypeSlots.pairs >= unixPrototypeMaxPairs {
		return false
	}
	unixPrototypeSlots.pairs++
	return true
}

func unixPrototypeReleasePair() {
	unixPrototypeSlots.Lock()
	unixPrototypeSlots.pairs--
	unixPrototypeSlots.Unlock()
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
	q.localOnce.Do(func() {
		err := q.local.Close()
		if err != nil {
			q.mu.Lock()
			q.uncertain = true
			q.mu.Unlock()
		}
		q.parent.recordCleanup(err)
	})
}

func (q *unixPrototypePair) closeRemote() {
	q.mu.Lock()
	remote := q.remote
	q.mu.Unlock()
	if remote != nil {
		q.remoteOnce.Do(func() {
			err := remote.Close()
			if err != nil {
				q.mu.Lock()
				q.uncertain = true
				q.mu.Unlock()
			}
			q.parent.recordCleanup(err)
		})
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

// Different literal paths can name the same physical directory (including
// case aliases on some Darwin filesystems). Capacity follows the opened FD.
type unixPrototypeHomeKey struct {
	dev, ino uint64
	uid      uint32
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

func unixPrototypeBoundSocketBuffers(fd int) error {
	for _, option := range []int{unix.SO_RCVBUF, unix.SO_SNDBUF} {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, option, unixPrototypeSocketBuffer); err != nil {
			return err
		}
		actual, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, option)
		if err != nil {
			return err
		}
		if actual <= 0 || actual > unixPrototypeSocketBufferCeil {
			return fmt.Errorf("source Unix socket buffer readback %d outside ceiling", actual)
		}
	}
	return nil
}

func unixPrototypeBoundAccepted(local *net.UnixConn) error {
	raw, err := local.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err = raw.Control(func(fd uintptr) { inner = unixPrototypeBoundSocketBuffers(int(fd)) }); err != nil {
		return err
	}
	return inner
}

// Configure backlog and socket buffers before bind or listen exposes a path.
// Every intermediate original remains on p if setup fails.
func (p *UnixPrototype) bindListener(path string) error {
	if err := p.openRawSocket(); err != nil {
		return err
	}
	fd := int(p.setupRaw.Fd())
	if err := unixPrototypeBoundSocketBuffers(fd); err != nil {
		return err
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		return err
	}
	if err := unix.Listen(fd, unixPrototypeBacklog); err != nil {
		return err
	}
	listener, err := net.FileListener(p.setupRaw)
	if err != nil {
		return err
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		p.setupOther = listener
		return errors.New("source Unix listener type changed")
	}
	p.listener = unixListener
	unixListener.SetUnlinkOnClose(false)
	closeErr := p.setupRaw.Close()
	p.setupRawClosed = true
	if closeErr != nil {
		return closeErr
	}
	p.setupRaw = nil
	return nil
}

// StartUnixPrototype binds a fresh socket under an existing private mesh
// directory. The caller's opener must complete pinned source-to-destination
// TLS and final grant acceptance; no Unix client byte is read before it
// succeeds. This prototype cannot establish that external opener's authority.
// The opener transfers sole ownership of every returned connection, including
// a partial (conn, error), and must not retain a watcher that closes it.
// A duplicate key or a path left by a crash is refused, never adopted or unlinked.
func StartUnixPrototype(ctx context.Context, home, key string, expires time.Time, open func(context.Context) (net.Conn, error)) (*UnixPrototype, error) {
	if ctx == nil || !hexID(key, 16) || open == nil || !expires.After(time.Now()) || expires.After(time.Now().Add(15*time.Minute)) {
		return nil, errors.New("exact bounded source Unix adapter authority required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return nil, errors.New("absolute clean source home required")
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || canonical != home {
		return nil, errors.New("canonical source home required")
	}
	if !unixPrototypeReserveGlobalAdapter() {
		return nil, errors.New("source Unix adapter capacity exhausted")
	}
	homeFD, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		unixPrototypeReleaseGlobalAdapter() // no opened original exists
		return nil, err
	}
	p := &UnixPrototype{key: key, name: "forward-unix-" + key, homePath: canonical, home: os.NewFile(uintptr(homeFD), "source-home"), open: open, openTimeout: 15 * time.Second, rejectedClose: (*net.UnixConn).Close, done: make(chan struct{}), active: map[*net.UnixConn]*unixPrototypePair{}}
	fail := func(e error) (*UnixPrototype, error) {
		// An uncertain setup never removes a path. It remains visible for exact
		// operator inspection and a subsequent start refuses the same key.
		var closeErr error
		if p.listener != nil {
			p.listener.SetUnlinkOnClose(false)
			closeErr = errors.Join(closeErr, p.listener.Close())
		}
		if p.setupOther != nil {
			closeErr = errors.Join(closeErr, p.setupOther.Close())
		}
		if p.setupRaw != nil && !p.setupRawClosed {
			closeErr = errors.Join(closeErr, p.setupRaw.Close())
			p.setupRawClosed = true
		}
		if p.dir != nil {
			closeErr = errors.Join(closeErr, p.dir.Close())
		}
		if p.parent != nil {
			closeErr = errors.Join(closeErr, p.parent.Close())
		}
		closeErr = errors.Join(closeErr, p.home.Close())
		if !p.setupTouched && closeErr == nil {
			unixPrototypeReleaseOwner(p)
		} else {
			p.cleanupErr = p.cleanupLedger.add(errors.Join(e, closeErr))
			unixPrototypeRetainOwner(p)
		}
		return nil, errors.Join(e, closeErr)
	}
	p.homeID, err = unixPrototypeStat(int(p.home.Fd()), "")
	if err != nil {
		return fail(errors.New("source home identity unavailable"))
	}
	p.homeKey = unixPrototypeHomeKey{dev: p.homeID.dev, ino: p.homeID.ino, uid: p.homeID.uid}
	if !unixPrototypeReserveHome(p) {
		return fail(errors.New("physical source home adapter capacity exhausted"))
	}
	if !unixPrototypePrivate(p.homeID, unix.S_IFDIR, 0700) {
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
	p.setupTouched = true // uncertain mutation retains the process-local slot
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
	if err = p.bindListener(path); err != nil {
		return fail(err)
	}
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
		p.closed = true
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
		if err := unixPrototypeBoundAccepted(local); err != nil {
			p.recordCleanup(err)
			if !p.rejectAccepted(local) {
				break
			}
			continue
		}
		p.mu.Lock()
		if p.closed || ctx.Err() != nil || len(p.active) >= unixPrototypeMaxPairsPerAdapter || !unixPrototypeReservePair() {
			p.mu.Unlock()
			if !p.rejectAccepted(local) {
				break
			}
			continue
		}
		p.active[local] = pair
		workers.Add(1)
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
	var cancelOpen context.CancelFunc
	defer func() {
		pair.abortAndClose()
		if cancelOpen != nil {
			cancelOpen() // returned original is closed before admission context ends
		}
		pair.mu.Lock()
		uncertain := pair.uncertain
		pair.mu.Unlock()
		p.mu.Lock()
		if !uncertain {
			delete(p.active, pair.local)
			unixPrototypeReleasePair()
		} else {
			p.closed = true
		}
		p.mu.Unlock()
		workers.Done()
	}()
	if !unixPrototypePeerAllowed(pair.local, uint32(os.Geteuid())) || ctx.Err() != nil {
		return
	}
	if err := p.intact(); err != nil {
		p.recordCleanup(err)
		return
	}
	// This is an admission deadline, not a stream lifetime. The timer only
	// cancels while open is pending. On success its child context remains
	// attached to the adapter lifetime, so a callback watching Done cannot
	// close an accepted stream fifteen seconds later.
	openCtx, cancel := context.WithCancel(ctx)
	cancelOpen = cancel
	var openMu sync.Mutex
	pending := true
	p.mu.Lock()
	openTimeout := p.openTimeout
	openTimerHook := p.openTimerHook
	p.mu.Unlock()
	deadline := time.Now().Add(openTimeout) // monotonic cut at opener start
	timerDone := make(chan struct{})
	timer := time.AfterFunc(time.Until(deadline), func() {
		defer close(timerDone)
		if openTimerHook != nil {
			openTimerHook()
		}
		openMu.Lock()
		if pending {
			cancelOpen()
		}
		openMu.Unlock()
	})
	remote, err := p.open(openCtx)
	returnedAt := time.Now() // stamp before timer coordination can delay us
	openMu.Lock()
	pending = false
	stopped := timer.Stop()
	openMu.Unlock()
	if stopped {
		close(timerDone) // callback was prevented and cannot close it
	} else {
		<-timerDone // an already-started callback is part of pair join
	}
	openErr := openCtx.Err()
	// Admission callbacks can return a partial connection alongside an error.
	// Own and close that original even when no application copy may start.
	if remote != nil {
		pair.setRemote(remote)
	}
	if err != nil || openErr != nil || !returnedAt.Before(deadline) {
		cancelOpen()
		return // admission failed before any Unix client byte was read
	}
	if remote == nil {
		cancelOpen()
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
	pair.mu.Lock()
	pair.active = true // pending -> active, retaining both original slots
	pair.mu.Unlock()
	finished := make(chan error, 2)
	copyDirection := func(dst io.Writer, src io.Reader, closeWrite func() error) {
		copyErr := unixPrototypeCopy(ctx, dst, src)
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

func unixPrototypePeerAllowed(local *net.UnixConn, expected uint32) bool {
	uid, err := unixPrototypePeerUID(local)
	return err == nil && uid == expected
}

// An explicit loop prevents ReaderFrom/WriterTo shortcuts and checks the
// lifetime before every bounded read and write. The opener remains responsible
// for its own TLS/control memory until a real Manager charge exists.
func unixPrototypeCopy(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, unixPrototypeCopyBuffer)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n < 0 || n > len(buf) {
			return errors.New("invalid source read count")
		}
		if n > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			for written := 0; written < n; {
				if err := ctx.Err(); err != nil {
					return err
				}
				m, err := dst.Write(buf[written:n])
				if m < 0 || m > n-written {
					return errors.New("invalid destination write count")
				}
				written += m
				if err != nil {
					return err
				}
				if m == 0 {
					return io.ErrShortWrite
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

func (p *UnixPrototype) closeListener() {
	p.listenerOnce.Do(func() { p.recordCleanup(p.listener.Close()) })
}

// A rejected accepted FD has no ordinary pair slot. At most one uncertain
// original may be retained per adapter; its first close failure immediately
// stops the sole accept loop and leaves the adapter capacity charged.
func (p *UnixPrototype) rejectAccepted(local *net.UnixConn) bool {
	p.mu.Lock()
	closeOriginal := p.rejectedClose
	p.mu.Unlock()
	if err := closeOriginal(local); err != nil {
		p.mu.Lock()
		p.emergency = local
		p.closed = true
		p.mu.Unlock()
		p.recordCleanup(err)
		return false
	}
	return true
}

func (p *UnixPrototype) recordCleanup(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.cleanupErr = p.cleanupLedger.add(err)
	p.closed = true
	p.mu.Unlock()
	p.cancel() // uncertainty withdraws admission, including before pair slots
}

func (p *UnixPrototype) recordApplication(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.applicationErr = p.appLedger.add(err)
	p.mu.Unlock()
}

func unixPrototypeCanceledIO(err error) bool {
	remaining := 256
	return unixPrototypeOnlyCanceledIO(err, 0, &remaining)
}

// errors.Is reports true when any joined leaf matches. For a mixed
// application/closed error that would erase the independent application
// failure after cancellation, so every leaf must be an explicit close error.
func unixPrototypeOnlyCanceledIO(err error, depth int, remaining *int) bool {
	if err == nil || depth > 32 || *remaining == 0 { // cycles or oversized trees are uncertain
		return false
	}
	*remaining = *remaining - 1
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !unixPrototypeOnlyCanceledIO(child, depth+1, remaining) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return unixPrototypeOnlyCanceledIO(wrapped.Unwrap(), depth+1, remaining)
	}
	return err == context.Canceled || err == net.ErrClosed ||
		err == os.ErrClosed || err == io.ErrClosedPipe
}

// Close reports uncertainty if positive join or exact original cleanup fails.
// The prototype has no durable ledger, so callers must not present this as a
// production recovery proof.
func (p *UnixPrototype) Close() error {
	if p.cancel == nil {
		return errors.Join(p.cleanupErr, errors.New("failed setup owner retained for inspection; no running adapter to close"))
	}
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
		closeErr := p.closeDescriptors()
		unixPrototypeRetainOwner(p)
		return errors.Join(applicationErr, prior, errors.New("Unix adapter join or close uncertain; original path retained"), closeErr)
	}
	if err := p.intact(); err != nil {
		closeErr := p.closeDescriptors()
		unixPrototypeRetainOwner(p)
		return errors.Join(applicationErr, err, closeErr)
	}
	if err := unix.Unlinkat(int(p.dir.Fd()), "service.sock", 0); err != nil {
		closeErr := p.closeDescriptors()
		unixPrototypeRetainOwner(p)
		return errors.Join(applicationErr, err, closeErr)
	}
	if err := unix.Unlinkat(int(p.parent.Fd()), p.name, unix.AT_REMOVEDIR); err != nil {
		closeErr := p.closeDescriptors()
		unixPrototypeRetainOwner(p)
		return errors.Join(applicationErr, err, closeErr)
	}
	// Unlink has already succeeded. A durability or descriptor-close failure
	// remains an uncertain result, but must not strand the other original FDs.
	syncErr := p.parent.Sync()
	closeErr := p.closeDescriptors()
	if syncErr == nil && closeErr == nil {
		unixPrototypeReleaseOwner(p)
	} else {
		unixPrototypeRetainOwner(p)
	}
	return errors.Join(applicationErr, syncErr, closeErr)
}
