package meshstream

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
)

type transportFixture struct {
	a, b    *mesh.Store
	server  *httptest.Server
	handler *Handler
	address string
	policy  *tls.Config
	q       Control
}

func transportTest(t *testing.T, limits Limits, handle func(context.Context, Scope, Control, net.Conn)) *transportFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a, err := mesh.Init(filepath.Join(t.TempDir(), "source"), strings.Repeat("a", 32), "source", "127.0.0.1:40001", "127.0.0.1:40001")
	if err != nil {
		t.Fatal(err)
	}
	b, err := mesh.Init(filepath.Join(t.TempDir(), "target"), strings.Repeat("b", 32), "target", listener.Addr().String(), listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	grant := mesh.Grant{Permissions: []string{"lease-own", "ssh-exec"}, Limits: model.Resources{CPU: 2, Memory: 1 << 30}, MaxTTL: time.Minute}
	pair := strings.Repeat("c", 64)
	for _, entry := range [][2]*mesh.Store{{a, b}, {b, a}} {
		if _, err = entry[0].Pair(entry[1].Config().Identity, grant, pair, "private stream test"); err != nil {
			t.Fatal(err)
		}
		if err = entry[0].Prove(entry[1].Config().Identity.Host, pair); err != nil {
			t.Fatal(err)
		}
		if err = entry[0].Activate(entry[1].Config().Identity.Host, pair); err != nil {
			t.Fatal(err)
		}
	}
	q := controlFixture()
	q.Resources = model.Resources{CPU: 1}
	q.Account = "armuser"
	incoming, err := b.Resolve(q.Source)
	if err != nil {
		t.Fatal(err)
	}
	q.Revision = incoming.Revision
	h := &Handler{Limits: limits, Handle: handle}
	h.Authorize = func(ctx context.Context, cert []byte) (Scope, error) {
		peer, err := b.PeerByCert(cert)
		if err != nil {
			return Scope{}, err
		}
		if !peer.Grant.Has("ssh-exec") {
			return Scope{}, model.Err("forbidden", "explicit SSH grant required")
		}
		return Scope{peer.Identity.Host, q.Target, peer.Pair, q.Account, peer.Identity.Fingerprint, peer.Revision, peer.Grant.MaxTTL, peer.Grant.Limits, time.Now().Add(time.Minute), q.TargetOwner, q.HostKeySHA256, 1}, nil
	}
	h.Revalidate = func(ctx context.Context, s Scope) error {
		_, err := b.WithPeer(s.Source, func(p mesh.Peer) (any, error) {
			if p.Pair != s.Pair || p.Revision != s.Revision || p.Identity.Fingerprint != s.Certificate || !p.Grant.Has("ssh-exec") {
				return nil, model.Err("forbidden", "changed")
			}
			return true, nil
		})
		return err
	}
	b.OnChange(h.ClosePeer)
	server := httptest.NewUnstartedServer(h)
	server.Listener.Close()
	server.Listener = listener
	server.TLS = b.ServerTLS()
	server.StartTLS()
	t.Cleanup(func() { h.ClosePeer(""); server.Close() })
	peer, err := a.Resolve(q.Target)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := a.ClientTLS(peer)
	if err != nil {
		t.Fatal(err)
	}
	return &transportFixture{a, b, server, h, listener.Addr().String(), policy, q}
}
func TestPinnedMutualTLSStreamLifetimeAndRevocation(t *testing.T) {
	f := transportTest(t, DefaultLimits(), func(ctx context.Context, _ Scope, _ Control, c net.Conn) { _, _ = io.Copy(c, c) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, f.address, f.policy, f.q, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Cancelling the completed handshake must not cancel the established stream.
	time.Sleep(20 * time.Millisecond)
	if _, err = c.Write([]byte("private synthetic stream")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 24)
	if _, err = io.ReadFull(c, got); err != nil || string(got) != "private synthetic stream" {
		t.Fatal(string(got), err)
	}
	if err = f.b.Revoke(f.q.Source, "private revoke"); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = c.Read(make([]byte, 1)); err == nil {
		t.Fatal("revoked stream stayed readable")
	}
	deadline := time.Now().Add(time.Second)
	for f.handler.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.handler.Active() != 0 {
		t.Fatal("revoked slot leaked")
	}
}
func TestStreamRejectsChangedAuthorityAndUnpinnedPolicy(t *testing.T) {
	var called atomic.Int32
	f := transportTest(t, DefaultLimits(), func(context.Context, Scope, Control, net.Conn) { called.Add(1) })
	for _, mutate := range []func(*Control){func(q *Control) { q.Revision++ }, func(q *Control) { q.Pair = strings.Repeat("d", 64) }, func(q *Control) { q.Source = strings.Repeat("e", 32) }, func(q *Control) { q.Account = "other" }, func(q *Control) { q.Resources.CPU = 3 }} {
		q := f.q
		mutate(&q)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := Dial(ctx, f.address, f.policy, q, DefaultLimits())
		cancel()
		if err == nil {
			t.Fatal("stale authority admitted")
		}
	}
	bad := f.policy.Clone()
	bad.InsecureSkipVerify = true
	if _, err := Dial(context.Background(), f.address, bad, f.q, DefaultLimits()); err == nil {
		t.Fatal("unverified TLS accepted")
	}
	bad = f.policy.Clone()
	bad.Certificates = nil
	noCert, stop := context.WithTimeout(context.Background(), time.Second)
	if _, err := Dial(noCert, f.address, bad, f.q, DefaultLimits()); err == nil {
		t.Fatal("missing client identity admitted")
	}
	stop()
	bad = f.policy.Clone()
	bad.ServerName = "wrong.arm.invalid"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Dial(ctx, f.address, bad, f.q, DefaultLimits()); err == nil {
		t.Fatal("wrong SAN accepted")
	}
	if called.Load() != 0 {
		t.Fatal("rejected stream invoked handler")
	}
}
func TestStreamOriginPathAndProtocolClosed(t *testing.T) {
	f := transportTest(t, DefaultLimits(), func(context.Context, Scope, Control, net.Conn) { t.Error("invalid upgrade reached handler") })
	client := &http.Client{Transport: pinnedTransport(f.address, f.policy, DefaultLimits())}
	for _, q := range []struct {
		path      string
		headers   http.Header
		protocols []string
	}{{Path, http.Header{"Origin": {"https://example.invalid"}}, []string{Protocol}}, {Path, http.Header{"Cookie": {"x=y"}}, []string{Protocol}}, {Path, http.Header{"Authorization": {"Bearer local-token"}}, []string{Protocol}}, {Path + "?token=x", nil, []string{Protocol}}, {"/v1/mesh", nil, []string{Protocol}}, {Path, nil, []string{Protocol, "other"}}, {Path, nil, []string{"other"}}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, err := websocket.Dial(ctx, "wss://"+f.address+q.path, &websocket.DialOptions{HTTPClient: client, HTTPHeader: q.headers, Subprotocols: q.protocols})
		cancel()
		if c != nil {
			c.CloseNow()
		}
		if err == nil {
			t.Fatal("invalid upgrade accepted", q)
		}
	}
}
func TestStreamPendingHandshakeSlotsAreBoundedAndExpire(t *testing.T) {
	l := DefaultLimits()
	l.Global = 1
	l.PerPeer = 1
	l.Handshake = 100 * time.Millisecond
	f := transportTest(t, l, func(context.Context, Scope, Control, net.Conn) { t.Error("missing control invoked handler") })
	client := &http.Client{Transport: pinnedTransport(f.address, f.policy, DefaultLimits())}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pending, _, err := websocket.Dial(ctx, "wss://"+f.address+Path, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Protocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer pending.CloseNow()
	if _, err = Dial(ctx, f.address, f.policy, f.q, l); err == nil {
		t.Fatal("second pending stream admitted")
	}
	deadline := time.Now().Add(time.Second)
	for f.handler.Active() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.handler.Active() != 0 {
		t.Fatal("pending handshake slot leaked")
	}
}
func TestStreamIdleAndByteLimitsCloseTransport(t *testing.T) {
	for _, kind := range []string{"idle", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			l := DefaultLimits()
			l.Idle = 80 * time.Millisecond
			l.BytesPerDirection = 8
			if kind == "bytes" {
				l.Idle = time.Second
			}
			f := transportTest(t, l, func(ctx context.Context, _ Scope, _ Control, c net.Conn) { _, _ = io.Copy(c, c) })
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c, err := Dial(ctx, f.address, f.policy, f.q, l)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if kind == "bytes" {
				n, err := c.Write([]byte("0123456789"))
				if n != 8 || err == nil {
					t.Fatal("byte cap not exact", n, err)
				}
			} else {
				_ = c.SetReadDeadline(time.Now().Add(time.Second))
				if _, err = c.Read(make([]byte, 1)); err == nil {
					t.Fatal("idle stream stayed open")
				}
			}
		})
	}
}

func TestStreamAuthorizationExpiryAndNonblockingRevocation(t *testing.T) {
	for _, kind := range []string{"expiry", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			f := transportTest(t, DefaultLimits(), func(ctx context.Context, _ Scope, _ Control, c net.Conn) { <-ctx.Done() })
			authorize := f.handler.Authorize
			if kind == "expiry" {
				f.handler.Authorize = func(ctx context.Context, cert []byte) (Scope, error) {
					s, err := authorize(ctx, cert)
					s.ValidUntil = time.Now().Add(60 * time.Millisecond)
					return s, err
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			c, err := Dial(ctx, f.address, f.policy, f.q, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if kind == "revoke" {
				start := time.Now()
				f.handler.ClosePeer(f.q.Source)
				if time.Since(start) > 100*time.Millisecond {
					t.Fatal("revocation waited for peer close handshake")
				}
			}
			_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			if _, err = c.Read(make([]byte, 1)); err == nil {
				t.Fatal("expired/revoked transport stayed open")
			}
		})
	}
}

func TestStreamMalformedControlNeverReachesSSH(t *testing.T) {
	var calls atomic.Int32
	l := DefaultLimits()
	l.Handshake = 200 * time.Millisecond
	f := transportTest(t, l, func(context.Context, Scope, Control, net.Conn) { calls.Add(1) })
	client := &http.Client{Transport: pinnedTransport(f.address, f.policy, l)}
	for _, test := range []struct {
		kind websocket.MessageType
		raw  []byte
	}{{websocket.MessageText, []byte(`{}`)}, {websocket.MessageBinary, []byte(`{"version":1,"execute":"untrusted-runtime-hook"}`)}, {websocket.MessageBinary, []byte(strings.Repeat("x", MaxControlBytes+1))}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, err := websocket.Dial(ctx, "wss://"+f.address+Path, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Protocol}})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		_ = c.Write(ctx, test.kind, test.raw)
		if _, _, err = c.Read(ctx); err == nil {
			t.Fatal("malformed control received successful admission")
		}
		c.CloseNow()
		cancel()
	}
	if calls.Load() != 0 {
		t.Fatal("untrusted wire data reached execution boundary")
	}
}

