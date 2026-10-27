//go:build arm_forward_review && arm_forward_unix_prototype && (darwin || linux)

package meshforward

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func unixPrototypeQueuedBytes(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var queued int
	var inner error
	err = raw.Control(func(fd uintptr) {
		queued, inner = unix.IoctlGetInt(int(fd), unixPrototypeFIONREAD)
	})
	if err != nil {
		return 0, err
	}
	return queued, inner
}

func TestUnixPrototypeQueuedByteProbeDetectsPrematureRead(t *testing.T) {
	home := unixPrototypeHome(t)
	path := filepath.Join(home, "queue-control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(time.Second))
	client, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	accepted, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	if _, err := client.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	before, err := unixPrototypeQueuedBytes(accepted)
	if err != nil || before != len("effect") {
		t.Fatal("receive queue did not expose the unread body", before, err)
	}
	var one [1]byte
	if n, err := accepted.Read(one[:]); n != 1 || err != nil {
		t.Fatal("negative-control read failed", n, err)
	}
	after, err := unixPrototypeQueuedBytes(accepted)
	if err != nil || after != before-1 {
		t.Fatal("receive-queue check would miss a premature read", before, after, err)
	}
}

type unixPrototypeNoFastReader struct{ *bytes.Reader }

func (*unixPrototypeNoFastReader) WriteTo(io.Writer) (int64, error) {
	panic("source WriterTo bypassed bounded copy")
}

type unixPrototypeNoFastWriter struct {
	writes int
	max    int
	cancel context.CancelFunc
}

func (w *unixPrototypeNoFastWriter) ReadFrom(io.Reader) (int64, error) {
	panic("destination ReaderFrom bypassed bounded copy")
}

func (w *unixPrototypeNoFastWriter) Write(b []byte) (int, error) {
	w.writes++
	if len(b) > w.max {
		w.max = len(b)
	}
	if w.cancel != nil {
		w.cancel()
	}
	return len(b), nil
}

func TestUnixPrototypeCopyBoundsChunksAndHidesFastPaths(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	dst := &unixPrototypeNoFastWriter{cancel: cancel}
	src := &unixPrototypeNoFastReader{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 3*unixPrototypeCopyBuffer))}
	if err := unixPrototypeCopy(ctx, dst, src); !errors.Is(err, context.Canceled) {
		t.Fatal("copy crossed canceled lifetime", err)
	}
	if dst.writes != 1 || dst.max != unixPrototypeCopyBuffer || src.Len() != 2*unixPrototypeCopyBuffer {
		t.Fatal("copy exceeded one bounded chunk", dst.writes, dst.max, src.Len())
	}
}

type unixPrototypeReadResult struct {
	payload []byte
	err     error
	used    bool
}

func (r *unixPrototypeReadResult) Read(dst []byte) (int, error) {
	if r.used {
		return 0, io.EOF
	}
	r.used = true
	return copy(dst, r.payload), r.err
}

func TestUnixPrototypeCopyOnlyLiteralEOFIsSuccess(t *testing.T) {
	mixed := errors.Join(io.EOF, syscall.EIO)
	for _, tc := range []struct {
		name    string
		body    string
		readErr error
		wantErr error
	}{
		{"plain_zero", "", io.EOF, nil},
		{"plain_positive", "ack", io.EOF, nil},
		{"mixed_zero", "", mixed, syscall.EIO},
		{"mixed_positive", "ack", mixed, syscall.EIO},
		{"wrapped_eof", "ack", fmt.Errorf("wrapped: %w", io.EOF), io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst bytes.Buffer
			err := unixPrototypeCopy(t.Context(), &dst, &unixPrototypeReadResult{payload: []byte(tc.body), err: tc.readErr})
			if dst.String() != tc.body {
				t.Fatal("read result bytes changed", dst.String(), tc.body)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatal("literal EOF was not accepted", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatal("read error was masked", err, tc.wantErr)
			}
		})
	}
}

type unixPrototypeFaultReadConn struct {
	net.Conn
	result unixPrototypeReadResult
}

func (c *unixPrototypeFaultReadConn) Read(dst []byte) (int, error) {
	return c.result.Read(dst)
}

