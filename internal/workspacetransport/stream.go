package workspacetransport

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
	"github.com/inferal-oss/arm/internal/workspace"
)

// Outer is a destination-owned snapshot of one authenticated direct pair. It
// conveys transport reachability only, never directory membership or grants.
type Outer struct {
	Source, Target, Pair, Certificate string
	Revision                          int
	Expires                           time.Time
}
type Admission struct {
	Local, Remote workspace.Identity
	Expires       time.Time
	TLS           *tls.Config
}

// Handler serves only the closed workspace protocol. Every class-specific
// admission and inner operation must consult current workspace ledger state.
// Callbacks must honor cancellation and never retain database/trust locks over IO.
type Handler struct {
	AuthorizeOuter func(context.Context, []byte) (Outer, error)
	Authorize      func(context.Context, Outer, Open) (Admission, error)
	Revalidate     func(context.Context, Outer, Open, Admission) error
	Handle         func(context.Context, Outer, Open, Admission, *tls.Conn)
	mu             sync.Mutex
	streams        map[*liveStream]Outer
	counts         map[string]int
	closed         bool
	workers        sync.WaitGroup
}
type liveStream struct {
	cancel context.CancelFunc
	abort  func() error
}

func (h *Handler) ClosePeer(source string) {
	h.mu.Lock()
	var list []*liveStream
	for stream, outer := range h.streams {
		if source == "" || source == outer.Source {
			list = append(list, stream)
		}
	}
	h.mu.Unlock()
	for _, stream := range list {
		stream.cancel()
		_ = stream.abort()
	}
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	deny := func(code int) { http.Error(w, "workspace stream unavailable", code) }
	if r.Method != "GET" || r.URL.Path != Path || r.URL.RawQuery != "" || r.URL.RawPath != "" || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || r.TLS.DidResume || len(r.TLS.PeerCertificates) != 1 || len(r.TLS.VerifiedChains) == 0 || r.TLS.NegotiatedProtocol != "http/1.1" || h.AuthorizeOuter == nil || h.Authorize == nil || h.Revalidate == nil || h.Handle == nil {
		deny(http.StatusForbidden)
		return
	}
	offered := r.Header.Values("Sec-Websocket-Protocol")
	if len(offered) != 1 || strings.TrimSpace(offered[0]) != Protocol {
		deny(http.StatusBadRequest)
		return
	}
	outer, e := h.AuthorizeOuter(r.Context(), r.TLS.PeerCertificates[0].Raw)
	fingerprint := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	if e != nil || outer.Certificate != hex.EncodeToString(fingerprint[:]) || r.TLS.PeerCertificates[0].VerifyHostname(mesh.SAN(outer.Source)) != nil || !time.Now().Before(outer.Expires) {
		deny(http.StatusForbidden)
		return
	}
	h.mu.Lock()
	if h.closed || len(h.streams) >= 32 || h.counts[outer.Source] >= 8 {
		h.mu.Unlock()
		deny(http.StatusTooManyRequests)
		return
	}
	if h.streams == nil {
		h.streams = map[*liveStream]Outer{}
		h.counts = map[string]int{}
	}
	// The reservation itself is registered before the WebSocket upgrade. ClosePeer
	// can cancel pending control/handshake as well as an established inner stream.
	ctx, cancel := context.WithTimeout(r.Context(), min(MaxLifetime, time.Until(outer.Expires)))
	capture := &captureHijacker{ResponseWriter: w}
	live := &liveStream{cancel: cancel, abort: func() error { capture.socket.close(); return nil }}
	h.workers.Add(1)
	h.streams[live] = outer
	h.counts[outer.Source]++
	h.mu.Unlock()
	defer h.workers.Done()
	defer func() {
		cancel()
		capture.socket.close()
		h.mu.Lock()
		delete(h.streams, live)
		h.counts[outer.Source]--
		if h.counts[outer.Source] == 0 {
			delete(h.counts, outer.Source)
		}
		h.mu.Unlock()
	}()
	stopAbort := context.AfterFunc(ctx, capture.socket.close)
	defer stopAbort()
	ws, e := websocket.Accept(capture, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}, CompressionMode: websocket.CompressionDisabled})
	if e != nil {
		return
	}
	defer func() { capture.socket.close(); _ = ws.CloseNow() }()
	ws.SetReadLimit(MaxControlBytes)
	initial, stop := context.WithTimeout(ctx, 5*time.Second)
	abortInitial := context.AfterFunc(initial, capture.socket.close)
	kind, raw, e := ws.Read(initial)
	if e != nil || kind != websocket.MessageBinary {
		abortInitial()
		stop()
		return
	}
	control, e := DecodeOpen(raw, time.Now())
	if e != nil || control.Source != outer.Source || control.Target != outer.Target || control.Pair != outer.Pair || control.Revision != outer.Revision {
		abortInitial()
		stop()
		return
	}
	admission, e := h.Authorize(initial, outer, control)
	if e != nil || !validAdmission(admission, true) || !time.Now().Before(admission.Expires) {
		abortInitial()
		stop()
		return
	}
	ready, _ := json.Marshal(readyFor(control))
	if e = ws.Write(initial, websocket.MessageBinary, ready); e != nil {
		abortInitial()
		stop()
		return
	}
	if !abortInitial() || initial.Err() != nil {
		stop()
		return
	}
	stop()
	life, finish := context.WithTimeout(ctx, min(time.Until(control.Expires), time.Until(admission.Expires)))
	defer finish()
	conn := newBounded(websocket.NetConn(life, ws, websocket.MessageBinary), finish, func() error { capture.socket.close(); return ws.CloseNow() })
	ws.SetReadLimit(MaxPayloadBytes) // NetConn resets this to unlimited.
	defer conn.Close()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		conn.watch(life, func() error { return h.Revalidate(life, outer, control, admission) })
	}()
	defer func() { _ = conn.Close(); <-watchDone }()
	inner := tls.Server(conn, admission.TLS)
	handshake, end := context.WithTimeout(life, 5*time.Second)
	e = inner.HandshakeContext(handshake)
	end()
	if e != nil || h.Revalidate(life, outer, control, admission) != nil {
		return
	}
	h.Handle(life, outer, control, admission, inner)
}

