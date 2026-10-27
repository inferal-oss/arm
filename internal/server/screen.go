package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"getarm.org/internal/mesh"
	"github.com/coder/websocket"
)

// Standard screen access is an exact service route. Neither endpoint accepts a
// host, port, path or command from the browser or peer.
const screenService = "127.0.0.1:5900"
const screenLifetime = 2 * time.Hour
const screenHandshakeTimeout = 5 * time.Second
const screenMessageBytes = 1 << 20
const screenChunkBytes = 32 << 10
const screenViewerBytesPerSecond = 8 << 20
const screenInputBytesPerSecond = 1 << 20

// NetConn disables the WebSocket read limit. Restore it only after constructing
// the adapter, on every receiving endpoint (including the outgoing mesh dial).
func screenNetConn(ctx context.Context, c *websocket.Conn) net.Conn {
	stream := websocket.NetConn(ctx, c, websocket.MessageBinary)
	c.SetReadLimit(screenMessageBytes)
	return &screenConn{Conn: stream, websocket: c}
}

type screenConn struct {
	net.Conn
	websocket *websocket.Conn
}

func (c *screenConn) Close() error {
	// Abort first so teardown cannot wait on an unresponsive close handshake.
	_ = c.websocket.CloseNow()
	return c.Conn.Close()
}