func TestUnixPrototypeMixedEOFStreamAbortsAndPreservesOutcome(t *testing.T) {
	home := unixPrototypeHome(t)
	peers := make(chan net.Conn, 1)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("e", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		client, peer := net.Pipe()
		peers <- peer
		return &unixPrototypeFaultReadConn{Conn: client, result: unixPrototypeReadResult{payload: []byte("ack"), err: errors.Join(io.EOF, syscall.EIO)}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var peer net.Conn
	select {
	case peer = <-peers:
	case <-time.After(time.Second):
		t.Fatal("faulted remote original did not open")
	}
	defer peer.Close()
	_ = local.SetReadDeadline(time.Now().Add(time.Second))
	var body [3]byte
	if _, err := io.ReadFull(local, body[:]); err != nil || string(body[:]) != "ack" {
		t.Fatal("positive read bytes did not cross before outcome error", string(body[:]), err)
	}
	var trailing [1]byte
	if n, err := local.Read(trailing[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("mixed EOF/EIO did not abort the stream", n, err)
	}
	if err := p.Close(); !errors.Is(err, syscall.EIO) {
		t.Fatal("mixed EOF/EIO was masked by apparent EOF", err)
	}
	if _, err := os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined application failure retained clean native path", err)
	}
}

func TestUnixPrototypeRejectsHomeAliasesBeforeReservation(t *testing.T) {
	home := unixPrototypeHome(t)
	alias := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	unixPrototypeSlots.Lock()
	before := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	for _, candidate := range []string{
		strings.TrimPrefix(home, "/"),
		home + "/.",
		home + "/mesh/..",
		alias,
	} {
		if owner, err := StartUnixPrototype(t.Context(), candidate, strings.Repeat("9", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, nil }); owner != nil || err == nil {
			t.Fatal("alias obtained an adapter", candidate, owner, err)
		}
		if _, err := os.Lstat(filepath.Join(home, "mesh", "forward-unix-"+strings.Repeat("9", 32))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected alias changed the filesystem", candidate, err)
		}
	}
	unixPrototypeSlots.Lock()
	after := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if before != after {
		t.Fatal("rejected alias changed adapter capacity", before, after)
	}
}

func TestUnixPrototypePendingPairsOccupyCapUntilOpenerJoins(t *testing.T) {
	home := unixPrototypeHome(t)
	entered := make(chan struct{}, unixPrototypeMaxPairsPerAdapter)
	release := make(chan struct{})
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("7", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		entered <- struct{}{}
		<-release // deliberately ignores cancellation to test positive join
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseOpen := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { releaseOpen(); _ = p.Close() })
	for i := 0; i < unixPrototypeMaxPairsPerAdapter; i++ {
		c, err := net.DialTimeout("unix", p.Path(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("pending opener did not start")
		}
	}
	third, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	_ = third.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := third.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("third pair was admitted", n, err)
	}
	select {
	case <-entered:
		t.Fatal("third opener bypassed pair cap")
	default:
	}
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case err := <-closed:
		t.Fatal("Close returned before pending openers joined", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseOpen()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending openers did not join")
	}
	unixPrototypeSlots.Lock()
	pairs := unixPrototypeSlots.pairs
	unixPrototypeSlots.Unlock()
	if pairs != 0 {
		t.Fatal("joined prototype retained pair slots", pairs)
	}
}

func TestUnixPrototypeEarlyOpenJoinsTimerWithoutStreamDeadline(t *testing.T) {
	home := unixPrototypeHome(t)
	adapterCtx, cancelAdapter := context.WithCancel(t.Context())
	defer cancelAdapter()
	returned := make(chan net.Conn, 1)
	openCanceled := make(chan struct{})
	p, err := StartUnixPrototype(adapterCtx, home, strings.Repeat("4", 32), time.Now().Add(time.Minute), func(openCtx context.Context) (net.Conn, error) {
		client, peer := net.Pipe()
		go func() { <-openCtx.Done(); close(openCanceled) }()
		returned <- peer
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelAdapter(); _ = p.Close() })
	p.mu.Lock()
	p.openTimeout = 40 * time.Millisecond // set before any accept
	p.mu.Unlock()
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var peer net.Conn
	select {
	case peer = <-returned:
	case <-time.After(time.Second):
		t.Fatal("successful opener did not return a remote original")
	}
	defer peer.Close()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-openCanceled:
		t.Fatal("successful opener context expired before stream ended")
	default:
	}
	if _, err := local.Write([]byte("alive")); err != nil {
		t.Fatal("accepted stream inherited opener timeout", err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var got [5]byte
	if _, err := io.ReadFull(peer, got[:]); err != nil || string(got[:]) != "alive" {
		t.Fatal("accepted stream ended at opener deadline", string(got[:]), err)
	}
	cancelAdapter()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-openCanceled:
	case <-time.After(time.Second):
		t.Fatal("opener context watcher did not finish after stream close")
	}
}

func TestUnixPrototypePendingOpenerDeadlineClosesWithoutReadingClient(t *testing.T) {
	home := unixPrototypeHome(t)
	adapterCtx, cancelAdapter := context.WithCancel(t.Context())
	defer cancelAdapter()
	entered := make(chan struct{})
	p, err := StartUnixPrototype(adapterCtx, home, strings.Repeat("0", 32), time.Now().Add(time.Minute), func(openCtx context.Context) (net.Conn, error) {
		close(entered)
		<-openCtx.Done()
		return nil, openCtx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancelAdapter(); _ = p.Close() })
	p.mu.Lock()
	p.openTimeout = 40 * time.Millisecond // set before any accept
	p.mu.Unlock()
	client, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("not-admitted")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("opener did not start")
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := client.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("timed-out opener admitted client bytes", n, err)
	}
	if adapterCtx.Err() != nil {
		t.Fatal("opener deadline canceled the original adapter")
	}
}

func TestUnixPrototypeStartedTimerCallbackJoinsBeforeLateRefusal(t *testing.T) {
	home := unixPrototypeHome(t)
	admit := make(chan struct{})
	timerEntered := make(chan struct{})
	timerRelease := make(chan struct{})
	remoteReady := make(chan net.Conn, 1)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("a", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		<-admit
		client, peer := net.Pipe()
		remoteReady <- peer
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseAdmit := sync.OnceFunc(func() { close(admit) })
	releaseTimer := sync.OnceFunc(func() { close(timerRelease) })
	t.Cleanup(func() { releaseAdmit(); releaseTimer(); _ = p.Close() })
	p.mu.Lock()
	p.openTimeout = 20 * time.Millisecond
	p.openTimerHook = func() { close(timerEntered); <-timerRelease }
	p.mu.Unlock()
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, err := local.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-timerEntered:
	case <-time.After(time.Second):
		t.Fatal("timer callback did not start")
	}
	releaseAdmit()
	var peer net.Conn
	select {
	case peer = <-remoteReady:
	case <-time.After(time.Second):
		t.Fatal("opener did not return after barrier release")
	}
	defer peer.Close()
	_ = peer.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	if n, err := peer.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatal("application crossed before timer callback joined", n, err)
	}
	releaseTimer()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := peer.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("late opener received application bytes", n, err)
	}
	_ = local.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := local.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("late opener was admitted after timer callback joined", n, err)
	}
}

