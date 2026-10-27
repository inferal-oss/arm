//go:build arm_forward_review && arm_forward_unix_prototype && (darwin || linux)

package meshforward

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func unixPrototypeHome(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "fup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
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
		<-admit // models the existing pinned TLS, token and READY exchange
		client, server := net.Pipe()
		remoteReady <- server
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
	close(admit)
	remote := <-remoteReady
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
	fault := <-opened
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
			close(release) // the original writer reports EIO only after cancellation
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
	local, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	partial := <-entered
	server := <-serverSide
	if _, err = local.Write([]byte("never delivered")); err != nil {
		t.Fatal(err)
	}
	cancel() // watcher may close the local side before open returns
	close(release)
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

func TestUnixPrototypeLostAckAndUnjoinedRestartRefuseAdoption(t *testing.T) {
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
	<-p.done // original process lost before publishing cleanup evidence
	if replacement, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), open); err == nil {
		replacement.Close()
		t.Fatal("unjoined restart adopted an old Unix path")
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
