package server

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/inferal-oss/arm/internal/mesh"
)

// These fixtures own only ephemeral loopback HTTP servers. They never contact
// the real Mac Screen Sharing port or the author's acceptance environment.
func screenPairFixture(t *testing.T, handler http.Handler) (*Server, nodeSession) {
	t.Helper()
	destination := httptest.NewUnstartedServer(handler)
	t.Cleanup(destination.Close)
	s, bound, right := screenPairAt(t, destination.Listener.Addr().String())
	destination.TLS = right.ServerTLS()
	destination.StartTLS()
	return s, bound
}

func screenPairAt(t *testing.T, address string) (*Server, nodeSession, *mesh.Store) {
	t.Helper()
	s := meshFixture(t)
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	left, err := mesh.Init(s.home, status.Host, "review-source", meshAddress(t), meshAddress(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { left.Close() })
	right, err := mesh.Init(filepath.Join(t.TempDir(), "peer"), strings.Repeat("b", 32), "review-destination", address, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { right.Close() })
	pair := strings.Repeat("a", 64)
	grant := mesh.Grant{Permissions: []string{"observe-host", "screen-view"}}
	for _, entry := range []struct {
		store *mesh.Store
		peer  mesh.Identity
	}{{left, right.Config().Identity}, {right, left.Config().Identity}} {
		if _, err := entry.store.PairReviewed(entry.peer, grant, pair, "isolated review", false); err != nil {
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
	return s, nodeSession{Host: peer.Identity.Host, Source: status.Host, Pair: pair, Pin: peer.Identity.Fingerprint, LocalRevision: peer.Revision, RemoteRevision: 1}, right
}

func TestScreenTLSCancellationClosesNativeHandshakeBeforeReturning(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s, bound, _ := screenPairAt(t, listener.Addr().String())
	accepted, closed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(closed)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		// Deliberately never answer the TLS ClientHello.
		_, _ = io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := s.mesh.dialScreen(ctx, bound)
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("native TLS dial did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("TLS cancellation lost", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TLS dial retained a canceled native interval")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("dial returned before its native TLS socket closed")
	}
}

func TestBrowserScreenEnforcesInputMessageLimit(t *testing.T) {
	forwarded := make(chan int64, 1)
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			forwarded <- -1
			return
		}
		defer c.CloseNow()
		// The destination fixture deliberately accepts any message size, so
		// only the real browser handler can enforce this test's input bound.
		stream := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
		n, _ := io.Copy(io.Discard, stream)
		forwarded <- n
	}))
	awaitScreenTransportCapacity(t, s)
	joined := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(joined)
		s.browserScreen(w, r)
	}))
	s.authority = server.Listener.Addr().String()
	token := strings.Repeat("c", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+s.authority+"/v1/screen", &websocket.DialOptions{Subprotocols: []string{"arm.v1", "arm.auth." + token}, HTTPHeader: http.Header{"Origin": {"http://" + s.authority}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_ = c.Write(ctx, websocket.MessageBinary, make([]byte, 2*screenMessageBytes))
	select {
	case n := <-forwarded:
		if n < 0 || n > screenMessageBytes+1 {
			t.Fatal("browser forwarded an unbounded message", n)
		}
	case <-ctx.Done():
		t.Fatal("oversized browser message did not close the route")
	}
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatal("browser screen handler failed to join")
	}
	s.nodeStopMu.Lock()
	defer s.nodeStopMu.Unlock()
	if s.nodeStopCalls["screen"] != 0 {
		t.Fatal("joined screen retained native work admission")
	}
}

func TestScreenDialRejectsPlaintextRedirect(t *testing.T) {
	var plaintextRequests atomic.Int32
	plaintext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plaintextRequests.Add(1)
		http.Error(w, "not an authorized screen endpoint", http.StatusForbidden)
	}))
	defer plaintext.Close()
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plaintext.URL+"/unreviewed-endpoint", http.StatusFound)
	}))
	conn, _ := s.mesh.dialScreen(t.Context(), bound)
	if conn != nil {
		conn.Close()
	}
	if n := plaintextRequests.Load(); n != 0 {
		t.Fatalf("pinned screen route followed redirect to plaintext unreviewed endpoint: %d requests", n)
	}
}