type ready struct {
	Version int    `json:"schema_version"`
	Route   string `json:"route_id"`
	Hash    string `json:"open_sha256"`
}

func readyFor(o Open) ready {
	raw, _ := json.Marshal(o)
	sum := sha256.Sum256(raw)
	return ready{Version, o.Route, hex.EncodeToString(sum[:])}
}
func validAdmission(a Admission, server bool) bool {
	c := a.TLS
	if a.Local.Host == a.Remote.Host || c == nil || c.InsecureSkipVerify || c.MinVersion != tls.VersionTLS13 || c.MaxVersion != tls.VersionTLS13 || c.VerifyConnection == nil || len(c.NextProtos) != 1 || c.NextProtos[0] != InnerProtocol {
		return false
	}
	if server {
		return c.ClientAuth == tls.RequireAndVerifyClientCert && c.ClientCAs != nil
	}
	return c.RootCAs != nil && c.ServerName == mesh.SAN(a.Remote.Host)
}

// Dial admits exactly one direct hop, then authenticates the pinned workspace
// endpoints independently through nested TLS. No proxy, redirect, retry or URL
// is accepted; the address must come from the caller's current direct registry.
func Dial(ctx context.Context, address string, outer *tls.Config, open Open, admission Admission, revalidate func(context.Context) error) (*tls.Conn, error) {
	if e := open.Validate(time.Now()); e != nil {
		return nil, e
	}
	host, port, e := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || outer == nil || outer.InsecureSkipVerify || outer.RootCAs == nil || outer.VerifyConnection == nil || outer.ServerName == "" || outer.MinVersion < tls.VersionTLS13 || !validAdmission(admission, false) || revalidate == nil {
		return nil, fmt.Errorf("literal target and pinned outer/inner policies required")
	}
	if e = revalidate(ctx); e != nil {
		return nil, e
	}
	config := outer.Clone()
	config.ClientSessionCache = nil
	config.SessionTicketsDisabled = true
	config.NextProtos = []string{"http/1.1"}
	socket := &socketAbort{}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DialTLSContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
		if network != "tcp" || requested != address {
			return nil, fmt.Errorf("workspace dial target changed")
		}
		raw, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if e != nil {
			return nil, e
		}
		socket.set(raw)
		conn := tls.Client(raw, config)
		if e = conn.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, e
		}
		return conn, nil
	}}
	initial, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	stopAbort := context.AfterFunc(initial, socket.close)
	defer stopAbort()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ws, _, e := websocket.Dial(initial, "wss://"+address+Path, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Protocol}, CompressionMode: websocket.CompressionDisabled})
	tr.CloseIdleConnections()
	if e != nil {
		socket.close()
		return nil, e
	}
	abort := func() error { socket.close(); return ws.CloseNow() }
	fail := func(e error) (*tls.Conn, error) { _ = abort(); return nil, e }
	if ws.Subprotocol() != Protocol {
		return fail(fmt.Errorf("workspace protocol differs"))
	}
	raw, _ := json.Marshal(open)
	if e = ws.Write(initial, websocket.MessageBinary, raw); e != nil {
		return fail(e)
	}
	ws.SetReadLimit(MaxControlBytes)
	kind, raw, e := ws.Read(initial)
	want, _ := json.Marshal(readyFor(open))
	if e != nil || kind != websocket.MessageBinary || !bytes.Equal(raw, want) {
		return fail(fmt.Errorf("workspace admission unavailable or changed"))
	}
	if !stopAbort() || initial.Err() != nil {
		return fail(fmt.Errorf("workspace admission timed out"))
	}
	life, cancel := context.WithTimeout(ctx, min(MaxLifetime, time.Until(open.Expires), time.Until(admission.Expires)))
	conn := newBounded(websocket.NetConn(life, ws, websocket.MessageBinary), cancel, abort)
	ws.SetReadLimit(MaxPayloadBytes)
	go conn.watch(life, func() error { return revalidate(life) })
	inner := tls.Client(conn, admission.TLS)
	handshake, end := context.WithTimeout(life, 5*time.Second)
	e = inner.HandshakeContext(handshake)
	end()
	if e != nil {
		_ = conn.Close()
		return nil, e
	}
	if e = revalidate(life); e != nil {
		_ = conn.Close()
		return nil, e
	}
	return inner, nil
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