func TestUnixPrototypeStartedTimerCallbackJoinsOnCancellation(t *testing.T) {
	home := unixPrototypeHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	timerEntered := make(chan struct{})
	timerRelease := make(chan struct{})
	p, err := StartUnixPrototype(ctx, home, strings.Repeat("b", 32), time.Now().Add(time.Minute), func(openCtx context.Context) (net.Conn, error) {
		<-openCtx.Done()
		return nil, openCtx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseTimer := sync.OnceFunc(func() { close(timerRelease) })
	t.Cleanup(func() { cancel(); releaseTimer(); _ = p.Close() })
	p.mu.Lock()
	p.openTimeout = 20 * time.Millisecond
	p.openTimerHook = func() { close(timerEntered); <-timerRelease }
	p.mu.Unlock()
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	select {
	case <-timerEntered:
	case <-time.After(time.Second):
		t.Fatal("timer callback did not start")
	}
	cancel()
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case err := <-closed:
		t.Fatal("Close returned before started timer callback joined", err)
	case <-time.After(40 * time.Millisecond):
	}
	releaseTimer()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not join started timer callback")
	}
}

func TestUnixPrototypeGlobalPendingCapAcrossHomes(t *testing.T) {
	homeA, homeB := unixPrototypeHome(t), unixPrototypeHome(t)
	release := make(chan struct{})
	owners := make([]*UnixPrototype, 0, unixPrototypeMaxAdapters+1)
	t.Cleanup(func() {
		close(release)
		for _, owner := range owners {
			_ = owner.Close()
		}
	})
	entered := make(chan struct{}, unixPrototypeMaxPairs)
	blockedOpen := func(context.Context) (net.Conn, error) {
		entered <- struct{}{}
		<-release
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	}
	for i := 0; i < unixPrototypeMaxAdapters; i++ {
		p, err := StartUnixPrototype(t.Context(), homeA, fmt.Sprintf("%032x", i+1), time.Now().Add(time.Minute), blockedOpen)
		if err != nil {
			t.Fatal(err)
		}
		owners = append(owners, p)
		for j := 0; j < unixPrototypeMaxPairsPerAdapter; j++ {
			c, err := net.DialTimeout("unix", p.Path(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("pending pair did not take a global slot")
			}
		}
	}
	physicalKey := owners[0].homeKey
	unixPrototypeSlots.Lock()
	beforeHomeRefusal, beforeGlobalRefusal := unixPrototypeSlots.adapters[physicalKey], unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if beforeHomeRefusal != unixPrototypeMaxAdapters {
		t.Fatal("opened home identity did not hold eight owners", beforeHomeRefusal)
	}
	refusedKey := strings.Repeat("f", 32)
	if p, err := StartUnixPrototype(t.Context(), homeA, refusedKey, time.Now().Add(time.Minute), blockedOpen); p != nil || err == nil {
		t.Fatal("ninth adapter in one home bypassed retained-owner cap", p, err)
	}
	if _, err := os.Lstat(filepath.Join(homeA, "mesh", "forward-unix-"+refusedKey)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("physical-home capacity refusal created an adapter directory", err)
	}
	unixPrototypeSlots.Lock()
	afterHomeRefusal, afterGlobalRefusal := unixPrototypeSlots.adapters[physicalKey], unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if afterHomeRefusal != beforeHomeRefusal || afterGlobalRefusal != beforeGlobalRefusal {
		t.Fatal("physical-home refusal changed held owner counts", beforeHomeRefusal, afterHomeRefusal, beforeGlobalRefusal, afterGlobalRefusal)
	}
	unexpected := make(chan struct{}, 1)
	p, err := StartUnixPrototype(t.Context(), homeB, strings.Repeat("e", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		unexpected <- struct{}{}
		return nil, errors.New("global slot bypass")
	})
	if err != nil {
		t.Fatal(err)
	}
	owners = append(owners, p)
	c, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := c.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("seventeenth pending pair was not refused", n, err)
	}
	select {
	case <-unexpected:
		t.Fatal("seventeenth opener started")
	default:
	}
	unixPrototypeSlots.Lock()
	pairs := unixPrototypeSlots.pairs
	unixPrototypeSlots.Unlock()
	if pairs != unixPrototypeMaxPairs {
		t.Fatal("global pending count changed", pairs)
	}
}

