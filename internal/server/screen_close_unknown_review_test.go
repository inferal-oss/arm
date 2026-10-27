package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type screenUnknownCloseConn struct {
	net.Conn
	attempts atomic.Int32
}

func (c *screenUnknownCloseConn) Close() error {
	c.attempts.Add(1)
	return errors.New("injected original TCP close result is unconfirmed")
}

type screenUnknownCloseListener struct {
	net.Listener
	seen chan *screenUnknownCloseConn
	t    *testing.T
}

func (l *screenUnknownCloseListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.t.Cleanup(func() { _ = c.Close() })
	wrapped := &screenUnknownCloseConn{Conn: c}
	l.seen <- wrapped
	return wrapped, nil
}

func TestScreenResourceUnknownNativeCloseRetainsCharge(t *testing.T) {
	destinationJoined := make(chan struct{})
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(destinationJoined)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		stream := screenNetConn(r.Context(), c)
		defer stream.Close()
		_, _ = io.Copy(stream, stream)
	}))
	joined := make(chan struct{})
	front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(joined); s.browserScreen(w, r) }))
	seen := make(chan *screenUnknownCloseConn, 1)
	front.Listener = &screenUnknownCloseListener{Listener: front.Listener, seen: seen, t: t}
	s.authority = front.Listener.Addr().String()
	token := strings.Repeat("e", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	front.Start()
	t.Cleanup(front.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	snapshot := func(phase string) {
		status, se := s.Manager.Status()
		policy, pe := s.Manager.BudgetInspect("self")
		data, _ := json.Marshal(map[string]any{"phase": phase, "cpu": status.CPU, "memory": status.Memory, "reserved": status.Reserved, "policy_limits": policy.Budget.Limits, "policy_available": policy.Available, "status_ok": se == nil, "policy_ok": pe == nil})
		t.Logf("native close probe actual resource snapshot: %s", data)
	}
	snapshot("before-real-browser-admission")
	client, err := dialScreenBrowser(ctx, front, token)
	snapshot("after-real-browser-admission")
	if err != nil {
		t.Fatal("close-error precondition did not obtain a real screen transport", err)
	}
	defer client.CloseNow()
	original := <-seen
	message := []byte("close-result-owned-transport-fixture")
	if err = client.Write(ctx, websocket.MessageBinary, message); err != nil {
		t.Fatal(err)
	}
	_, echoed, err := client.Read(ctx)
	if err != nil || string(echoed) != string(message) {
		t.Fatal("close-error precondition did not carry application bytes", err)
	}
	requireScreenCharge(t, s, "active", true)
	_ = client.CloseNow()
	for _, done := range []<-chan struct{}{joined, destinationJoined} {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("close-error handler did not return", ctx.Err())
		}
	}
	// An open original TCP descriptor can still accept a deadline. This fixture
	// closes it during cleanup, after checking the actual handler's resource row.
	if original.attempts.Load() == 0 {
		t.Fatal("injected close error was never exercised")
	}
	if err = original.Conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatal("original TCP descriptor was already confirmed closed", err)
	}
	status, leases := screenTransportLeases(t, s)
	if len(leases) != 1 {
		t.Fatal("missing exact screen allocation")
	}
	if leases[0].State != "needs_attention" || leases[0].Ended != nil || leases[0].Outcome != "interrupted" || status.Reserved.CPU != screenTransportCPU || status.Reserved.Memory != screenTransportMemory {
		t.Fatalf("COUNTEREXAMPLE: original native TCP close remains unconfirmed and descriptor open, but actual handler settled screen charge: state=%s CPU=%g memory=%d", leases[0].State, status.Reserved.CPU, status.Reserved.Memory)
	}
}

type screenCloseResultConn struct {
	net.Conn
	closeFn func() error
}

func (c screenCloseResultConn) Close() error { return c.closeFn() }

// These two component controls exercise the close recorder; the handler case
// above separately requires actual native .05CPU/8MiB admission and TCP bytes.
func TestScreenOriginalCloseKeepsFirstFailureAcrossConcurrentCalls(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	failure := errors.New("original close failed")
	frame := &screenTransportFrame{}
	ctx := context.WithValue(t.Context(), screenTransportFrameKey{}, frame)
	original := screenOwnTransportConn(ctx, screenCloseResultConn{Conn: a, closeFn: func() error {
		if calls.Add(1) > 1 {
			return nil
		}
		close(entered)
		<-release
		return failure
	}})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- original.Close() }()
	<-entered
	go func() { second <- original.Close() }()
	select {
	case err := <-second:
		close(release)
		t.Fatal("later close escaped the original active close", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	for _, done := range []<-chan error{first, second} {
		if err := <-done; !errors.Is(err, failure) {
			t.Fatal("later close masked the original error", err)
		}
	}
	if frame.closeOriginals() || calls.Load() != 1 {
		t.Fatal("frame retried or certified a failed original close", calls.Load())
	}
}

func TestScreenOriginalCloseMissingReturnCannotBecomeSuccess(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	frame := &screenTransportFrame{}
	ctx := context.WithValue(t.Context(), screenTransportFrameKey{}, frame)
	original := screenOwnTransportConn(ctx, screenCloseResultConn{Conn: a, closeFn: func() error { panic("injected missing native close result") }})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("close did not exercise the injected missing return")
			}
		}()
		_ = original.Close()
	}()
	if frame.closeOriginals() {
		t.Fatal("missing original close result became successful reclamation")
	}
}