func TestScreenStopPreventsNewRemoteDial(t *testing.T) {
	var destinationRequests atomic.Int32
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationRequests.Add(1)
		http.Error(w, "review destination refuses screen upgrade", http.StatusForbidden)
	}))
	s.nodeStopClosed = true
	s.authority = "127.0.0.1:1234"
	token := strings.Repeat("c", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	r := httptest.NewRequest(http.MethodGet, "http://"+s.authority+"/v1/screen", nil)
	r.Header.Set("Origin", "http://"+s.authority)
	r.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token)
	w := httptest.NewRecorder()
	s.browserScreen(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped screen admission returned HTTP %d", w.Code)
	}
	if n := destinationRequests.Load(); n != 0 {
		t.Fatalf("durably stopped backend started a new screen route: %d requests, HTTP %d", n, w.Code)
	}
}

func TestScreenDestinationStopRejectsBeforeNativeAdmission(t *testing.T) {
	for _, closing := range []bool{false, true} {
		s := &Server{nodeStopClosed: !closing, nodeStopClosing: closing}
		m := &meshService{owner: s}
		w := httptest.NewRecorder()
		m.serve(w, httptest.NewRequest(http.MethodGet, "https://127.0.0.1:1234/v1/screen", nil))
		if w.Code != http.StatusServiceUnavailable || m.requests != 0 || len(m.screens) != 0 {
			t.Fatal("destination admitted native screen work during Stop", closing, w.Code)
		}
		if s.nodeStopCalls["mesh-stream"] != 0 || s.nodeStopCalls["inspection"] != 0 {
			t.Fatal("refused destination request did not join")
		}
	}
}

func TestScreenNetConnPreservesMessageLimit(t *testing.T) {
	type outcome struct {
		bytes int64
		err   error
	}
	result := make(chan outcome, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			result <- outcome{err: err}
			return
		}
		defer c.CloseNow()
		stream := screenNetConn(r.Context(), c)
		defer stream.Close()
		n, err := io.Copy(io.Discard, stream)
		result <- outcome{n, err}
	}))
	defer server.Close()
	c, _, err := websocket.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_ = c.Write(t.Context(), websocket.MessageBinary, make([]byte, 2<<20))
	_ = c.Close(websocket.StatusNormalClosure, "done")
	got := <-result
	// The streaming library may expose its one-byte EOF lookahead before
	// rejecting an oversized message. It must never consume the full payload.
	if got.bytes > screenMessageBytes+1 || !errors.Is(got.err, websocket.ErrMessageTooBig) {
		t.Fatalf("screen stream consumed %d bytes, error %v", got.bytes, got.err)
	}
}

func TestScreenDialRejectsEveryRedirectWithoutSecondRequest(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/v1/screen" {
					http.Error(w, "redirect followed", http.StatusForbidden)
					return
				}
				// Even the same pinned origin cannot redirect the reviewed path.
				http.Redirect(w, r, "/unreviewed", status)
			}))
			conn, err := s.mesh.dialScreen(t.Context(), bound)
			if conn != nil {
				conn.Close()
			}
			if err == nil || requests.Load() != 1 {
				t.Fatal("redirect was followed", requests.Load(), err)
			}
		})
	}
}

func TestScreenDialPinnedUpgradeAndReceiveLimit(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "oversized"}[oversized], func(t *testing.T) {
			var requestOK atomic.Bool
			s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestOK.Store(r.URL.Path == "/v1/screen" && r.URL.RawQuery == "" && r.Header.Get("Origin") == "" && r.Header.Get("Arm-Screen-Pair") == strings.Repeat("a", 64) && r.Header.Get("Arm-Screen-Revision") == "1" && len(r.TLS.PeerCertificates) == 1)
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				if oversized {
					_ = c.Write(r.Context(), websocket.MessageBinary, make([]byte, 2*screenMessageBytes))
				} else {
					_ = c.Write(r.Context(), websocket.MessageBinary, []byte("RFB 003.889\n"))
				}
				// Keep the socket alive until the client closes/cancels it.
				_, _, _ = c.Read(r.Context())
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			conn, err := s.mesh.dialScreen(ctx, bound)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if oversized {
				n, err := io.Copy(io.Discard, conn)
				if n > screenMessageBytes+1 || !errors.Is(err, websocket.ErrMessageTooBig) {
					t.Fatal("outgoing mesh receive limit missing", n, err)
				}
			} else {
				banner := make([]byte, 12)
				if _, err = io.ReadFull(conn, banner); err != nil || string(banner) != "RFB 003.889\n" {
					t.Fatal("pinned upgrade did not preserve stream", string(banner), err)
				}
			}
			if !requestOK.Load() {
				t.Fatal("dial did not use exact authenticated screen route")
			}
		})
	}
}

