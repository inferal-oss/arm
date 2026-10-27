package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Direct component fixtures explicitly supply positive private authority. Real
// handlers bind the original Manager hold; these fixtures do not grant leases.
func screenPositiveCheckerContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, screenTransportFrameKey{}, &screenTransportFrame{checkActive: func(ctx context.Context) error { return ctx.Err() }})
}

type screenClockControl struct {
	tick    atomic.Int64
	refusal error
	wallEnd time.Time
	frame   *screenTransportFrame
}

func screenControlledClock(t *testing.T) (context.Context, *screenClockControl) {
	t.Helper()
	c := &screenClockControl{refusal: errors.New("controlled elapsed entitlement expired"), wallEnd: time.Now().Add(time.Hour)}
	c.tick.Store(1)
	c.frame = &screenTransportFrame{checkActive: func(context.Context) error {
		if c.tick.Load() >= 2 {
			return c.refusal
		}
		return nil
	}}
	ctx, cancel := context.WithDeadline(t.Context(), c.wallEnd)
	t.Cleanup(cancel)
	return context.WithValue(ctx, screenTransportFrameKey{}, c.frame), c
}

func (c *screenClockControl) expire(t *testing.T, ctx context.Context) {
	t.Helper()
	if ctx.Err() != nil || !c.wallEnd.After(time.Now()) {
		t.Fatal("fixture expired its Go context/calendar instead of controlled elapsed authority")
	}
	c.tick.Store(2)
}

func screenClockAwait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("original controlled screen cut did not join")
	}
}

func screenClockRelease() (chan struct{}, func()) {
	release := make(chan struct{})
	var once sync.Once
	return release, func() { once.Do(func() { close(release) }) }
}

type screenClockHijacker struct {
	http.ResponseWriter
	hijack func() (net.Conn, *bufio.ReadWriter, error)
}

func (w screenClockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return w.hijack() }

func TestScreenClockMissingOrExpiredEntitlementStartsNoNativeWork(t *testing.T) {
	for _, mode := range []string{"missing-frame", "missing-checker", "elapsed-expired"} {
		t.Run(mode, func(t *testing.T) {
			ctx, clock := screenControlledClock(t)
			switch mode {
			case "missing-frame":
				ctx = t.Context()
			case "missing-checker":
				ctx = context.WithValue(t.Context(), screenTransportFrameKey{}, &screenTransportFrame{})
			case "elapsed-expired":
				clock.expire(t, ctx)
			}
			var dials, upgrades atomic.Int32
			conn, err := screenDialTransport(ctx, "127.0.0.1:1", func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("unexpected native dial")
			})
			if err == nil || conn != nil || dials.Load() != 0 {
				t.Fatal("missing/expired entitlement started a native dial")
			}
			if screenHandshakeTransport(ctx, nil) == nil {
				t.Fatal("missing/expired entitlement reached TLS")
			}
			writer := screenClockHijacker{ResponseWriter: httptest.NewRecorder(), hijack: func() (net.Conn, *bufio.ReadWriter, error) {
				upgrades.Add(1)
				return nil, nil, errors.New("unexpected hijack")
			}}
			if _, _, err := http.NewResponseController(screenOwnedUpgradeWriter(ctx, writer)).Hijack(); err == nil || upgrades.Load() != 0 {
				t.Fatal("missing/expired entitlement reached native upgrade")
			}
			source := bytes.NewBufferString("not authorized")
			if n, err := (screenReader{ctx, source}).Read(make([]byte, 32)); n != 0 || err == nil || source.Len() != len("not authorized") {
				t.Fatal("missing/expired entitlement consumed relay input")
			}
		})
	}
}