func TestUnixPrototypeProcessAdapterCapAndExactRecycle(t *testing.T) {
	unixPrototypeSlots.Lock()
	baseline := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	remaining := unixPrototypeMaxAdaptersProcess - baseline
	if remaining < 1 {
		t.Fatal("no process capacity available for causal cap control", baseline)
	}
	owners := make([]*UnixPrototype, 0, remaining)
	key := strings.Repeat("a", 32)
	for i := 0; i < remaining; i++ {
		home := unixPrototypeHome(t) // a distinct canonical private backend home
		p, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
			return nil, errors.New("unused")
		})
		if err != nil {
			t.Fatal("failed before filling remaining process capacity", i, remaining, err)
		}
		owners = append(owners, p)
		t.Cleanup(func() { _ = p.Close() }) // executes before this home's cleanup
	}
	unixPrototypeSlots.Lock()
	full := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if full != unixPrototypeMaxAdaptersProcess {
		t.Fatal("process adapter capacity did not fill exactly", full)
	}
	refusedHome := unixPrototypeHome(t)
	refusedPath := filepath.Join(refusedHome, "mesh", "forward-unix-"+key)
	if p, err := StartUnixPrototype(t.Context(), refusedHome, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		return nil, nil
	}); p != nil || err == nil {
		t.Fatal("owner beyond process cap was admitted", p, err)
	}
	if _, err := os.Lstat(refusedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("capacity refusal mutated a new adapter path", err)
	}
	unixPrototypeSlots.Lock()
	afterRefusal := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if afterRefusal != full {
		t.Fatal("capacity refusal changed held owner count", full, afterRefusal)
	}
	for _, p := range owners {
		if err := p.Close(); err != nil {
			t.Fatal("normal original cleanup did not release its slot", err)
		}
	}
	unixPrototypeSlots.Lock()
	afterClose := unixPrototypeSlots.allAdapters
	unixPrototypeSlots.Unlock()
	if afterClose != baseline {
		t.Fatal("normal close changed pre-existing uncertain owners", baseline, afterClose)
	}
}

func TestUnixPrototypeNativePeerUIDWrongExpectedControl(t *testing.T) {
	home := unixPrototypeHome(t)
	path := filepath.Join(home, "peer-control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(time.Second))
	client, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	actual := uint32(os.Geteuid())
	if !unixPrototypePeerAllowed(peer, actual) || unixPrototypePeerAllowed(peer, actual^1) {
		t.Fatal("native peer UID did not enforce the supplied expected UID")
	}
	// Both processes in this test have the same actual UID. This is a wrong
	// expected-UID control, not a cross-account authorization receipt.
}

func TestUnixPrototypeListenerBufferReadbackWithinCeiling(t *testing.T) {
	home := unixPrototypeHome(t)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("3", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, errors.New("unused") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	raw, err := p.listener.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var inner error
	if err = raw.Control(func(fd uintptr) {
		for _, option := range []int{unix.SO_RCVBUF, unix.SO_SNDBUF} {
			actual, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, option)
			if e != nil {
				inner = e
				return
			}
			if actual <= 0 || actual > unixPrototypeSocketBufferCeil {
				inner = fmt.Errorf("actual socket buffer %d outside %d ceiling", actual, unixPrototypeSocketBufferCeil)
				return
			}
		}
	}); err != nil || inner != nil {
		t.Fatal("listener buffer readback failed", err, inner)
	}
}

func TestUnixPrototypeRawSocketHasCloexecBeforeBind(t *testing.T) {
	p := &UnixPrototype{}
	t.Cleanup(func() {
		if p.setupRaw != nil && !p.setupRawClosed {
			_ = p.setupRaw.Close()
		}
	})
	if err := p.openRawSocket(); err != nil {
		t.Fatal(err)
	}
	flags, err := unix.FcntlInt(p.setupRaw.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("raw listener could be inherited across exec", flags, err)
	}
}

type unixPrototypeListError []error

func (e unixPrototypeListError) Error() string   { return "test error list" }
func (e unixPrototypeListError) Unwrap() []error { return []error(e) }

type unixPrototypeCycleError struct{}

func (e *unixPrototypeCycleError) Error() string { return "test error cycle" }
func (e *unixPrototypeCycleError) Unwrap() error { return e }

func TestUnixPrototypeCancellationErrorTreeBound(t *testing.T) {
	dag := error(net.ErrClosed)
	for i := 0; i < 20; i++ {
		dag = errors.Join(dag, dag)
	}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"plain_cancel", context.Canceled, true},
		{"wrapped_close", fmt.Errorf("socket: %w", net.ErrClosed), true},
		{"benign_join", errors.Join(net.ErrClosed, io.ErrClosedPipe), true},
		{"wrapped_benign_join", fmt.Errorf("socket: %w", errors.Join(os.ErrClosed, net.ErrClosed)), true},
		{"mixed_join", errors.Join(syscall.EIO, net.ErrClosed), false},
		{"malformed_empty", unixPrototypeListError{}, false},
		{"malformed_nil_child", unixPrototypeListError{nil, net.ErrClosed}, false},
		{"cycle", &unixPrototypeCycleError{}, false},
		{"repeated_dag", dag, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := unixPrototypeCanceledIO(tc.err); got != tc.want {
				t.Fatalf("cancellation classification = %t, want %t", got, tc.want)
			}
		})
	}
}