func (c *screenConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n, err := c.Conn.Write(p[:min(len(p), screenChunkBytes)])
		written += n
		p = p[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

// Cover the interval between a successful native dial and starting screenCopy,
// including an upgrade that is still in progress. Join an already running close
// callback before releasing the handler's Stop registration.
func screenCloseOnCancel(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		_ = conn.Close()
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

type screenWriter struct {
	ctx  context.Context
	dst  net.Conn
	rate *screenLimiter
}

type screenLimiter struct {
	mu     sync.Mutex
	tokens float64
	at     time.Time
	rate   float64
}

func newScreenLimiter(bytesPerSecond int) *screenLimiter {
	return &screenLimiter{tokens: 64 << 10, at: time.Now(), rate: float64(bytesPerSecond)}
}
func (l *screenLimiter) WaitN(ctx context.Context, n int) error {
	if n < 0 || n > 64<<10 {
		return fmt.Errorf("invalid screen pacing chunk")
	}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		l.mu.Lock()
		now := time.Now()
		l.tokens = math.Min(64<<10, l.tokens+now.Sub(l.at).Seconds()*l.rate)
		l.at = now
		if l.tokens >= float64(n) {
			l.tokens -= float64(n)
			l.mu.Unlock()
			return nil
		}
		delay := time.Duration(math.Ceil((float64(n) - l.tokens) / l.rate * float64(time.Second)))
		l.mu.Unlock()
		timer := time.NewTimer(max(time.Millisecond, delay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type screenPrefixReader struct {
	io.Reader
	max    int
	prefix []byte
}

type screenReader struct {
	ctx context.Context
	src io.Reader
}

func (r screenReader) Read(p []byte) (int, error) {
	if err := screenTransportActive(r.ctx); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if check := screenTransportActive(r.ctx); check != nil {
		return 0, check // Do not relay bytes returned after a slow expired read.
	}
	return n, err
}

func (r *screenPrefixReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	if len(r.prefix) < r.max {
		r.prefix = append(r.prefix, p[:min(n, r.max-len(r.prefix))]...)
	}
	return n, e
}

type screenCopyResult struct {
	toScreen, toViewer int64
	client, server     []byte
}

func screenPrelude(result screenCopyResult) string {
	version := func(p []byte) string {
		if len(p) < 12 || !strings.HasPrefix(string(p[:12]), "RFB ") || p[11] != '\n' {
			return "unknown"
		}
		return string(p[:12])
	}
	choice := "none"
	if len(result.client) >= 13 {
		choice = strconv.Itoa(int(result.client[12]))
	}
	security := "unknown"
	if len(result.server) >= 13 {
		count := int(result.server[12])
		if count <= 4 && len(result.server) >= 13+count {
			security = fmt.Sprintf("%x", result.server[13:13+count])
		}
	}
	return fmt.Sprintf("server_version=%q client_version=%q security_types=%s selected_security=%s", version(result.server), version(result.client), security, choice)
}

func (w screenWriter) Write(p []byte) (int, error) {
	if err := screenTransportActive(w.ctx); err != nil {
		return 0, err
	}
	if err := w.rate.WaitN(w.ctx, len(p)); err != nil {
		return 0, err
	}
	if err := screenTransportActive(w.ctx); err != nil {
		return 0, err
	}
	n, err := w.dst.Write(p)
	if err == nil {
		err = screenTransportActive(w.ctx)
	}
	return n, err
}

func screenCopy(ctx context.Context, a, b net.Conn) screenCopyResult {
	var wg sync.WaitGroup
	var result screenCopyResult
	if screenTransportActive(ctx) != nil {
		_ = a.Close()
		_ = b.Close()
		return result
	}
	finished, canceled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(canceled)
		select {
		case <-ctx.Done():
			_ = a.Close()
			_ = b.Close()
		case <-finished:
		}
	}()
	client, server := &screenPrefixReader{Reader: screenReader{ctx, a}, max: 13}, &screenPrefixReader{Reader: screenReader{ctx, b}, max: 17}
	// A busy framebuffer must not consume the input channel's pacing budget.
	// Independent limits keep clicks responsive even during a full-screen update.
	inputLimiter := newScreenLimiter(screenInputBytesPerSecond)
	frameLimiter := newScreenLimiter(screenViewerBytesPerSecond)
	wg.Add(2)
	go func() {
		defer wg.Done()
		result.toScreen, _ = io.CopyBuffer(screenWriter{ctx, b, inputLimiter}, struct{ io.Reader }{client}, make([]byte, screenChunkBytes))
		_ = b.Close()
		_ = a.Close()
	}()
	go func() {
		defer wg.Done()
		result.toViewer, _ = io.CopyBuffer(screenWriter{ctx, a, frameLimiter}, struct{ io.Reader }{server}, make([]byte, screenChunkBytes))
		_ = a.Close()
		_ = b.Close()
	}()
	wg.Wait()
	close(finished)
	<-canceled
	result.client, result.server = client.prefix, server.prefix
	return result
}

func (s *Server) browserScreen(w http.ResponseWriter, r *http.Request) {
	leave, err := s.beginStopWork("screen")
	if err != nil {
		http.Error(w, "backend stopping", http.StatusServiceUnavailable)
		return
	}
	defer leave()
	if r.Method != http.MethodGet || r.URL.Path != "/v1/screen" || r.URL.RawQuery != "" || !s.validHost(r.Host) {
		http.Error(w, "invalid screen request", http.StatusForbidden)
		return
	}
	admission, ok := s.authenticateAdmission(r, time.Now())
	if !ok || !admission.Browser {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if runtime.GOOS != "darwin" && (admission.Node == nil || admission.Node.Local) {
		http.Error(w, "local screen unavailable on this platform", http.StatusForbidden)
		return
	}
	s.screenOnce.Do(func() { s.screenSlots = make(chan struct{}, 8) })
	select {
	case s.screenSlots <- struct{}{}:
		defer func() { <-s.screenSlots }()
	default:
		http.Error(w, "screen session limit", http.StatusTooManyRequests)
		return
	}
	deadline := time.Now().Add(screenLifetime)
	if admission.Expires.Before(deadline) {
		deadline = admission.Expires
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	var service *meshService
	host := "local"
	if admission.Node != nil && !admission.Node.Local {
		bound := *admission.Node
		host = bound.Host
		var e error
		service, e = s.validateNodeSession(bound)
		if e != nil {
			http.Error(w, "node context changed", http.StatusForbidden)
			return
		}
	}
	ctx, finish, err := s.reserveScreenTransport(ctx)
	if err != nil {
		log.Printf("ARM screen: transport admission refused: %v", err)
		http.Error(w, "screen transport resources unavailable", http.StatusServiceUnavailable)
		return
	}
	defer finish() // Runs after every original native close and worker join below.
	var target net.Conn
	if service != nil {
		target, err = service.dialScreen(ctx, *admission.Node)
		if err != nil {
			log.Printf("ARM screen: mesh dial rejected host=%s: %v", host, err)
			http.Error(w, "screen route unavailable", http.StatusBadGateway)
			return
		}
	} else {
		target, err = screenDialTransport(ctx, screenService, (&net.Dialer{Timeout: 3 * time.Second}).DialContext)
		if err != nil {
			log.Printf("ARM screen: local service unavailable: %v", err)
			http.Error(w, "macOS Screen Sharing is unavailable", http.StatusBadGateway)
			return
		}
	}
	defer target.Close()
	defer screenCloseOnCancel(ctx, target)()
	if screenTransportActive(ctx) != nil {
		http.Error(w, "screen transport no longer active", http.StatusServiceUnavailable)
		return
	}
	c, err := websocket.Accept(screenOwnedUpgradeWriter(ctx, w), r, &websocket.AcceptOptions{Subprotocols: []string{"arm.v1"}})
	if err != nil {
		return
	}
	defer c.CloseNow()
	if screenTransportActive(ctx) != nil {
		return
	}
	viewer := screenNetConn(ctx, c)
	defer viewer.Close()
	var watchDone <-chan struct{}
	if admission.Node != nil && !admission.Node.Local {
		bound := *admission.Node
		done := make(chan struct{})
		watchDone = done
		go func() {
			defer close(done)
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, e := s.validateNodeSession(bound); e != nil {
						log.Printf("ARM screen: source context changed host=%s", bound.Host)
						cancel()
						return
					}
				}
			}
		}()
	}
	defer func() {
		cancel()
		if watchDone != nil {
			<-watchDone
		}
	}()
	log.Printf("ARM screen: opened host=%s mode=standard", host)
	result := screenCopy(ctx, viewer, target)
	log.Printf("ARM screen: closed host=%s to_screen=%d to_viewer=%d %s", host, result.toScreen, result.toViewer, screenPrelude(result))
}

func (m *meshService) dialScreen(ctx context.Context, bound nodeSession) (net.Conn, error) {
	if err := screenTransportActive(ctx); err != nil {
		return nil, err
	}
	peer, err := m.store.Resolve(bound.Host)
	if err != nil {
		return nil, err
	}
	if peer.State != "active" || peer.Pair != bound.Pair || peer.Revision != bound.LocalRevision || peer.Identity.Fingerprint != bound.Pin {
		return nil, fmt.Errorf("screen enrollment changed")
	}
	tlsConfig, err := m.store.ClientTLS(peer)
	if err != nil {
		return nil, err
	}
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if state.NegotiatedProtocol != "http/1.1" || len(state.PeerCertificates) != 1 {
			log.Printf("ARM screen: mesh TLS refused alpn=%q peer_certs=%d", state.NegotiatedProtocol, len(state.PeerCertificates))
		}
		return verify(state)
	}
	header := http.Header{}
	header.Set("Arm-Screen-Pair", bound.Pair)
	header.Set("Arm-Screen-Revision", strconv.Itoa(bound.RemoteRevision))
	handshake, cancel := context.WithTimeout(ctx, screenHandshakeTimeout)
	defer cancel()
	// Own the complete native dial/handshake here. http.Transport can detach
	// its dial context from a canceled request; no native dial is delegated to
	// that worker or allowed to outlive this admission interval.
	native, err := screenDialTransport(handshake, peer.Identity.Address, (&net.Dialer{}).DialContext)
	if err != nil {
		return nil, err
	}
	// Retain the first result of the exact original TCP close, including
	// cancellation during TLS or a refused HTTP upgrade. TLS stays pinned and
	// the HTTP transport still receives the same concrete *tls.Conn.
	pinned := tls.Client(native, tlsConfig)
	transferred := false
	defer func() {
		if !transferred {
			_ = pinned.Close()
		}
	}()
	defer screenCloseOnCancel(handshake, pinned)()
	if err = screenHandshakeTransport(handshake, pinned); err != nil {
		return nil, err
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: screenHandshakeTimeout, ResponseHeaderTimeout: screenHandshakeTimeout, MaxResponseHeaderBytes: 8192}
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("screen requires the pinned TLS endpoint")
	}
	// Hand the exact socket to HTTP once. The explicit handshake above also
	// preserves the mesh's required http/1.1 ALPN on Upgrade requests.
	var socketMu sync.Mutex
	used := false
	transport.DialTLSContext = func(_ context.Context, network, address string) (net.Conn, error) {
		socketMu.Lock()
		defer socketMu.Unlock()
		if network != "tcp" || address != peer.Identity.Address || used {
			return nil, fmt.Errorf("screen dial target changed")
		}
		if err := screenTransportActive(handshake); err != nil {
			return nil, err
		}
		used = true
		return pinned, nil
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := screenTransportActive(handshake); err != nil {
		return nil, err
	}
	c, response, err := websocket.Dial(handshake, "wss://"+peer.Identity.Address+"/v1/screen", &websocket.DialOptions{HTTPClient: client, HTTPHeader: header, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("destination returned HTTP %d: %w", response.StatusCode, err)
		}
		return nil, err
	}
	if err := screenTransportActive(ctx); err != nil {
		_ = c.CloseNow()
		return nil, err
	}
	// NetConn owns the upgraded socket. Its close releases the HTTP transport.
	transferred = true
	return screenNetConn(ctx, c), nil
}

func (m *meshService) meshScreen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || r.Header.Get("Origin") != "" || runtime.GOOS != "darwin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	select {
	case m.screenSlots <- struct{}{}:
		defer func() { <-m.screenSlots }()
	default:
		http.Error(w, "screen session limit", http.StatusTooManyRequests)
		return
	}
	peer, err := m.store.PeerByCert(r.TLS.PeerCertificates[0].Raw)
	if err != nil {
		http.Error(w, "untrusted peer", http.StatusForbidden)
		return
	}
	name := peer.Identity.Host
	// Register before the grant check/native dial. Sealing mesh admission and
	// registering a screen share the same mutex, so close/revoke cannot miss a
	// session that is still dialing or upgrading rather than already hijacked.
	ctx, cancel, err := m.registerScreen(r.Context(), name)
	if err != nil {
		http.Error(w, "mesh closing", http.StatusServiceUnavailable)
		return
	}
	defer cancel()
	pair, revision := r.Header.Get("Arm-Screen-Pair"), r.Header.Get("Arm-Screen-Revision")
	check := func() error {
		_, e := m.store.WithPeer(name, func(current mesh.Peer) (any, error) {
			if current.State != "active" || !current.Proven || !current.Grant.Has("screen-view") || current.Identity.Fingerprint != peer.Identity.Fingerprint || current.Pair != pair || strconv.Itoa(current.Revision) != revision {
				return nil, fmt.Errorf("screen grant or enrollment changed")
			}
			return nil, nil
		})
		return e
	}
	if err = check(); err != nil {
		http.Error(w, "screen grant denied", http.StatusForbidden)
		return
	}
	ctx, finish, err := m.owner.reserveScreenTransport(ctx)
	if err != nil {
		log.Printf("ARM screen: destination transport admission refused: %v", err)
		http.Error(w, "screen transport resources unavailable", http.StatusServiceUnavailable)
		return
	}
	defer finish() // Grant/lifetime cancellation alone cannot certify reclamation.
	// Resource admission may wait on native observation or the ledger. It is
	// not screen authority, so recheck the original grant after that interval.
	if err = check(); err != nil {
		http.Error(w, "screen grant changed", http.StatusForbidden)
		return
	}
	target, err := screenDialTransport(ctx, screenService, (&net.Dialer{Timeout: 3 * time.Second}).DialContext)
	if err != nil {
		log.Printf("ARM screen: destination service unavailable: %v", err)
		http.Error(w, "macOS Screen Sharing unavailable", http.StatusBadGateway)
		return
	}
	defer target.Close()
	defer screenCloseOnCancel(ctx, target)()
	if screenTransportActive(ctx) != nil {
		http.Error(w, "screen transport no longer active", http.StatusServiceUnavailable)
		return
	}
	c, err := websocket.Accept(screenOwnedUpgradeWriter(ctx, w), r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()
	if screenTransportActive(ctx) != nil {
		return
	}
	viewer := screenNetConn(ctx, c)
	defer viewer.Close()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = c.CloseNow()
				return
			case <-ticker.C:
				if check() != nil {
					log.Printf("ARM screen: peer grant changed host=%s", name)
					_ = c.CloseNow()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	log.Printf("ARM screen: destination opened source=%s mode=standard", name)
	result := screenCopy(ctx, viewer, target)
	log.Printf("ARM screen: destination closed source=%s to_screen=%d to_viewer=%d %s", name, result.toScreen, result.toViewer, screenPrelude(result))
}

type screenSession struct {
	host   string
	cancel context.CancelFunc
}

func (m *meshService) registerScreen(parent context.Context, host string) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(parent, screenLifetime)
	session := &screenSession{host: host, cancel: cancel}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sealed {
		cancel()
		return nil, nil, fmt.Errorf("mesh admission closed")
	}
	if m.screens == nil {
		m.screens = make(map[*screenSession]struct{})
	}
	m.screens[session] = struct{}{}
	return ctx, func() {
		cancel()
		m.mu.Lock()
		delete(m.screens, session)
		m.mu.Unlock()
	}, nil
}

func (m *meshService) closeScreenStreams(host string) {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for session := range m.screens {
		if host == "" || session.host == host {
			cancels = append(cancels, session.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}
