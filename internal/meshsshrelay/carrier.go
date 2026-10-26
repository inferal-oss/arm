package meshsshrelay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshforward"
	"github.com/inferal-oss/arm/internal/workspace"
)

func digest(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

type Outer struct {
	Source, Target, Pair, Certificate string
	Revision                          int
	Expires                           time.Time
}

// Permit is an in-process current ledger result, never a transferable token.
// Relays get no TLS private endpoint; only the final dedicated handler receives
// an inner TLS server policy pinned to the ORIGINAL authenticated source.
type Permit struct {
	Local            workspace.Identity
	Origin           workspace.Identity
	Destination      workspace.Identity
	Expires          time.Time
	Inner            *tls.Config
	RelayGrant       RelayGrant
	DestinationGrant DestinationGrant
	ForwardGrant     meshforward.Grant
	TransitRate      int64
}
type TransitReservation struct {
	Context context.Context
	End     func()
	Finish  func()
}
type Handler struct {
	RoutePath      string
	Subprotocol    string
	AllowedClass   string
	AuthorizeOuter func(context.Context, []byte) (Outer, error)
	Authorize      func(context.Context, Outer, Open) (Permit, error)
	ReserveTransit func(admission, lifetime context.Context, joined <-chan struct{}, outer Outer, open Open, permit Permit) (TransitReservation, error)
	ReserveFinal   func(admission, lifetime context.Context, joined <-chan struct{}, outer Outer, open Open, permit Permit) (TransitReservation, error)
	Revalidate     func(context.Context, Outer, Open, Permit) error
	Forward        func(context.Context, Outer, Open, Permit, net.Conn)
	Final          func(context.Context, Outer, Open, Permit, *tls.Conn)
	mu             sync.Mutex
	closed         bool
	active         map[*routeStream]Outer
	counts         map[string]int
	classes        map[string]int
	origins        map[string]int
	workers        sync.WaitGroup
	budget         admissionBudget
	abortHook      func() // test-only hook after socket abort, set before ServeHTTP
}
type routeStream struct {
	cancel context.CancelFunc
	socket *socketAbort
}

func (h *Handler) ClosePeer(peer string) {
	h.mu.Lock()
	var list []*routeStream
	for c, o := range h.active {
		if peer == "" || o.Source == peer {
			list = append(list, c)
		}
	}
	h.mu.Unlock()
	for _, c := range list {
		c.cancel()
		c.socket.close()
	}
}
func (h *Handler) Stop() { h.mu.Lock(); h.closed = true; h.mu.Unlock(); h.ClosePeer("") }
func (h *Handler) Wait() { h.workers.Wait() }
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, protocol := h.RoutePath, h.Subprotocol
	if path == "" {
		path = Path
	}
	if protocol == "" {
		protocol = Protocol
	}
	deny := func(n int) { http.Error(w, "SSH relay unavailable", n) }
	if r.Method != "GET" || r.URL.Path != path || r.URL.RawQuery != "" || r.URL.RawPath != "" || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 || r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || r.TLS.DidResume || r.TLS.NegotiatedProtocol != "http/1.1" || len(r.TLS.PeerCertificates) != 1 || len(r.TLS.VerifiedChains) == 0 || h.AuthorizeOuter == nil || h.Authorize == nil || h.Revalidate == nil || h.Forward == nil || h.Final == nil {
		deny(http.StatusForbidden)
		return
	}
	declared := r.Header.Values("Arm-SSH-Route-Class")
	if len(declared) != 1 || declared[0] != ClassSSH && declared[0] != ClassSync && declared[0] != ClassSSHIdentity && declared[0] != ClassForward ||
		h.AllowedClass != "" && declared[0] != h.AllowedClass || h.AllowedClass == "" && declared[0] == ClassForward {
		deny(http.StatusBadRequest)
		return
	}
	class := declared[0]
	globalLimit, peerLimit := 8, 2
	protocols := r.Header.Values("Sec-Websocket-Protocol")
	if len(protocols) != 1 || strings.TrimSpace(protocols[0]) != protocol {
		deny(http.StatusBadRequest)
		return
	}
	outer, e := h.AuthorizeOuter(r.Context(), r.TLS.PeerCertificates[0].Raw)
	if e != nil || outer.Certificate != digest(r.TLS.PeerCertificates[0].Raw) || r.TLS.PeerCertificates[0].VerifyHostname(mesh.SAN(outer.Source)) != nil || !time.Now().Before(outer.Expires) {
		deny(http.StatusForbidden)
		return
	}
	capture := &captureHijacker{ResponseWriter: w}
	h.mu.Lock()
	if h.closed || len(h.active) >= 8 || h.classes[class] >= globalLimit || h.counts[outer.Source+":"+class] >= peerLimit {
		h.mu.Unlock()
		deny(http.StatusTooManyRequests)
		return
	}
	if h.active == nil {
		h.active = map[*routeStream]Outer{}
		h.counts = map[string]int{}
		h.classes = map[string]int{}
		h.origins = map[string]int{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(MaxLifetime, time.Until(outer.Expires)))
	stream := &routeStream{cancel, captureSocket(capture)}
	h.active[stream] = outer
	h.counts[outer.Source+":"+class]++
	h.classes[class]++
	h.workers.Add(1)
	h.mu.Unlock()
	defer h.workers.Done()
	abortSocket := func() {
		capture.socket.close()
		if h.abortHook != nil {
			h.abortHook()
		}
	}
	defer func() {
		cancel()
		capture.socket.close()
		h.mu.Lock()
		delete(h.active, stream)
		h.counts[outer.Source+":"+class]--
		h.classes[class]--
		if h.counts[outer.Source+":"+class] == 0 {
			delete(h.counts, outer.Source+":"+class)
		}
		h.mu.Unlock()
	}()
	abortOnCancel := startAbortWorker(ctx, abortSocket)
	defer abortOnCancel.StopAndJoin()
	ws, e := websocket.Accept(capture, r, &websocket.AcceptOptions{Subprotocols: []string{protocol}, CompressionMode: websocket.CompressionDisabled})
	if e != nil {
		return
	}
	defer func() { capture.socket.close(); _ = ws.CloseNow() }()
	ws.SetReadLimit(MaxControlBytes)
	initial, stop := context.WithTimeout(ctx, 5*time.Second)
	abortInitial := startAbortWorker(initial, abortSocket)
	defer abortInitial.StopAndJoin()
	kind, raw, e := ws.Read(initial)
	if e != nil || kind != websocket.MessageBinary {
		abortInitial.StopAndJoin()
		stop()
		return
	}
	o, e := DecodeOpen(raw, time.Now())
	if e != nil || o.Class != class || o.ImmediateSource() != outer.Source || o.ImmediateTarget() != outer.Target || o.Pair != outer.Pair || o.EnrollmentRevision != outer.Revision {
		abortInitial.StopAndJoin()
		stop()
		return
	}
	permit, e := h.Authorize(initial, outer, o)
	if e != nil || permit.Local.Host != outer.Target || permit.Origin.Host != o.Source || permit.Destination.Host != o.Target || !time.Now().Before(permit.Expires) || !h.budget.admit(outer.Source, o.Workspace+":"+o.Generation+":"+o.Route, minTime(o.Expires, permit.Expires), class) {
		abortInitial.StopAndJoin()
		stop()
		return
	}
	if o.Final() {
		if !validInner(permit, true, o.Class) {
			abortInitial.StopAndJoin()
			stop()
			return
		}
	} else if permit.Inner != nil {
		abortInitial.StopAndJoin()
		stop()
		return
	}
	lifetime := ctx
	if !o.Final() {
		if h.ReserveTransit == nil || permit.TransitRate < 64<<10 || permit.TransitRate > 1<<30 {
			abortInitial.StopAndJoin()
			stop()
			return
		}
		joined := make(chan struct{})
		reservation, reserveErr := h.ReserveTransit(initial, ctx, joined, outer, o, permit)
		if reserveErr != nil || reservation.Context == nil || reservation.End == nil || reservation.Finish == nil {
			// A reservation may already own durable resources even when its
			// final admission check fails. Join every pre-reservation abort
			// callback and close the WebSocket before publishing its witness.
			capture.socket.close()
			_ = ws.CloseNow()
			abortInitial.StopAndJoin()
			abortOnCancel.StopAndJoin()
			if reservation.End != nil {
				reservation.End()
			}
			close(joined)
			if reservation.Finish != nil {
				reservation.Finish()
			}
			stop()
			return
		}
		lifetime = reservation.Context
		// Manager end and expiry abort even an admission ACK that is still in
		// flight; the durable allocation remains charged until all work joins.
		holdAbort := startAbortWorker(lifetime, abortSocket)
		// Later stream defers close/join both directions and the watcher before
		// this transit CPU/memory reservation is released.
		defer func() {
			// Admission may fail before boundedConn exists. In that path the
			// earlier WebSocket defer has not run yet, so close its carrier
			// explicitly before recording the original transport join.
			capture.socket.close()
			_ = ws.CloseNow()
			holdAbort.StopAndJoin()
			abortInitial.StopAndJoin()
			abortOnCancel.StopAndJoin()
			reservation.End()
			close(joined)
			reservation.Finish()
		}()
	}
	ack, _ := json.Marshal(ready{Version, o.Route, o.WireDigest()})
	if e = ws.Write(initial, websocket.MessageBinary, ack); e != nil {
		abortInitial.StopAndJoin()
		stop()
		return
	}
	if !abortInitial.StopAndJoin() || initial.Err() != nil {
		stop()
		return
	}
	stop()
	life, finish := context.WithTimeout(lifetime, min(time.Until(o.Expires), time.Until(permit.Expires)))
	defer finish()
	conn := newBounded(websocket.NetConn(life, ws, websocket.MessageBinary), finish, func() error { capture.socket.close(); return ws.CloseNow() })
	ws.SetReadLimit(MaxPayloadBytes)
	conn.startWatch(life, func() error { return h.Revalidate(life, outer, o, permit) })
	defer conn.Close()
	if e = h.Revalidate(life, outer, o, permit); e != nil {
		return
	}
	if !o.Final() {
		h.Forward(life, outer, o, permit, conn)
		return
	}
	inner := tls.Server(conn, permit.Inner)
	handshake, end := context.WithTimeout(life, 5*time.Second)
	e = inner.HandshakeContext(handshake)
	end()
	if e != nil || h.Revalidate(life, outer, o, permit) != nil {
		return
	}
	if AcceptBinding(inner, o) != nil || h.Revalidate(life, outer, o, permit) != nil {
		return
	}
	// Count the ORIGINAL source only after its own end-to-end TLS proof. Hop
	// headers cannot spend another member's execution stream slots.
	h.mu.Lock()
	originBucket := o.Source
	originLimit := 2
	if h.origins[originBucket] >= originLimit {
		h.mu.Unlock()
		return
	}
	h.origins[originBucket]++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.origins[originBucket]--
		if h.origins[originBucket] == 0 {
			delete(h.origins, originBucket)
		}
		h.mu.Unlock()
	}()
	if o.Class == ClassForward {
		if h.ReserveFinal == nil {
			return
		}
		joined := make(chan struct{})
		reservation, reserveErr := h.ReserveFinal(life, life, joined, outer, o, permit)
		if reserveErr != nil || reservation.Context == nil || reservation.End == nil || reservation.Finish == nil {
			_ = conn.Close()
			capture.socket.close()
			_ = ws.CloseNow()
			abortInitial.StopAndJoin()
			abortOnCancel.StopAndJoin()
			if reservation.End != nil {
				reservation.End()
			}
			close(joined)
			if reservation.Finish != nil {
				reservation.Finish()
			}
			return
		}
		holdAbort := startAbortWorker(reservation.Context, abortSocket)
		defer func() {
			_ = conn.Close() // joins the original carrier revalidation watcher
			capture.socket.close()
			_ = ws.CloseNow()
			holdAbort.StopAndJoin()
			abortInitial.StopAndJoin()
			abortOnCancel.StopAndJoin()
			reservation.End()
			close(joined)
			reservation.Finish()
		}()
		h.Final(reservation.Context, outer, o, permit, inner)
		return
	}
	h.Final(life, outer, o, permit, inner)
}
func captureSocket(c *captureHijacker) *socketAbort { return &c.socket }