func newBounded(c net.Conn, cancel context.CancelFunc, abort func() error) *boundedConn {
	// time.Since retains the epoch's monotonic component. Never serialize wall
	// time for inactivity: clock corrections must not extend a live stream.
	epoch := time.Now()
	b := &boundedConn{Conn: c, max: MaxStreamBytes, cancel: cancel, abort: abort, elapsed: func() time.Duration { return time.Since(epoch) }}
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
		return 0, fmt.Errorf("workspace incoming byte limit reached")
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
			return total, fmt.Errorf("workspace outgoing byte limit reached")
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
func (c *boundedConn) watch(ctx context.Context, check func() error) {
	tick := time.NewTicker(max(10*time.Millisecond, min(time.Second, MaxIdle/4)))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-tick.C:
			if c.idleFor() >= MaxIdle || check != nil && check() != nil {
				_ = c.Close()
				return
			}
		}
	}
}

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

// Stop prevents new upgrades and aborts owned streams without waiting. Wait
// must be called outside manager, trust, service and lifecycle locks.
func (h *Handler) Stop() { h.mu.Lock(); h.closed = true; h.mu.Unlock(); h.ClosePeer("") }
func (h *Handler) Wait() { h.workers.Wait() }

// Finished is only a lifecycle bookkeeping observation, never authority.
func (h *Handler) Finished() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed && len(h.streams) == 0
}
