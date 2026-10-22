package routeproto

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type open struct {
	Version   int      `json:"version"`
	Workspace string   `json:"workspace"`
	Target    string   `json:"target"`
	Class     string   `json:"class"`
	Path      []string `json:"path"`
}
type request struct {
	Workspace string `json:"workspace"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Operation string `json:"operation"`
	Body      string `json:"body"`
}
type response struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	Allowed bool   `json:"allowed"`
	Echo    string `json:"echo,omitempty"`
	Error   string `json:"error,omitempty"`
}

type capture struct {
	sync.Mutex
	b bytes.Buffer
}

func (c *capture) Write(p []byte) (int, error) { c.Lock(); defer c.Unlock(); return c.b.Write(p) }
func (c *capture) Bytes() []byte               { c.Lock(); defer c.Unlock(); return bytes.Clone(c.b.Bytes()) }

type fixture struct {
	a, b, c      identity
	ctx          context.Context
	cancel       context.CancelFunc
	first, final *httptest.Server
	streams      sync.Map
	active       atomic.Int32
	dials        atomic.Int32
	cipher       capture
	observed     chan response
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Now()
	f := &fixture{a: newIdentity(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now.Add(-time.Minute), now.Add(time.Hour)), b: newIdentity(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now.Add(-time.Minute), now.Add(time.Hour)), c: newIdentity(t, "cccccccccccccccccccccccccccccccc", now.Add(-time.Minute), now.Add(time.Hour)), observed: make(chan response, 32)}
	f.ctx, f.cancel = context.WithCancel(context.Background())
	serve := func(i, peer identity, handler http.HandlerFunc) *httptest.Server {
		s := httptest.NewUnstartedServer(handler)
		s.Config.ErrorLog = log.New(io.Discard, "", 0)
		s.TLS = policy(i, peer, true, "http/1.1")
		s.StartTLS()
		return s
	}
	f.final = serve(f.c, f.a, func(w http.ResponseWriter, r *http.Request) {
		ws, control, ok := f.accept(w, r, false)
		if !ok {
			return
		}
		defer ws.CloseNow()
		if control.Target != f.c.host {
			return
		}
		conn := bounded(f.ctx, ws, defaultBounds())
		defer conn.Close()
		f.track(conn)
		inner := tls.Server(conn, policy(f.c, f.b, true, innerProtocol))
		ctx, cancel := context.WithTimeout(f.ctx, time.Second)
		defer cancel()
		if e := inner.HandshakeContext(ctx); e != nil {
			return
		}
		_ = inner.SetDeadline(time.Now().Add(time.Second))
		// Authenticate original B from inner TLS, never outer hop A or header.
		cert := inner.ConnectionState().PeerCertificates[0]
		source := strings.TrimSuffix(strings.TrimPrefix(cert.DNSNames[0], "node-"), ".arm.invalid")
		result := response{Source: source, Target: f.c.host}
		var q request
		if e := readObject(inner, &q); e != nil {
			result.Error = "invalid closed message envelope"
		} else if q.Workspace != "workspace-probe" || q.Source != source || q.Target != f.c.host {
			result.Error = "authenticated identity mismatch"
		} else if q.Operation != "message_probe" {
			result.Error = "operation outside closed message probe API"
		} else if len(q.Body) > 16<<10 {
			result.Error = "message body too large"
		} else {
			result.Allowed = true
			result.Echo = q.Body
		}
		_ = writeObject(inner, result)
		select {
		case f.observed <- result:
		default:
		}
	})
	f.first = serve(f.a, f.b, func(w http.ResponseWriter, r *http.Request) {
		ws, control, ok := f.accept(w, r, true)
		if !ok {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
		defer cancel()
		// The only next hop is this already pinned C fixture. No request address.
		f.dials.Add(1)
		next, e := dialOuter(ctx, f.final.Listener.Addr().String(), policy(f.a, f.c, false, "http/1.1"))
		if e != nil {
			return
		}
		defer next.CloseNow()
		control.Path = []string{}
		raw, _ := json.Marshal(control)
		if e = next.Write(ctx, websocket.MessageBinary, raw); e != nil {
			return
		}
		if kind, ack, e := next.Read(ctx); e != nil || kind != websocket.MessageBinary || string(ack) != "ready" {
			return
		}
		if e = ws.Write(ctx, websocket.MessageBinary, []byte("ready")); e != nil {
			return
		}
		left, right := bounded(ctx, ws, defaultBounds()), bounded(ctx, next, defaultBounds())
		defer left.Close()
		defer right.Close()
		f.track(left)
		f.track(right)
		done := make(chan struct{}, 2)
		copyBounded := func(dst net.Conn, src net.Conn) {
			_, _ = io.CopyBuffer(dst, io.TeeReader(src, &f.cipher), make([]byte, 32<<10))
			_ = left.Close()
			_ = right.Close()
			done <- struct{}{}
		}
		go copyBounded(right, left)
		go copyBounded(left, right)
		<-done
		<-done
	})
	t.Cleanup(func() {
		f.cancel()
		f.streams.Range(func(k, v any) bool { _ = k.(net.Conn).Close(); return true })
		f.first.Close()
		f.final.Close()
	})
	return f
}
func (f *fixture) track(c net.Conn) { f.streams.Store(c, true) }
func (f *fixture) accept(w http.ResponseWriter, r *http.Request, relay bool) (*wire, open, bool) {
	if r.Method != "GET" || r.URL.Path != outerPath || r.URL.RawQuery != "" || r.URL.RawPath != "" || len(r.Header.Values("Origin")) > 0 || len(r.Header.Values("Authorization")) > 0 || len(r.Header.Values("Cookie")) > 0 || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || r.TLS.Version != tls.VersionTLS13 || r.TLS.NegotiatedProtocol != "http/1.1" {
		http.Error(w, "closed route only", http.StatusForbidden)
		return nil, open{}, false
	}
	if r.Header.Get("Sec-WebSocket-Protocol") != outerProtocol {
		http.Error(w, "closed protocol only", http.StatusBadRequest)
		return nil, open{}, false
	}
	// A small in-flight admission cap includes open-control waiters. OS accept
	// quotas/rate policy remain subsequent production design, not claimed here.
	if f.active.Add(1) > 8 {
		f.active.Add(-1)
		http.Error(w, "busy", http.StatusTooManyRequests)
		return nil, open{}, false
	}
	defer f.active.Add(-1)
	ws, e := acceptWire(w, r, &websocket.AcceptOptions{Subprotocols: []string{outerProtocol}, CompressionMode: websocket.CompressionDisabled})
	if e != nil {
		return nil, open{}, false
	}
	ws.SetReadLimit(1024)
	ctx, cancel := context.WithTimeout(f.ctx, 300*time.Millisecond)
	stopAbort := context.AfterFunc(ctx, func() { _ = ws.CloseNow() })
	defer func() { stopAbort(); cancel() }()
	kind, raw, e := ws.Read(ctx)
	if e != nil || kind != websocket.MessageBinary {
		ws.CloseNow()
		return nil, open{}, false
	}
	var control open
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if canonicalKeys(raw, &control) != nil || d.Decode(&control) != nil || d.Decode(new(any)) != io.EOF || control.Version != 1 || control.Workspace != "workspace-probe" || control.Target != f.c.host || control.Class != "message" || relay && (len(control.Path) != 1 || control.Path[0] != f.c.host) || !relay && len(control.Path) != 0 {
		ws.CloseNow()
		return nil, open{}, false
	}
	if !relay {
		if e = ws.Write(ctx, websocket.MessageBinary, []byte("ready")); e != nil {
			ws.CloseNow()
			return nil, open{}, false
		}
	}
	return ws, control, true
}
func (f *fixture) connect(t *testing.T, config *tls.Config) (*tls.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	ws, e := dialOuter(ctx, f.first.Listener.Addr().String(), policy(f.b, f.a, false, "http/1.1"))
	if e != nil {
		return nil, e
	}
	q := open{1, "workspace-probe", f.c.host, "message", []string{f.c.host}}
	raw, _ := json.Marshal(q)
	if e = ws.Write(ctx, websocket.MessageBinary, raw); e != nil {
		ws.CloseNow()
		return nil, e
	}
	if kind, ack, e := ws.Read(ctx); e != nil || kind != websocket.MessageBinary || string(ack) != "ready" {
		ws.CloseNow()
		return nil, errors.New("relay admission failed")
	}
	conn := bounded(f.ctx, ws, defaultBounds())
	f.track(conn)
	if config == nil {
		config = policy(f.b, f.c, false, innerProtocol)
	}
	inner := tls.Client(conn, config)
	if e = inner.HandshakeContext(ctx); e != nil {
		conn.Close()
		return nil, e
	}
	return inner, nil
}

func TestThreeNodePinnedNestedTLSKeepsOriginalSourceAndOpaqueBody(t *testing.T) {
	f := newFixture(t)
	c, e := f.connect(t, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	secret := "MESSAGE-PLAINTEXT-MUST-NOT-APPEAR-AT-RELAY"
	if e = writeObject(c, request{"workspace-probe", f.b.host, f.c.host, "message_probe", secret}); e != nil {
		t.Fatal(e)
	}
	var got response
	if e = readObject(c, &got); e != nil {
		t.Fatal(e)
	}
	if !got.Allowed || got.Source != f.b.host || got.Source == f.a.host || got.Target != f.c.host || got.Echo != secret {
		t.Fatalf("origin/destination/body changed: %+v", got)
	}
	if f.dials.Load() != 1 {
		t.Fatal("unexpected next-hop dial count", f.dials.Load())
	}
	if bytes.Contains(f.cipher.Bytes(), []byte(secret)) {
		t.Fatal("relay observed plaintext")
	}
	if c.ConnectionState().Version != tls.VersionTLS13 || c.ConnectionState().DidResume || c.ConnectionState().NegotiatedProtocol != innerProtocol {
		t.Fatal("wrong inner TLS protocol")
	}
}

func TestClosedFinalHandlerDeniesOrdinaryRPCAndForgedSource(t *testing.T) {
	f := newFixture(t)
	for _, op := range []string{"run", "acquire", "status", "message", "agent_message", "node_context_open", "mesh_operator_resume", "ssh", "share", "connect", "port_forward"} {
		t.Run(op, func(t *testing.T) {
			c, e := f.connect(t, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			if e = writeObject(c, request{"workspace-probe", f.b.host, f.c.host, op, ""}); e != nil {
				t.Fatal(e)
			}
			var got response
			if e = readObject(c, &got); e != nil || got.Allowed || !strings.Contains(got.Error, "outside closed") {
				t.Fatal(got, e)
			}
		})
	}
	t.Run("forged-original-relay", func(t *testing.T) {
		c, e := f.connect(t, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		_ = writeObject(c, request{"workspace-probe", f.a.host, f.c.host, "message_probe", ""})
		var got response
		if e = readObject(c, &got); e != nil || got.Allowed || got.Source != f.b.host {
			t.Fatal(got, e)
		}
	})
	for _, raw := range []string{"POST /v1/mesh HTTP/1.1\r\n\r\n", "GET /ws HTTP/1.1\r\nUpgrade: websocket\r\n\r\n", "SSH-2.0-test\r\n"} {
		t.Run(strings.Fields(raw)[0], func(t *testing.T) {
			c, e := f.connect(t, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			if _, e = c.Write([]byte(raw)); e != nil {
				t.Fatal(e)
			}
			var got response
			if e = readObject(c, &got); e != nil || got.Allowed || !strings.Contains(got.Error, "invalid closed") {
				t.Fatal(got, e)
			}
		})
	}
}