func unixPrototypeHome(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "fup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			t.Errorf("remove native adapter home %q: %v", home, err)
		}
		if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("native adapter home remains after cleanup %q: %v", home, err)
		}
	})
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestUnixPrototypeAdmissionBeforeApplicationBytesAndCancellationJoin(t *testing.T) {
	home := unixPrototypeHome(t)
	key := strings.Repeat("a", 32)
	entered := make(chan struct{})
	admit := make(chan struct{})
	remoteReady := make(chan net.Conn, 1)
	ctx, cancel := context.WithCancel(t.Context())
	p, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		close(entered)
		<-admit // models final pinned TLS and destination grant acceptance
		client, server := net.Pipe()
		remoteReady <- server
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseAdmission := sync.OnceFunc(func() { close(admit) })
	t.Cleanup(func() { releaseAdmission(); _ = p.Close() })
	client, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("admission did not start")
	}
	select {
	case <-remoteReady:
		t.Fatal("application reached remote before admission")
	default:
	}
	p.mu.Lock()
	var accepted *net.UnixConn
	for local := range p.active {
		accepted = local
	}
	p.mu.Unlock()
	if accepted == nil {
		t.Fatal("accepted original not registered before opener")
	}
	queued, err := unixPrototypeQueuedBytes(accepted)
	if err != nil || queued != len("effect") {
		t.Fatal("Unix client bytes were read before admission", queued, err)
	}
	releaseAdmission()
	var remote net.Conn
	select {
	case remote = <-remoteReady:
	case <-time.After(time.Second):
		t.Fatal("admitted remote did not return")
	}
	defer remote.Close()
	if err = remote.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 6)
	if _, err = io.ReadFull(remote, bytes); err != nil || string(bytes) != "effect" {
		t.Fatal(string(bytes), err)
	}
	cancel()
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if !p.joined || !p.watcherJoined {
		t.Fatal("Close returned before watcher and workers joined")
	}
	if _, err = os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined prototype left a live socket path", err)
	}
}

func TestUnixPrototypeEarlyCancelHasNoFilesystemEffect(t *testing.T) {
	home := unixPrototypeHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	key := strings.Repeat("d", 32)
	if p, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, nil }); p != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled adapter start had authority", p, err)
	}
	if _, err := os.Lstat(filepath.Join(home, "mesh", "forward-unix-"+key)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled adapter start created a path", err)
	}
}

type unixPrototypeFaultConn struct {
	net.Conn
	closeErr   error
	writeErr   error
	closeCalls atomic.Int32
	writeSeen  chan struct{}
	writeGate  <-chan struct{}
}

type unixPrototypeHeldOnCloseError struct {
	net.Conn
	closeErr   error
	closeCalls atomic.Int32
}

func (c *unixPrototypeHeldOnCloseError) Close() error {
	c.closeCalls.Add(1)
	return c.closeErr // deliberately leaves the original live for fixture cleanup
}

func (c *unixPrototypeFaultConn) Close() error {
	c.closeCalls.Add(1)
	_ = c.Conn.Close()
	return c.closeErr
}

func (c *unixPrototypeFaultConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		if c.writeSeen != nil {
			close(c.writeSeen)
			c.writeSeen = nil
		}
		if c.writeGate != nil {
			<-c.writeGate
		}
		return 0, c.writeErr
	}
	return c.Conn.Write(p)
}

