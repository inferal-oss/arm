package meshstream

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/inferal-oss/arm/internal/mesh"
)

type Limits struct {
	Global, PerPeer           int
	Handshake, Idle, Lifetime time.Duration
	BytesPerDirection         int64
}

func DefaultLimits() Limits {
	return Limits{8, 2, 5 * time.Second, 5 * time.Minute, 7 * 24 * time.Hour, 1 << 30}
}
func (l Limits) validate() error {
	if l.Global < 1 || l.Global > 64 || l.PerPeer < 1 || l.PerPeer > l.Global || l.Handshake <= 0 || l.Handshake > 30*time.Second || l.Idle <= 0 || l.Idle > time.Hour || l.Lifetime <= 0 || l.Lifetime > 7*24*time.Hour || l.BytesPerDirection < 1 || l.BytesPerDirection > 1<<40 {
		return fmt.Errorf("invalid stream bounds")
	}
	return nil
}

// Handler requires destination-owned authentication and a fresh authorization
// check. Handle must return when ctx is cancelled and must not infer execution
// permission from the transport permit alone.
type Handler struct {
	Authorize  func(context.Context, []byte) (Scope, error)
	Revalidate func(context.Context, Scope) error
	Handle     func(context.Context, Scope, Control, net.Conn)
	Limits     Limits
	mu         sync.Mutex
	streams    map[*boundedConn]Scope
	counts     map[string]int
	pending    int
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deny := func(code int) { http.Error(w, "SSH stream unavailable", code) }
	if r.Method != "GET" || r.URL.Path != Path || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.Header.Get("Origin") != "" || len(r.Header.Values("Origin")) != 0 || r.Header.Get("Authorization") != "" || len(r.Header.Values("Cookie")) != 0 || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || r.TLS.DidResume || len(r.TLS.PeerCertificates) != 1 || len(r.TLS.VerifiedChains) == 0 || r.TLS.NegotiatedProtocol != "http/1.1" || h.Authorize == nil || h.Revalidate == nil || h.Handle == nil || h.Limits.validate() != nil {
		deny(http.StatusForbidden)
		return
	}
	offered := r.Header.Values("Sec-Websocket-Protocol")
	if len(offered) != 1 || strings.TrimSpace(offered[0]) != Protocol {
		deny(http.StatusBadRequest)
		return
	}
	scope, err := h.Authorize(r.Context(), r.TLS.PeerCertificates[0].Raw)
	certificateHash := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	if err != nil || scope.Certificate != hex.EncodeToString(certificateHash[:]) || r.TLS.PeerCertificates[0].VerifyHostname(mesh.SAN(scope.Source)) != nil {
		deny(http.StatusForbidden)
		return
	}
	h.mu.Lock()
	if h.pending >= h.Limits.Global || h.counts[scope.Source] >= h.Limits.PerPeer {
		h.mu.Unlock()
		deny(http.StatusTooManyRequests)
		return
	}
	if h.counts == nil {
		h.counts = map[string]int{}
	}
	h.pending++
	h.counts[scope.Source]++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.pending--
		h.counts[scope.Source]--
		if h.counts[scope.Source] == 0 {
			delete(h.counts, scope.Source)
		}
		h.mu.Unlock()
	}()
	capture := &captureHijacker{ResponseWriter: w}
	ws, err := websocket.Accept(capture, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	abort := func() error {
		capture.socket.close()
		return ws.CloseNow()
	}
	defer abort()
	ws.SetReadLimit(MaxControlBytes)
	initial, cancel := context.WithTimeout(r.Context(), h.Limits.Handshake)
	initialAbort := newJoinedAbort(initial, capture.socket.close)
	defer initialAbort.stopAndJoin()
	kind, raw, err := ws.Read(initial)
	initialAbort.stopAndJoin()
	cancel()
	if err != nil || kind != websocket.MessageBinary {
		return
	}
	control, err := decodeControl(raw)
	if err != nil || control.Authorize(scope) != nil {
		return
	}
	lifetime := min(h.Limits.Lifetime, control.TTL+h.Limits.Handshake, time.Until(scope.ValidUntil), time.Until(r.TLS.PeerCertificates[0].NotAfter))
	ctx, cancel := context.WithTimeout(r.Context(), lifetime)
	defer cancel()
	conn := newBounded(websocket.NetConn(ctx, ws, websocket.MessageBinary), h.Limits, cancel, abort)
	ws.SetReadLimit(MaxPayloadBytes) // NetConn disables it during construction.
	defer conn.Close()
	h.mu.Lock()
	if h.streams == nil {
		h.streams = map[*boundedConn]Scope{}
	}
	h.streams[conn] = scope
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.streams, conn); h.mu.Unlock() }()
	// Register first: revocation cannot miss a successfully admitted live stream.
	if h.Revalidate(ctx, scope) != nil {
		return
	}
	ack, err := json.Marshal(control.Ready())
	if err != nil {
		return
	}
	initial, stop := context.WithTimeout(ctx, h.Limits.Handshake)
	err = ws.Write(initial, websocket.MessageBinary, ack)
	stop()
	if err != nil {
		return
	}
	watchDone := conn.startWatch(ctx, h.Limits, func() error { return h.Revalidate(ctx, scope) })
	defer func() { _ = conn.Close(); <-watchDone }()
	h.Handle(ctx, scope, control, conn)
}