func TestScreenNativeDialIntervalIsRegisteredUntilCanceledAndJoined(t *testing.T) {
	entered := make(chan struct{})
	s, bound := screenPairFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	awaitScreenTransportCapacity(t, s)
	s.authority = "127.0.0.1:1234"
	token := strings.Repeat("c", 64)
	s.browser.nodeSessions = map[[32]byte]nodeCredential{sha256.Sum256([]byte(token)): {Expires: time.Now().Add(time.Minute), Context: bound}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "http://"+s.authority+"/v1/screen", nil).WithContext(ctx)
	r.Header.Set("Origin", "http://"+s.authority)
	r.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token)
	done := make(chan struct{})
	go func() { defer close(done); s.browserScreen(httptest.NewRecorder(), r) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("screen handshake did not start")
	}
	s.nodeStopMu.Lock()
	count := s.nodeStopCalls["screen"]
	s.nodeStopClosing = true
	s.nodeStopMu.Unlock()
	if count != 1 {
		t.Fatal("native screen dial absent from Stop census", count)
	}
	joined := make(chan struct{})
	go func() { s.nodeStopCallsWG.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("Stop joined before the screen dial returned")
	default:
	}
	cancel()
	for _, wait := range []<-chan struct{}{done, joined} {
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			t.Fatal("canceled native screen dial did not join")
		}
	}
}

func TestScreenCopyCancellationJoinsBlockedIO(t *testing.T) {
	for _, blockedWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "read", true: "write"}[blockedWrite], func(t *testing.T) {
			a, client := net.Pipe()
			b, native := net.Pipe()
			defer client.Close()
			defer native.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); screenCopy(ctx, a, b) }()
			if blockedWrite {
				// Copy has consumed this byte and now waits for an unread native
				// socket; cancellation must join that write as well as both reads.
				if _, err := client.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("screen copy retained blocked native IO")
			}
		})
	}
}

func TestScreenRegistrySealAndRevocationCoverPreUpgradeSessions(t *testing.T) {
	m := &meshService{}
	a, leaveA, err := m.registerScreen(t.Context(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer leaveA()
	b, leaveB, err := m.registerScreen(t.Context(), "b")
	if err != nil {
		t.Fatal(err)
	}
	defer leaveB()
	m.closeScreenStreams("a")
	if a.Err() == nil || b.Err() != nil {
		t.Fatal("peer revocation did not cancel exactly its pending screen")
	}
	m.sealRequests()
	m.closeScreenStreams("")
	if b.Err() == nil {
		t.Fatal("mesh close missed a pre-upgrade screen")
	}
	if _, leave, err := m.registerScreen(t.Context(), "late"); err == nil {
		leave()
		t.Fatal("screen registered after mesh seal")
	}
	for i := 0; i < 100; i++ {
		m := &meshService{}
		var wg sync.WaitGroup
		var registered context.Context
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, leave, err := m.registerScreen(t.Context(), "race")
			if err == nil {
				registered = ctx
				defer leave()
				<-ctx.Done()
			}
		}()
		go func() { defer wg.Done(); m.sealRequests(); m.closeScreenStreams("") }()
		wg.Wait()
		if registered != nil && registered.Err() == nil {
			t.Fatal("close missed a concurrent registration")
		}
	}
}

func TestScreenNetConnSplitsWritesAndAcceptsExactLimit(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, screenMessageBytes)
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			result <- err
			return
		}
		stream := screenNetConn(r.Context(), c)
		defer stream.Close()
		got := make([]byte, len(payload)+1)
		if _, err = io.ReadFull(stream, got); err == nil && (!bytes.Equal(got[:len(payload)], payload) || got[len(payload)] != '!') {
			err = errors.New("exact-limit message corrupted")
		}
		if err == nil {
			_, err = stream.Write(payload)
		}
		result <- err
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err = c.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatal(err)
	}
	if err = c.Write(ctx, websocket.MessageBinary, []byte{'!'}); err != nil {
		t.Fatal(err)
	}
	for read := 0; read < len(payload); {
		kind, p, err := c.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || len(p) > screenChunkBytes || len(p) == 0 {
			t.Fatal("unbounded or invalid screen output message", kind, len(p), err)
		}
		read += len(p)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}