func TestUnixPrototypeOriginalCloseErrorIsStickyAndRetainsPath(t *testing.T) {
	home := unixPrototypeHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	closedErr := errors.New("original remote close uncertain")
	opened := make(chan *unixPrototypeFaultConn, 1)
	p, err := StartUnixPrototype(ctx, home, strings.Repeat("e", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		fault := &unixPrototypeFaultConn{Conn: client, closeErr: closedErr}
		opened <- fault
		return fault, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var fault *unixPrototypeFaultConn
	select {
	case fault = <-opened:
	case <-time.After(time.Second):
		t.Fatal("original remote did not open")
	}
	cancel()
	if err = p.Close(); !errors.Is(err, closedErr) {
		t.Fatal("original close error not retained", err)
	}
	if again := p.Close(); !errors.Is(again, closedErr) || fault.closeCalls.Load() != 1 {
		t.Fatal("original close repeated or lost sticky result", again, fault.closeCalls.Load())
	}
	if !p.watcherJoined || !p.joined {
		t.Fatal("uncertain close returned before positive joins")
	}
	if _, err = os.Lstat(p.Path()); err != nil {
		t.Fatal("uncertain original socket was unlinked", err)
	}
	for _, fd := range []*os.File{p.dir, p.parent, p.home} {
		if _, err = fd.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("uncertain close stranded an original directory descriptor", err)
		}
	}
}

func TestUnixPrototypeCopyErrorAbortsBothOriginalDirections(t *testing.T) {
	home := unixPrototypeHome(t)
	copyErr := errors.New("injected remote write failure")
	seen := make(chan struct{})
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("f", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return &unixPrototypeFaultConn{Conn: client, writeErr: copyErr, writeSeen: seen}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if _, err = local.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("remote write failure was not exercised")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		recorded := errors.Is(p.applicationErr, copyErr)
		p.mu.Unlock()
		if recorded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote write failure did not settle into sticky outcome")
		}
		time.Sleep(time.Millisecond)
	}
	if err = p.Close(); !errors.Is(err, copyErr) {
		t.Fatal("copy failure not retained after joined abort", err)
	}
	if !p.joined || !p.watcherJoined {
		t.Fatal("copy failure left a direction or watcher unjoined")
	}
	if _, err = os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined original socket was not cleaned after application error", err)
	}
}

func TestUnixPrototypeCanceledCopyPreservesIndependentEIO(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(error) error
	}{
		{"plain", func(err error) error { return err }},
		{"mixed_join", func(err error) error { return errors.Join(err, net.ErrClosed) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := unixPrototypeHome(t)
			ctx, cancel := context.WithCancel(t.Context())
			copyErr := errors.New("independent application EIO after cancellation")
			entered := make(chan struct{})
			release := make(chan struct{})
			p, err := StartUnixPrototype(ctx, home, strings.Repeat("2", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				t.Cleanup(func() { _ = server.Close() })
				return &unixPrototypeFaultConn{Conn: client, writeErr: tc.wrap(copyErr), writeSeen: entered, writeGate: release}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			releaseWrite := sync.OnceFunc(func() { close(release) })
			t.Cleanup(func() { releaseWrite(); _ = p.Close() })
			local, err := net.DialTimeout("unix", p.Path(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			if _, err = local.Write([]byte("effect")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("application write was not entered")
			}
			cancel()
			releaseWrite() // the original writer reports EIO only after cancellation
			if err = p.Close(); !errors.Is(err, copyErr) {
				t.Fatal("cancellation erased independent application error", err)
			}
			if !p.joined || !p.watcherJoined {
				t.Fatal("independent EIO returned before original joins")
			}
			if _, err = os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("joined socket remained charged solely for application EIO", err)
			}
		})
	}
}

func TestUnixPrototypePartialAdmissionConnectionClosedOnce(t *testing.T) {
	home := unixPrototypeHome(t)
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan *unixPrototypeFaultConn, 1)
	serverSide := make(chan net.Conn, 1)
	release := make(chan struct{})
	p, err := StartUnixPrototype(ctx, home, strings.Repeat("3", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		partial := &unixPrototypeFaultConn{Conn: client}
		entered <- partial
		serverSide <- server
		<-release
		return partial, errors.New("admission failed with partial connection")
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseAdmission := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() { releaseAdmission(); _ = p.Close() })
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var partial *unixPrototypeFaultConn
	select {
	case partial = <-entered:
	case <-time.After(time.Second):
		t.Fatal("partial original did not open")
	}
	var server net.Conn
	select {
	case server = <-serverSide:
	case <-time.After(time.Second):
		t.Fatal("partial original peer did not publish")
	}
	if _, err = local.Write([]byte("never delivered")); err != nil {
		t.Fatal(err)
	}
	cancel() // watcher may close the local side before open returns
	releaseAdmission()
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if got := partial.closeCalls.Load(); got != 1 {
		t.Fatalf("partial original connection closed %d times, want once", got)
	}
	var one [1]byte
	if n, readErr := server.Read(one[:]); n != 0 || !errors.Is(readErr, io.EOF) {
		t.Fatal("application byte reached partial admission connection", n, readErr)
	}
	if _, err = os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined partial-admission path remained", err)
	}
}

func TestUnixPrototypeNamedHomeReplacementRetainsOriginalPath(t *testing.T) {
	home := unixPrototypeHome(t)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("1", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	moved := home + "-original"
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err = os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = p.Close(); err == nil {
		t.Fatal("replacement named home permitted original socket unlink")
	}
	if _, err = os.Lstat(filepath.Join(moved, "mesh", p.name, "service.sock")); err != nil {
		t.Fatal("original socket disappeared after named home replacement", err)
	}
}

func TestUnixPrototypeLostAckAndUnjoinedSameProcessPathRefuseAdoption(t *testing.T) {
	home := unixPrototypeHome(t)
	key := strings.Repeat("b", 32)
	ctx, cancel := context.WithCancel(t.Context())
	open := func(context.Context) (net.Conn, error) { return nil, errors.New("unused") }
	p, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), open)
	if err != nil {
		t.Fatal(err)
	}
	if replacement, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), open); err == nil {
		replacement.Close()
		t.Fatal("lost start reply created or adopted a second socket")
	}
	cancel()
	<-p.done // same-process original stopped without calling Close
	if replacement, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), open); err == nil {
		replacement.Close()
		t.Fatal("second same-process start adopted an unjoined Unix path")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixPrototypeChangedInodeAndCloseFailureRetainPath(t *testing.T) {
	for _, mode := range []string{"replaced_socket", "close_error"} {
		t.Run(mode, func(t *testing.T) {
			home := unixPrototypeHome(t)
			p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("c", 32), time.Now().Add(time.Minute),
				func(context.Context) (net.Conn, error) { return nil, errors.New("unused") })
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replaced_socket" {
				if err = os.Remove(p.Path()); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(p.Path(), []byte("foreign path"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				p.recordCleanup(errors.New("injected original close failure"))
			}
			if err = p.Close(); err == nil {
				t.Fatal("uncertain original identity or close silently removed path")
			}
			if _, err = os.Lstat(p.Path()); err != nil {
				t.Fatal("uncertain original path was removed", err)
			}
		})
	}
}

func TestUnixPrototypeFailedSetupKeepsOriginalOwner(t *testing.T) {
	home := unixPrototypeHome(t)
	key := strings.Repeat("d", 32)
	p, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, errors.New("unused") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	before := len(unixPrototypeRetainedOwners())
	if duplicate, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) { return nil, nil }); duplicate != nil || err == nil {
		t.Fatal("duplicate setup unexpectedly succeeded", duplicate, err)
	}
	owners := unixPrototypeRetainedOwners()
	if len(owners) != before+1 {
		t.Fatal("failed setup lost its original owner", before, len(owners))
	}
	found := false
	for _, owner := range owners {
		if owner != p && owner.key == key && owner.homePath == home && owner.cleanupErr != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("retained setup owner cannot be inspected")
	}
}