func TestStreamAuthorizationCannotMisbindAuthenticatedCertificate(t *testing.T) {
	for _, kind := range []string{"fingerprint", "source"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			f := transportTest(t, DefaultLimits(), func(context.Context, Scope, Control, net.Conn) { calls.Add(1) })
			authorize := f.handler.Authorize
			f.handler.Authorize = func(ctx context.Context, cert []byte) (Scope, error) {
				s, err := authorize(ctx, cert)
				if kind == "source" {
					s.Source = strings.Repeat("e", 32)
				} else {
					s.Certificate = strings.Repeat("e", 64)
				}
				return s, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := Dial(ctx, f.address, f.policy, f.q, DefaultLimits()); err == nil {
				t.Fatal("authorization scope misbound TLS certificate")
			}
			if calls.Load() != 0 {
				t.Fatal("misbound authority reached SSH")
			}
		})
	}
}

func TestStreamMessageLimitSurvivesNetConnAndFragments(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "fragmented"}[fragmented], func(t *testing.T) {
			var delivered atomic.Int64
			done := make(chan error, 1)
			f := transportTest(t, DefaultLimits(), func(ctx context.Context, _ Scope, _ Control, c net.Conn) {
				var buf [4096]byte
				for {
					n, err := c.Read(buf[:])
					delivered.Add(int64(n))
					if err != nil {
						done <- err
						return
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client := &http.Client{Transport: pinnedTransport(f.address, f.policy, DefaultLimits())}
			c, _, err := websocket.Dial(ctx, "wss://"+f.address+Path, &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Protocol}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			raw, _ := json.Marshal(f.q)
			if err = c.Write(ctx, websocket.MessageBinary, raw); err != nil {
				t.Fatal(err)
			}
			if _, _, err = c.Read(ctx); err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, MaxFrameBytes+1)
			if fragmented {
				w, err := c.Writer(ctx, websocket.MessageBinary)
				if err != nil {
					t.Fatal(err)
				}
				for len(payload) > 0 {
					n := min(len(payload), 4096)
					if _, err = w.Write(payload[:n]); err != nil {
						break
					}
					payload = payload[n:]
					time.Sleep(time.Millisecond)
				}
				_ = w.Close()
			} else {
				_ = c.Write(ctx, websocket.MessageBinary, payload)
			}
			// Read the close concurrently: a limit failure must arrive before the outer
			// timeout and before the server delivers the excess payload byte.
			_, _, readErr := c.Read(ctx)
			if readErr == nil || ctx.Err() != nil {
				t.Fatal("oversized SSH message was not promptly rejected", readErr)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("oversized stream handler stayed active")
			}
			if delivered.Load() > MaxFrameBytes {
				t.Fatal("NetConn disabled message bound", delivered.Load())
			}
		})
	}
}