func TestScreenClockDialRechecksAfterDelayedNativeReturn(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		peer, err := listener.Accept()
		if err != nil {
			return
		}
		defer peer.Close()
		_, _ = io.Copy(io.Discard, peer)
	}()
	entered := make(chan struct{})
	release, resume := screenClockRelease()
	defer resume()
	var closes atomic.Int32
	done := make(chan error, 1)
	go func() {
		conn, err := screenDialTransport(ctx, listener.Addr().String(), func(ctx context.Context, network, address string) (net.Conn, error) {
			original, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			close(entered)
			<-release
			return screenCloseResultConn{Conn: original, closeFn: func() error { closes.Add(1); return original.Close() }}, nil
		})
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	screenClockAwait(t, entered)
	clock.expire(t, ctx)
	resume()
	select {
	case err := <-done:
		if !errors.Is(err, clock.refusal) {
			t.Fatal("slow dial accepted expired elapsed authority", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow native dial did not return")
	}
	screenClockAwait(t, peerDone)
	if !clock.frame.closeOriginals() || closes.Load() != 1 || ctx.Err() != nil {
		t.Fatal("refused dial lost exact original close or relied on Go cancellation", closes.Load())
	}
}

func TestScreenClockTLSRechecksAfterDelayedHandshake(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	config := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	config.InsecureSkipVerify = false
	config.ServerName, _, _ = net.SplitHostPort(server.Listener.Addr().String())
	entered := make(chan struct{})
	release, resume := screenClockRelease()
	defer resume()
	verify := config.VerifyConnection
	config.VerifyConnection = func(state tls.ConnectionState) error {
		if verify != nil {
			if err := verify(state); err != nil {
				return err
			}
		}
		close(entered)
		<-release
		return nil
	}
	original, err := (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var closes atomic.Int32
	owned := screenOwnTransportConn(ctx, screenCloseResultConn{Conn: original, closeFn: func() error { closes.Add(1); return original.Close() }})
	pinned := tls.Client(owned, config)
	defer pinned.Close()
	done := make(chan error, 1)
	go func() { err := screenHandshakeTransport(ctx, pinned); _ = pinned.Close(); done <- err }()
	screenClockAwait(t, entered)
	clock.expire(t, ctx)
	resume()
	select {
	case err := <-done:
		if !errors.Is(err, clock.refusal) {
			t.Fatal("TLS completion revived expired transport authority", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controlled TLS handshake did not return")
	}
	if !clock.frame.closeOriginals() || closes.Load() != 1 || ctx.Err() != nil {
		t.Fatal("TLS refusal gated or repeated original native cleanup", closes.Load())
	}
}

func TestScreenClockUpgradeRechecksAfterDelayedHijack(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	original, peer := net.Pipe()
	defer original.Close()
	defer peer.Close()
	entered := make(chan struct{})
	release, resume := screenClockRelease()
	defer resume()
	var closes atomic.Int32
	writer := screenClockHijacker{ResponseWriter: httptest.NewRecorder(), hijack: func() (net.Conn, *bufio.ReadWriter, error) {
		close(entered)
		<-release
		return screenCloseResultConn{Conn: original, closeFn: func() error { closes.Add(1); return original.Close() }}, nil, nil
	}}
	done := make(chan error, 1)
	go func() {
		conn, _, err := http.NewResponseController(screenOwnedUpgradeWriter(ctx, writer)).Hijack()
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	screenClockAwait(t, entered)
	clock.expire(t, ctx)
	resume()
	select {
	case err := <-done:
		if !errors.Is(err, clock.refusal) {
			t.Fatal("late hijack acquired expired transport authority", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native upgrade cut did not join")
	}
	if !clock.frame.closeOriginals() || closes.Load() != 1 || ctx.Err() != nil {
		t.Fatal("upgrade refusal skipped or retried original cleanup", closes.Load())
	}
}

type screenClockDelayedRead struct {
	net.Conn
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *screenClockDelayedRead) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return n, err
}

func TestScreenClockRelayDiscardsReadCompletedAfterExpiry(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	a, client := net.Pipe()
	b, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	entered := make(chan struct{})
	release, resume := screenClockRelease()
	defer resume()
	ownedA := screenOwnTransportConn(ctx, &screenClockDelayedRead{Conn: a, entered: entered, release: release})
	ownedB := screenOwnTransportConn(ctx, b)
	done := make(chan screenCopyResult, 1)
	go func() { done <- screenCopy(ctx, ownedA, ownedB) }()
	written := make(chan error, 1)
	go func() { _, err := client.Write([]byte("withheld-after-elapsed-expiry")); written <- err }()
	screenClockAwait(t, entered)
	clock.expire(t, ctx)
	resume()
	select {
	case result := <-done:
		if result.toScreen != 0 || result.toViewer != 0 {
			t.Fatal("relay forwarded bytes from a read completed after expiry", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expiry did not join both original relay workers")
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if !clock.frame.closeOriginals() || ctx.Err() != nil {
		t.Fatal("relay cleanup relied on the still-live Go timer")
	}
}

type screenClockWriteCounter struct {
	net.Conn
	writes atomic.Int32
}

func (c *screenClockWriteCounter) Write(p []byte) (int, error) { c.writes.Add(1); return len(p), nil }

func TestScreenClockWriterRechecksAfterPacingWait(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	entered := make(chan struct{})
	check := clock.frame.checkActive
	var checks atomic.Int32
	clock.frame.checkActive = func(ctx context.Context) error {
		err := check(ctx)
		if checks.Add(1) == 1 {
			close(entered)
		}
		return err
	}
	rate := newScreenLimiter(screenInputBytesPerSecond)
	rate.mu.Lock() // Keep the pacing interval open after the first fresh check.
	var unlock sync.Once
	resume := func() { unlock.Do(rate.mu.Unlock) }
	defer resume()
	dst := &screenClockWriteCounter{}
	done := make(chan error, 1)
	go func() {
		_, err := (screenWriter{ctx, dst, rate}).Write([]byte("blocked-before-native-write"))
		done <- err
	}()
	screenClockAwait(t, entered)
	clock.expire(t, ctx)
	resume()
	select {
	case err := <-done:
		if !errors.Is(err, clock.refusal) || dst.writes.Load() != 0 {
			t.Fatal("pacing wait bypassed fresh native authority", dst.writes.Load(), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pacing cut did not join")
	}
}

func TestScreenClockExpiryCannotGateOriginalCloseAndFirstFailure(t *testing.T) {
	ctx, clock := screenControlledClock(t)
	a, peer := net.Pipe()
	defer a.Close()
	defer peer.Close()
	var closes atomic.Int32
	failure := errors.New("controlled original native close failure")
	original := screenOwnTransportConn(ctx, screenCloseResultConn{Conn: a, closeFn: func() error { closes.Add(1); return failure }})
	clock.expire(t, ctx)
	if !errors.Is(screenTransportActive(ctx), clock.refusal) {
		t.Fatal("fixture authority did not expire")
	}
	if err := original.Close(); !errors.Is(err, failure) || clock.frame.closeOriginals() || closes.Load() != 1 || ctx.Err() != nil {
		t.Fatal("expired authority gated cleanup or erased its original failure", closes.Load(), err)
	}
}

func TestScreenTransportFrameBindsCurrentManagerHold(t *testing.T) {
	s := meshFixture(t)
	startAndAwaitScreenTransportCapacity(t, s)
	ctx, finish, err := s.reserveScreenTransport(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	frame, _ := ctx.Value(screenTransportFrameKey{}).(*screenTransportFrame)
	if frame == nil || frame.checkActive == nil || frame.checkActive(context.Background()) != nil {
		t.Fatal("screen reservation did not carry its exact Manager activity check")
	}
	lease := requireScreenCharge(t, s, "active", true)
	if _, err := s.Manager.End(lease.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if frame.checkActive(context.Background()) == nil {
		t.Fatal("a fresh context replaced original Manager hold authority")
	}
	finish()
	requireScreenCharge(t, s, "terminal", false)
}