// The remote here is a net.Pipe wrapper that injects EIO without closing its
// underlying original. This is a lifecycle control, not native kernel EIO.
func TestUnixPrototypeSyntheticUnclosedRemoteRetainsPairAndWithdrawsAdmission(t *testing.T) {
	home := unixPrototypeHome(t)
	closeErr := syscall.EIO
	opened := make(chan struct {
		fault *unixPrototypeHeldOnCloseError
		peer  net.Conn
	}, 1)
	var opens atomic.Int32
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("8", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		opens.Add(1)
		client, server := net.Pipe()
		fault := &unixPrototypeHeldOnCloseError{Conn: client, closeErr: closeErr}
		opened <- struct {
			fault *unixPrototypeHeldOnCloseError
			peer  net.Conn
		}{fault, server}
		return fault, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		fault *unixPrototypeHeldOnCloseError
		peer  net.Conn
	}
	select {
	case original = <-opened:
	case <-time.After(time.Second):
		t.Fatal("held original did not open")
	}
	t.Cleanup(func() { _ = original.fault.Conn.Close(); _ = original.peer.Close() })
	_ = local.Close()
	_ = original.peer.Close() // both copy directions finish; fault.Close leaves original live
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		recorded := errors.Is(p.cleanupErr, closeErr)
		p.mu.Unlock()
		if recorded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unclosed original did not withdraw admission")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 8; i++ {
		client, err := net.DialTimeout("unix", p.Path(), 100*time.Millisecond)
		if err == nil {
			_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if n, readErr := client.Read(make([]byte, 1)); n != 0 || !errors.Is(readErr, io.EOF) {
				t.Fatal("post-EIO client remained admitted", n, readErr)
			}
			_ = client.Close()
		}
	}
	if err := p.Close(); !errors.Is(err, closeErr) {
		t.Fatal("unclosed original EIO was not retained", err)
	}
	p.mu.Lock()
	retainedPairs := len(p.active)
	p.mu.Unlock()
	if retainedPairs != 1 || opens.Load() != 1 || original.fault.closeCalls.Load() != 1 {
		t.Fatal("uncertain pair owner or admission count changed", retainedPairs, opens.Load(), original.fault.closeCalls.Load())
	}
	found := false
	for _, owner := range unixPrototypeRetainedOwners() {
		if owner == p {
			found = true
		}
	}
	if !found {
		t.Fatal("uncertain original owner was reduced to a counter")
	}
}