func TestClientMessageLimitSurvivesNetConn(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "fragmented"}[fragmented], func(t *testing.T) {
			f := transportTest(t, DefaultLimits(), func(context.Context, Scope, Control, net.Conn) {})
			malicious := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}, CompressionMode: websocket.CompressionDisabled})
				if err != nil {
					return
				}
				defer ws.CloseNow()
				ctx, cancel := context.WithTimeout(r.Context(), time.Second)
				defer cancel()
				_, raw, err := ws.Read(ctx)
				if err != nil {
					return
				}
				q, err := decodeControl(raw)
				if err != nil {
					return
				}
				ack, _ := json.Marshal(q.Ready())
				if ws.Write(ctx, websocket.MessageBinary, ack) != nil {
					return
				}
				payload := make([]byte, MaxFrameBytes+1)
				if fragmented {
					writer, err := ws.Writer(ctx, websocket.MessageBinary)
					if err != nil {
						return
					}
					for len(payload) > 0 {
						n := min(len(payload), 4096)
						if _, err = writer.Write(payload[:n]); err != nil {
							break
						}
						payload = payload[n:]
					}
					_ = writer.Close()
				} else {
					_ = ws.Write(ctx, websocket.MessageBinary, payload)
				}
				_, _, _ = ws.Read(ctx)
			}))
			malicious.TLS = f.b.ServerTLS()
			malicious.StartTLS()
			defer malicious.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c, err := Dial(ctx, malicious.Listener.Addr().String(), f.policy, f.q, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			raw, err := io.ReadAll(c)
			if err == nil || len(raw) > MaxFrameBytes || ctx.Err() != nil {
				t.Fatal("client oversized message not bounded", len(raw), err, ctx.Err())
			}
		})
	}
}