type ready struct {
	Version int    `json:"schema_version"`
	Route   string `json:"route_id"`
	Digest  string `json:"open_sha256"`
}

func validInner(p Permit, server bool, class string) bool {
	c := p.Inner
	innerProtocol := InnerProtocol
	if class == ClassForward {
		innerProtocol = ForwardInnerProtocol
	}
	if c == nil || c.InsecureSkipVerify || c.MinVersion != tls.VersionTLS13 || c.MaxVersion != tls.VersionTLS13 || c.VerifyConnection == nil || len(c.NextProtos) != 1 || c.NextProtos[0] != innerProtocol {
		return false
	}
	if server {
		return c.ClientAuth == tls.RequireAndVerifyClientCert && c.ClientCAs != nil
	}
	return c.RootCAs != nil && c.ServerName == mesh.SAN(p.Destination.Host)
}

// DialCarrier creates one bounded outer hop and returns only its raw opaque
// stream. Relays MUST NOT wrap it in TLS: the original source performs the one
// final-destination handshake, after all admitted hops have been connected.
func DialCarrier(ctx context.Context, address string, policy *tls.Config, o Open, revalidate func(context.Context) error) (net.Conn, error) {
	path, protocol := Path, Protocol
	if o.Class == ClassForward {
		path, protocol = ForwardPath, ForwardProtocol
	}
	if e := o.Validate(time.Now()); e != nil {
		return nil, e
	}
	host, port, e := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || n < 1 || n > 65535 || net.ParseIP(host) == nil || policy == nil || policy.InsecureSkipVerify || policy.RootCAs == nil || policy.ServerName != mesh.SAN(o.ImmediateTarget()) || policy.VerifyConnection == nil || policy.MinVersion < tls.VersionTLS13 || revalidate == nil {
		return nil, fmt.Errorf("literal reviewed hop and pinned TLS1.3 policy required")
	}
	if e = revalidate(ctx); e != nil {
		return nil, e
	}
	config := policy.Clone()
	config.NextProtos = []string{"http/1.1"}
	config.ClientSessionCache = nil
	config.SessionTicketsDisabled = true
	socket := &socketAbort{}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DialTLSContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
		if network != "tcp" || requested != address {
			return nil, fmt.Errorf("SSH relay hop changed")
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
	initialAbort := startAbortWorker(initial, socket.close)
	defer initialAbort.StopAndJoin()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ws, _, e := websocket.Dial(initial, "wss://"+address+path, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Arm-SSH-Route-Class": []string{o.Class}}, Subprotocols: []string{protocol}, CompressionMode: websocket.CompressionDisabled})
	tr.CloseIdleConnections()
	if e != nil {
		socket.close()
		return nil, e
	}
	abort := func() error { socket.close(); return ws.CloseNow() }
	fail := func(e error) (net.Conn, error) { _ = abort(); return nil, e }
	if ws.Subprotocol() != protocol {
		return fail(fmt.Errorf("SSH relay protocol differs"))
	}
	raw, _ := json.Marshal(o)
	if e = ws.Write(initial, websocket.MessageBinary, raw); e != nil {
		return fail(e)
	}
	ws.SetReadLimit(MaxControlBytes)
	kind, raw, e := ws.Read(initial)
	expected, _ := json.Marshal(ready{Version, o.Route, o.WireDigest()})
	if e != nil || kind != websocket.MessageBinary || !bytes.Equal(raw, expected) {
		return fail(fmt.Errorf("SSH relay admission unavailable or changed"))
	}
	if !initialAbort.StopAndJoin() || initial.Err() != nil {
		return fail(fmt.Errorf("SSH relay admission timed out"))
	}
	life, cancel := context.WithTimeout(ctx, min(MaxLifetime, time.Until(o.Expires)))
	conn := newBounded(websocket.NetConn(life, ws, websocket.MessageBinary), cancel, abort)
	ws.SetReadLimit(MaxPayloadBytes)
	conn.startWatch(life, func() error { return revalidate(life) })
	return conn, nil
}

// Finished is lifecycle bookkeeping only, never an authorization observation.
func (h *Handler) Finished() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed && len(h.active) == 0
}
