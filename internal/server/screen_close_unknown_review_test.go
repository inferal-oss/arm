package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
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
	s, bound, closeManager := screenCloseResourceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	awaitScreenTransportCapacity(t, s)
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

	closed := make(chan error, 1)
	go func() { closed <- closeManager() }()
	select {
	case err = <-closed:
		var detail *model.Error
		if err != nil && (!errors.As(err, &detail) || detail.Code != "needs_attention") {
			t.Fatal("Manager.Close failed outside retained cleanup", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Manager.Close did not return after actual screen workers joined")
	}
	if err = original.Conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatal("Manager.Close replaced the original close witness", err)
	}
	reopened, err := manager.Open(s.home, model.Resources{CPU: 2, Disk: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retained, err := reopened.Inspect(leases[0].ID)
	if err != nil || retained.State != "needs_attention" || retained.Ended != nil {
		t.Fatal("restart erased native cleanup uncertainty", retained, err)
	}
	requireScreenCharge(t, &Server{Manager: reopened}, "needs_attention", true)
	if _, err = reopened.Reconcile(retained.ID); err == nil {
		t.Fatal("restart adopted an original screen with no close witness")
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

func screenCloseResourceFixture(t *testing.T, handler http.Handler) (*Server, nodeSession, func() error) {
	t.Helper()
	destination := httptest.NewUnstartedServer(handler)
	t.Cleanup(destination.Close)
	home := filepath.Join(t.TempDir(), "state")
	m, err := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var closeErr error
	closeManager := func() error { once.Do(func() { closeErr = m.Close() }); return closeErr }
	t.Cleanup(func() { _ = closeManager() })
	s := &Server{Manager: m, home: home, lifecycleContext: context.Background()}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	left, err := mesh.Init(home, status.Host, "close-source", meshAddress(t), meshAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = left.Close() })
	right, err := mesh.Init(filepath.Join(t.TempDir(), "peer"), strings.Repeat("b", 32), "close-destination", destination.Listener.Addr().String(), destination.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = right.Close() })
	pair := strings.Repeat("a", 64)
	grant := mesh.Grant{Permissions: []string{"observe-host", "screen-view"}}
	for _, entry := range []struct {
		store *mesh.Store
		peer  mesh.Identity
	}{{left, right.Config().Identity}, {right, left.Config().Identity}} {
		if _, err := entry.store.PairReviewed(entry.peer, grant, pair, "isolated screen close fixture", false); err != nil {
			t.Fatal(err)
		}
		if err := entry.store.Prove(entry.peer.Host, pair); err != nil {
			t.Fatal(err)
		}
		if err := entry.store.Activate(entry.peer.Host, pair); err != nil {
			t.Fatal(err)
		}
	}
	s.mesh = &meshService{owner: s, store: left}
	peer, err := left.Resolve(right.Config().Identity.Host)
	if err != nil {
		t.Fatal(err)
	}
	destination.TLS = right.ServerTLS()
	destination.StartTLS()
	return s, nodeSession{Host: peer.Identity.Host, Source: status.Host, Pair: pair, Pin: peer.Identity.Fingerprint, LocalRevision: peer.Revision, RemoteRevision: 1}, closeManager
}

func TestScreenTLSAbortClosesOriginalNativeSocketOnlyOnce(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	var calls atomic.Int32
	failure := errors.New("original TLS socket close is unconfirmed")
	native := screenCloseResultConn{Conn: a, closeFn: func() error { calls.Add(1); return failure }}
	secured := tls.Client(native, &tls.Config{})
	frame := &screenTransportFrame{}
	ctx := context.WithValue(t.Context(), screenTransportFrameKey{}, frame)
	owned := screenOwnTransportConn(ctx, secured)
	if err := owned.Close(); !errors.Is(err, failure) {
		t.Fatal("TLS abort hid original socket result", err)
	}
	if frame.closeOriginals() || calls.Load() != 1 {
		t.Fatal("TLS abort retried or certified an unconfirmed native close", calls.Load())
	}
}