func TestStreamLargestLegitimateMessageIsNotRejected(t *testing.T) {
	f := transportTest(t, DefaultLimits(), func(ctx context.Context, _ Scope, _ Control, c net.Conn) { _, _ = io.Copy(c, c) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := Dial(ctx, f.address, f.policy, f.q, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	payload := []byte(strings.Repeat("k", MaxPayloadBytes))
	if n, err := c.Write(payload); err != nil || n != len(payload) {
		t.Fatal("largest valid payload rejected", n, err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(c, got); err != nil || string(got) != string(payload) {
		t.Fatal("valid maximum message not delivered", err)
	}
}

// A peer can send an invalid text message then refuse the close handshake.
// coder's NetConn performs that protocol-error close synchronously, so neither
// its context nor CloseNow alone is sufficient to bound forced shutdown.
func TestServerTextFrameCannotDelayPeerRevocation(t *testing.T) {
	done := make(chan struct{})
	f := transportTest(t, DefaultLimits(), func(_ context.Context, _ Scope, _ Control, c net.Conn) {
		defer close(done)
		_, _ = io.Copy(io.Discard, c)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tr := pinnedTransport(f.address, f.policy, DefaultLimits())
	defer tr.CloseIdleConnections()
	ws, _, err := websocket.Dial(ctx, "wss://"+f.address+Path, &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}, Subprotocols: []string{Protocol}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	raw, _ := json.Marshal(f.q)
	if err = ws.Write(ctx, websocket.MessageBinary, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ws.Read(ctx); err != nil {
		t.Fatal(err)
	}
	if err = ws.Write(ctx, websocket.MessageText, []byte("invalid SSH data")); err != nil {
		t.Fatal(err)
	}
	// Do not read/acknowledge the peer's protocol close.
	time.Sleep(30 * time.Millisecond)
	closed := make(chan struct{})
	go func() { f.handler.ClosePeer(f.q.Source); close(closed) }()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("revocation waited for malicious close handshake")
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("text frame read survived revoked underlying socket")
	}
}

func TestClientTextFrameCannotDelayCancellation(t *testing.T) {
	f := transportTest(t, DefaultLimits(), func(context.Context, Scope, Control, net.Conn) {})
	sent := make(chan struct{})
	release := make(chan struct{})
	malicious := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Protocol}})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return
		}
		q, err := decodeControl(raw)
		if err != nil {
			return
		}
		ack, _ := json.Marshal(q.Ready())
		if ws.Write(ctx, websocket.MessageBinary, ack) != nil {
			return
		}
		if ws.Write(ctx, websocket.MessageText, []byte("invalid SSH data")) != nil {
			return
		}
		close(sent)
		<-release // Deliberately never read or acknowledge the protocol close.
	}))
	malicious.TLS = f.b.ServerTLS()
	malicious.StartTLS()
	defer malicious.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := Dial(ctx, malicious.Listener.Addr().String(), f.policy, f.q, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	done := make(chan error, 1)
	go func() { _, err := c.Read(make([]byte, 1)); done <- err }()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("malicious text not sent")
	}
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("text frame became SSH bytes")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancel waited for malicious close handshake")
	}
}

func TestIdleUsesMonotonicElapsedActivityBoundary(t *testing.T) {
	// The injected clock exposes elapsed time only: a wall-clock correction has
	// no input into either activity timestamps or the idle comparison.
	var elapsed atomic.Int64
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newBounded(a, DefaultLimits(), cancel, nil)
	c.elapsed = func() time.Duration { return time.Duration(elapsed.Load()) }
	defer c.Close()
	elapsed.Store(int64(39 * time.Second))
	if got := c.idleFor(); got != 39*time.Second {
		t.Fatal("initial monotonic duration", got)
	}
	received := make(chan error, 1)
	go func() { _, err := io.ReadFull(b, make([]byte, 1)); received <- err }()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	elapsed.Store(int64(78 * time.Second))
	if got := c.idleFor(); got != 39*time.Second {
		t.Fatal("activity did not reset elapsed idle", got)
	}
	elapsed.Store(int64(79 * time.Second))
	if got := c.idleFor(); got != 40*time.Second {
		t.Fatal("exact idle boundary changed", got)
	}
	limits := DefaultLimits()
	limits.Idle = 40 * time.Second
	go c.watch(ctx, limits, nil)
	select {
	case <-ctx.Done():
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("monotonic idle boundary did not close connection")
	}
}