// ClosePeer also catches hijacked WebSocket streams absent from HTTP ConnState.
func (h *Handler) ClosePeer(source string) {
	h.mu.Lock()
	var close []*boundedConn
	for c, s := range h.streams {
		if source == "" || s.Source == source {
			close = append(close, c)
		}
	}
	h.mu.Unlock()
	for _, c := range close {
		_ = c.Close()
	}
}
func (h *Handler) Active() int { h.mu.Lock(); defer h.mu.Unlock(); return h.pending }

// Dial accepts only literal peer addresses and an already pinned TLS policy.
// It never redirects, proxies or retries a failed connection.
func Dial(ctx context.Context, address string, policy *tls.Config, control Control, limits Limits) (net.Conn, error) {
	if err := control.Validate(); err != nil {
		return nil, err
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	n, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || policy == nil || policy.InsecureSkipVerify || policy.RootCAs == nil || policy.VerifyConnection == nil || policy.ServerName == "" || policy.MinVersion < tls.VersionTLS13 {
		return nil, fmt.Errorf("literal address and pinned TLS1.3 policy required")
	}
	config := policy.Clone()
	config.ClientSessionCache = nil
	config.SessionTicketsDisabled = true
	config.NextProtos = []string{"http/1.1"}
	socket := &socketAbort{}
	tr := pinnedTransportCaptured(address, config, limits, socket)
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	initial, stop := context.WithTimeout(ctx, limits.Handshake)
	defer stop()
	initialAbort := newJoinedAbort(initial, socket.close)
	defer initialAbort.stopAndJoin()
	ws, _, err := websocket.Dial(initial, "wss://"+address+Path, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Protocol}, CompressionMode: websocket.CompressionDisabled})
	tr.CloseIdleConnections()
	if err != nil {
		return nil, err
	}
	abort := func() error { socket.close(); return ws.CloseNow() }
	fail := func(err error) (net.Conn, error) { _ = abort(); return nil, err }
	if ws.Subprotocol() != Protocol {
		return fail(fmt.Errorf("SSH stream protocol mismatch"))
	}
	raw, err := json.Marshal(control)
	if err != nil || len(raw) > MaxControlBytes {
		return fail(fmt.Errorf("SSH control exceeds bound"))
	}
	if err = ws.Write(initial, websocket.MessageBinary, raw); err != nil {
		return fail(err)
	}
	ws.SetReadLimit(MaxControlBytes)
	kind, raw, err := ws.Read(initial)
	if err != nil || kind != websocket.MessageBinary {
		return fail(fmt.Errorf("SSH stream admission unavailable"))
	}
	if _, err = closedObject(raw, []string{"version", "source", "target", "pair", "revision", "account", "control_sha256"}); err != nil {
		return fail(err)
	}
	var accepted ready
	expectedAck, _ := json.Marshal(control.Ready())
	if err = json.Unmarshal(raw, &accepted); err != nil || accepted != control.Ready() || !bytes.Equal(raw, expectedAck) {
		return fail(fmt.Errorf("SSH stream admission identity changed"))
	}
	if !initialAbort.stopAndJoin() || initial.Err() != nil {
		return fail(fmt.Errorf("SSH stream admission timed out"))
	}
	life, cancel := context.WithTimeout(ctx, min(limits.Lifetime, control.TTL+limits.Handshake))
	conn := newBounded(websocket.NetConn(life, ws, websocket.MessageBinary), limits, cancel, abort)
	ws.SetReadLimit(MaxPayloadBytes) // Restore after NetConn construction.
	watchDone := conn.startWatch(life, limits, nil)
	return &watchedConn{boundedConn: conn, done: watchDone}, nil
}

type joinedAbort struct {
	stop    func() bool
	done    chan struct{}
	once    sync.Once
	stopped bool
}

func newJoinedAbort(ctx context.Context, abort func()) *joinedAbort {
	owner := &joinedAbort{done: make(chan struct{})}
	owner.stop = context.AfterFunc(ctx, func() { defer close(owner.done); abort() })
	return owner
}
func (owner *joinedAbort) stopAndJoin() bool {
	owner.once.Do(func() {
		owner.stopped = owner.stop()
		if owner.stopped {
			close(owner.done)
		}
	})
	<-owner.done
	return owner.stopped
}