func TestUnixPrototypeRejectedCloseFailureKeepsOneEmergencyOwner(t *testing.T) {
	home := unixPrototypeHome(t)
	release := make(chan struct{})
	entered := make(chan struct{}, unixPrototypeMaxPairsPerAdapter)
	var opens atomic.Int32
	var fakeCloseCalls atomic.Int32
	capturedEmergency := make(chan *net.UnixConn, 1)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("6", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		opens.Add(1)
		entered <- struct{}{}
		<-release
		return nil, errors.New("admission stopped")
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseOpen := sync.OnceFunc(func() { close(release) })
	t.Cleanup(func() {
		releaseOpen()
		_ = p.Close()
		if p.emergency != nil {
			_ = p.emergency.Close() // fixture releases the deliberately held original
		}
	})
	p.mu.Lock()
	p.rejectedClose = func(original *net.UnixConn) error {
		fakeCloseCalls.Add(1)
		select {
		case capturedEmergency <- original:
		default:
		}
		return syscall.EIO // deliberately leaves the native accepted FD open
	}
	p.mu.Unlock()
	for i := 0; i < unixPrototypeMaxPairsPerAdapter; i++ {
		c, err := net.DialTimeout("unix", p.Path(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("pending opener missing")
		}
	}
	third, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		retained, closed, cleanup := p.emergency, p.closed, p.cleanupErr
		p.mu.Unlock()
		if retained != nil && closed && errors.Is(cleanup, syscall.EIO) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rejected close did not retain one emergency original and withdraw admission")
		}
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 8; i++ {
		c, err := net.DialTimeout("unix", p.Path(), 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
		}
	}
	releaseOpen()
	if err := p.Close(); !errors.Is(err, syscall.EIO) {
		t.Fatal("emergency original failure was not retained", err)
	}
	for i := 0; i < 8; i++ {
		client, err := net.DialTimeout("unix", p.Path(), 100*time.Millisecond)
		if err == nil {
			_ = client.Close()
		}
	}
	var original *net.UnixConn
	select {
	case original = <-capturedEmergency:
	default:
		t.Fatal("fake close did not capture the accepted original")
	}
	if opens.Load() != unixPrototypeMaxPairsPerAdapter || p.emergency != original || fakeCloseCalls.Load() != 1 {
		t.Fatal("post-failure admission or emergency owner changed", opens.Load(), p.emergency, fakeCloseCalls.Load())
	}
	raw, err := original.SyscallConn()
	if err != nil {
		t.Fatal("retained original was closed", err)
	}
	var nativeType int
	var nativeErr error
	if err := raw.Control(func(fd uintptr) {
		nativeType, nativeErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE)
	}); err != nil || nativeErr != nil || nativeType != unix.SOCK_STREAM {
		t.Fatal("exact retained accepted FD is no longer open after pressure", nativeType, err, nativeErr)
	}
}

func TestUnixPrototypeSequentialFailuresRecycleSlotsAndBoundOutcome(t *testing.T) {
	home := unixPrototypeHome(t)
	type openedPair struct {
		peer net.Conn
		seen <-chan struct{}
	}
	opened := make(chan openedPair, 1)
	p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("5", 32), time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		client, peer := net.Pipe()
		seen := make(chan struct{})
		opened <- openedPair{peer: peer, seen: seen}
		return &unixPrototypeFaultConn{Conn: client, writeErr: syscall.EIO, writeSeen: seen}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	unixPrototypeSlots.Lock()
	baseline := unixPrototypeSlots.pairs
	unixPrototypeSlots.Unlock()
	for i := 0; i < 64; i++ {
		client, err := net.DialTimeout("unix", p.Path(), time.Second)
		if err != nil {
			t.Fatal("clean application failure stopped admission", i, err)
		}
		t.Cleanup(func() { _ = client.Close() })
		var pair openedPair
		select {
		case pair = <-opened:
		case <-time.After(time.Second):
			t.Fatal("sequential failed original did not open", i)
		}
		t.Cleanup(func() { _ = pair.peer.Close() })
		if _, err := client.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-pair.seen:
		case <-time.After(2 * time.Second):
			t.Fatal("application error was not exercised")
		}
		_ = pair.peer.Close()
		_ = client.Close()
		deadline := time.Now().Add(2 * time.Second)
		for {
			p.mu.Lock()
			remaining := len(p.active)
			p.mu.Unlock()
			if remaining == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("clean failed pair retained a slot", i, remaining)
			}
			time.Sleep(time.Millisecond)
		}
	}
	p.mu.Lock()
	samples, omitted, outcome := p.appLedger.n, p.appLedger.omitted, p.applicationErr
	p.mu.Unlock()
	unixPrototypeSlots.Lock()
	remaining := unixPrototypeSlots.pairs
	unixPrototypeSlots.Unlock()
	if outcome == nil {
		t.Fatal("sequential application errors were not recorded")
	}
	if samples != unixPrototypeErrorSamples || omitted == 0 || len(outcome.Error()) > 2048 || remaining != baseline {
		t.Fatal("sequential errors grew storage or lost recyclable pair capacity", samples, omitted, len(outcome.Error()), baseline, remaining)
	}
	if err := p.Close(); !errors.Is(err, syscall.EIO) {
		t.Fatal("bounded outcome lost original EIO", err)
	}
}