type watchedConn struct {
	*boundedConn
	done <-chan struct{}
}

func (c *watchedConn) Close() error {
	err := c.boundedConn.Close()
	<-c.done
	return err
}

type boundedConn struct {
	abort func() error
	net.Conn
	readMu, writeMu       sync.Mutex
	readBytes, writeBytes int64
	max                   int64
	last                  atomic.Int64
	elapsed               func() time.Duration
	cancel                context.CancelFunc
	once                  sync.Once
}

func newBounded(c net.Conn, l Limits, cancel context.CancelFunc, abort func() error) *boundedConn {
	// time.Since retains the epoch's monotonic component. Never serialize wall
	// time for inactivity: clock corrections must not extend a live stream.
	epoch := time.Now()
	b := &boundedConn{Conn: c, max: l.BytesPerDirection, cancel: cancel, abort: abort, elapsed: func() time.Duration { return time.Since(epoch) }}
	return b
}
func (c *boundedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		if c.abort != nil {
			_ = c.abort()
		}
		err = c.Conn.Close()
	})
	return err
}
func (c *boundedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.readBytes >= c.max {
		_ = c.Close()
		return 0, fmt.Errorf("SSH incoming byte limit reached")
	}
	if int64(len(p)) > c.max-c.readBytes {
		p = p[:c.max-c.readBytes]
	}
	n, err := c.Conn.Read(p)
	c.readBytes += int64(n)
	if n > 0 {
		c.last.Store(int64(c.elapsed()))
	}
	return n, err
}
func (c *boundedConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		if c.writeBytes >= c.max {
			_ = c.Close()
			return total, fmt.Errorf("SSH outgoing byte limit reached")
		}
		nmax := min(int64(len(p)), int64(MaxPayloadBytes), c.max-c.writeBytes)
		n, err := c.Conn.Write(p[:nmax])
		total += n
		c.writeBytes += int64(n)
		p = p[n:]
		if n > 0 {
			c.last.Store(int64(c.elapsed()))
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
func (c *boundedConn) idleFor() time.Duration {
	return c.elapsed() - time.Duration(c.last.Load())
}
func (c *boundedConn) watch(ctx context.Context, l Limits, check func() error) {
	tick := time.NewTicker(max(10*time.Millisecond, min(time.Second, l.Idle/4)))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-tick.C:
			if c.idleFor() >= l.Idle || check != nil && check() != nil {
				_ = c.Close()
				return
			}
		}
	}
}
func (c *boundedConn) startWatch(ctx context.Context, l Limits, check func() error) <-chan struct{} {
	done := make(chan struct{})
	go func() { defer close(done); c.watch(ctx, l, check) }()
	return done
}

// Go's HTTP transport suppresses ALPN for Upgrade requests in its built-in TLS
// dial path. Perform the pinned handshake explicitly: never relax the mesh's
// exact http/1.1 ALPN requirement merely to carry a WebSocket.
func pinnedTransport(address string, config *tls.Config, limits Limits) *http.Transport {
	return pinnedTransportCaptured(address, config, limits, nil)
}
func pinnedTransportCaptured(address string, config *tls.Config, limits Limits, socket *socketAbort) *http.Transport {
	return &http.Transport{Proxy: nil, TLSClientConfig: config, TLSHandshakeTimeout: limits.Handshake, ResponseHeaderTimeout: limits.Handshake, DisableKeepAlives: true, DialTLSContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
		if network != "tcp" || requested != address {
			return nil, fmt.Errorf("SSH stream dial target changed")
		}
		handshake, cancel := context.WithTimeout(ctx, limits.Handshake)
		defer cancel()
		raw, err := (&net.Dialer{}).DialContext(handshake, "tcp", address)
		if err != nil {
			return nil, err
		}
		if socket != nil {
			socket.set(raw)
		}
		conn := tls.Client(raw, config)
		if err = conn.HandshakeContext(handshake); err != nil {
			raw.Close()
			return nil, err
		}
		return conn, nil
	}}
}

// Preserve only the exact admitted socket. WebSocket's protocol-error Close may
// wait for its own five-second handshake despite NetConn context cancellation.
// Aborting the underlying TCP socket first makes forced shutdown independent of
// that close handshake, including when another goroutine is already closing.
type socketAbort struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

func (s *socketAbort) set(c net.Conn) {
	if t, ok := c.(*tls.Conn); ok {
		c = t.NetConn()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	s.conn = c
	s.mu.Unlock()
}
func (s *socketAbort) close() {
	s.mu.Lock()
	s.closed = true
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

type captureHijacker struct {
	http.ResponseWriter
	socket socketAbort
}

func (w *captureHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.socket.set(c)
	}
	return c, b, err
}
