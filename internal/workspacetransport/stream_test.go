package workspacetransport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/workspace"
)

type wireFixture struct {
	a, b      *mesh.Store
	server    *httptest.Server
	handler   *Handler
	address   string
	outer     *tls.Config
	admission Admission
	open      Open
	request   Request
	handled   atomic.Int32
}

func wireIdentity(v mesh.Identity) workspace.Identity {
	return workspace.Identity{Host: v.Host, Name: v.Name, Certificate: v.Certificate, Fingerprint: v.Fingerprint, SPKI: v.SPKI, Expires: v.Expires}
}
func wireTest(t *testing.T) *wireFixture {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	f := &wireFixture{address: listener.Addr().String()}
	f.a, e = mesh.InitPrepared(filepath.Join(t.TempDir(), "a"), strings.Repeat("a", 32), "source", "127.0.0.1:40001", "127.0.0.1:40001")
	if e != nil {
		t.Fatal(e)
	}
	f.b, e = mesh.InitPrepared(filepath.Join(t.TempDir(), "b"), strings.Repeat("b", 32), "directory", f.address, f.address)
	if e != nil {
		t.Fatal(e)
	}
	pair := strings.Repeat("c", 64)
	for _, s := range [][2]*mesh.Store{{f.a, f.b}, {f.b, f.a}} {
		_, e = s[0].Pair(s[1].Config().Identity, mesh.Grant{Permissions: []string{"observe-host"}}, pair, "private closed protocol test")
		if e != nil {
			t.Fatal(e)
		}
		if e = s[0].Prove(s[1].Config().Identity.Host, pair); e != nil {
			t.Fatal(e)
		}
		if e = s[0].Activate(s[1].Config().Identity.Host, pair); e != nil {
			t.Fatal(e)
		}
	}
	ai, bi := wireIdentity(f.a.Config().Identity), wireIdentity(f.b.Config().Identity)
	incoming, _ := f.b.Resolve(ai.Host)
	outgoing, _ := f.a.Resolve(bi.Host)
	f.outer, e = f.a.ClientTLS(outgoing)
	if e != nil {
		t.Fatal(e)
	}
	policy, e := f.a.WorkspaceTLS(ai, bi, false)
	if e != nil {
		t.Fatal(e)
	}
	f.admission = Admission{Local: ai, Remote: bi, Expires: time.Now().Add(time.Minute), TLS: policy}
	f.open = Open{Version: Version, Workspace: strings.Repeat("d", 32), Generation: strings.Repeat("e", 32), Route: strings.Repeat("f", 32), Source: ai.Host, Target: bi.Host, Path: []string{ai.Host, bi.Host}, Class: ClassAdmissionStatus, Expires: time.Now().Add(10 * time.Second), Pair: pair, Revision: incoming.Revision, Consent: strings.Repeat("1", 64)}
	f.request = Request{Version: Version, Route: f.open.Route, Workspace: f.open.Workspace, Generation: f.open.Generation, Source: ai.Host, Target: bi.Host, Operation: OperationStatus, Status: &workspace.AdmissionStatusRequest{Workspace: f.open.Workspace, Generation: f.open.Generation, Source: ai.Host, Consent: f.open.Consent, Nonce: strings.Repeat("2", 64)}}
	h := &Handler{}
	h.AuthorizeOuter = func(ctx context.Context, der []byte) (Outer, error) {
		p, e := f.b.PeerByCert(der)
		if e != nil {
			return Outer{}, e
		}
		return Outer{Source: p.Identity.Host, Target: bi.Host, Pair: p.Pair, Certificate: p.Identity.Fingerprint, Revision: p.Revision, Expires: p.Identity.Expires}, nil
	}
	h.Authorize = func(ctx context.Context, o Outer, c Open) (Admission, error) {
		p, e := f.b.WorkspaceTLS(bi, ai, true)
		return Admission{Local: bi, Remote: ai, Expires: time.Now().Add(time.Minute), TLS: p}, e
	}
	h.Revalidate = func(ctx context.Context, o Outer, c Open, a Admission) error {
		p, e := f.b.Resolve(o.Source)
		if e != nil {
			return e
		}
		if p.State != "active" || p.Pair != o.Pair || p.Revision != o.Revision {
			return fmt.Errorf("changed")
		}
		return nil
	}
	h.Handle = func(ctx context.Context, o Outer, c Open, a Admission, inner *tls.Conn) {
		q, e := ReadRequest(inner, c)
		if e != nil {
			return
		}
		f.handled.Add(1)
		r := q.Response()
		r.Status = &workspace.AdmissionStatus{Schema: workspace.Schema, Workspace: c.Workspace, Generation: c.Generation, Source: c.Target, Target: c.Source, Consent: c.Consent, Nonce: q.Status.Nonce}
		_ = WriteReply(inner, q, r)
	}
	f.handler = h
	f.b.OnChange(h.ClosePeer)
	server := httptest.NewUnstartedServer(h)
	server.Listener.Close()
	server.Listener = listener
	server.TLS = f.b.ServerTLS()
	server.StartTLS()
	f.server = server
	t.Cleanup(func() { h.ClosePeer(""); server.Close(); f.a.Close(); f.b.Close() })
	return f
}
func TestWorkspaceWireNestedPinnedTLSClosedRecords(t *testing.T) {
	f := wireTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, e := Dial(ctx, f.address, f.outer, f.open, f.admission, func(context.Context) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if e = WriteRequest(conn, f.open, f.request); e != nil {
		t.Fatal(e)
	}
	reply, e := ReadReply(conn, f.request)
	if e != nil || reply.Status == nil || f.handled.Load() != 1 {
		t.Fatal(reply, e, f.handled.Load())
	}
	if conn.ConnectionState().NegotiatedProtocol != InnerProtocol {
		t.Fatal("not inner protocol")
	}
	for _, op := range []string{"acquire", "status", "ssh_account", "message_send", "workspace_decide", "lease-own"} {
		q := f.request
		q.Operation = op
		if e = q.Validate(f.open); e == nil {
			t.Fatal("admitted foreign RPC", op)
		}
	}
}
func TestWorkspaceWireRefusesChangedOuterOrInnerPin(t *testing.T) {
	f := wireTest(t)
	for _, mutate := range []func(*Open){func(o *Open) { o.Revision++ }, func(o *Open) { o.Pair = strings.Repeat("3", 64) }, func(o *Open) { o.Path = append(o.Path, o.Source) }} {
		q := f.open
		mutate(&q)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, e := Dial(ctx, f.address, f.outer, q, f.admission, func(context.Context) error { return nil })
		cancel()
		if e == nil {
			c.Close()
			t.Fatal("changed outer accepted")
		}
	}
	bad := f.admission
	bad.TLS = f.admission.TLS.Clone()
	bad.TLS.ServerName = mesh.SAN(strings.Repeat("4", 32))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c, e := Dial(ctx, f.address, f.outer, f.open, bad, func(context.Context) error { return nil }); e == nil {
		c.Close()
		t.Fatal("wrong inner pin accepted")
	}
	if f.handled.Load() != 0 {
		t.Fatal("unverified inner reached handler")
	}
}
func TestWorkspaceWireCancellationAndDirectRevocationAbort(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			f := wireTest(t)
			entered := make(chan struct{})
			f.handler.Handle = func(ctx context.Context, _ Outer, _ Open, _ Admission, c *tls.Conn) {
				close(entered)
				_, _ = io.Copy(io.Discard, c)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, e := Dial(ctx, f.address, f.outer, f.open, f.admission, func(context.Context) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			<-entered
			start := time.Now()
			if revoke {
				if e = f.b.Revoke(f.open.Source, "explicit private revoke"); e != nil {
					t.Fatal(e)
				}
			} else {
				cancel()
			}
			result := make(chan error, 1)
			go func() { _, e := conn.Read(make([]byte, 1)); result <- e }()
			select {
			case e := <-result:
				if e == nil {
					t.Fatal("read survived cancellation")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not abort captured socket")
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("slow cancellation")
			}
		})
	}
}
func TestWorkspaceClosedWireStrictNullAliasAndBounds(t *testing.T) {
	f := wireTest(t)
	raw, _ := json.Marshal(f.request)
	for _, mutate := range []func([]byte) []byte{func(b []byte) []byte { return []byte(strings.Replace(string(b), `"operation":`, `"Operation":`, 1)) }, func(b []byte) []byte { return append([]byte(`{"proof":null,`), b[1:]...) }, func(b []byte) []byte { return append([]byte(`{"operation":"status",`), b[1:]...) }} {
		var q Request
		if e := workspace.Decode(mutate(raw), &q); e == nil {
			t.Fatal("accepted alias/null/duplicate")
		}
	}
	var b strings.Builder
	if e := writeRecord(&b, make([]byte, MaxRequestBytes+1), MaxRequestBytes); e == nil {
		t.Fatal("oversize outgoing accepted")
	}
	if _, e := ReadRequest(strings.NewReader("\x7f\xff\xff\xff"), f.open); e == nil {
		t.Fatal("oversize incoming accepted")
	}
}

func rawWorkspaceSocket(t *testing.T, f *wireFixture, control bool) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	config := f.outer.Clone()
	config.NextProtos = []string{"http/1.1"}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		raw, e := (&net.Dialer{}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		conn := tls.Client(raw, config)
		if e = conn.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, e
		}
		return conn, nil
	}}
	t.Cleanup(tr.CloseIdleConnections)
	ws, _, e := websocket.Dial(ctx, "wss://"+f.address+Path, &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}, Subprotocols: []string{Protocol}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.CloseNow() })
	if control {
		raw, _ := json.Marshal(f.open)
		if e = ws.Write(ctx, websocket.MessageBinary, raw); e != nil {
			t.Fatal(e)
		}
		if _, _, e = ws.Read(ctx); e != nil {
			t.Fatal(e)
		}
	}
	return ws, ctx
}
func TestWorkspaceWirePendingControlsBoundedAndUnackedTextAbort(t *testing.T) {
	f := wireTest(t)
	for i := 0; i < 8; i++ {
		rawWorkspaceSocket(t, f, false)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if c, e := Dial(ctx, f.address, f.outer, f.open, f.admission, func(context.Context) error { return nil }); e == nil {
		c.Close()
		t.Fatal("pending per-peer cap ignored")
	}
	start := time.Now()
	f.handler.Stop()
	done := make(chan struct{})
	go func() { f.handler.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pending controls did not abort")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("slow stop")
	}
	if c, e := Dial(ctx, f.address, f.outer, f.open, f.admission, func(context.Context) error { return nil }); e == nil {
		c.Close()
		t.Fatal("closed handler accepted new stream")
	}
	// Independent live handler: malicious text after the control does not require
	// its peer to acknowledge a WebSocket close before revocation can finish.
	g := wireTest(t)
	ws, ctx := rawWorkspaceSocket(t, g, true)
	if e := ws.Write(ctx, websocket.MessageText, []byte("not TLS")); e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	start = time.Now()
	g.handler.Stop()
	g.handler.Wait()
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("text frame delayed captured socket abort")
	}
}

type collectingWireConn struct {
	net.Conn
	collect bool
	data    []byte
}

func (c *collectingWireConn) Write(p []byte) (int, error) {
	if c.collect {
		c.data = append(c.data, p...)
		return len(p), nil
	}
	return c.Conn.Write(p)
}
func TestWorkspaceWirePostControlFrameLimitIncludingFragments(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(fmt.Sprint(fragmented), func(t *testing.T) {
			f := wireTest(t)
			done := make(chan int64, 1)
			f.handler.Handle = func(ctx context.Context, _ Outer, _ Open, _ Admission, conn *tls.Conn) {
				n, _ := io.Copy(io.Discard, conn)
				done <- n
			}
			ws, ctx := rawWorkspaceSocket(t, f, true)
			raw := &collectingWireConn{Conn: websocket.NetConn(ctx, ws, websocket.MessageBinary)}
			inner := tls.Client(raw, f.admission.TLS)
			if e := inner.HandshakeContext(ctx); e != nil {
				t.Fatal(e)
			}
			raw.collect = true
			if _, e := inner.Write(make([]byte, MaxFrameBytes*2)); e != nil {
				t.Fatal(e)
			}
			if len(raw.data) <= MaxFrameBytes {
				t.Fatal("fixture did not create oversized message")
			}
			if fragmented {
				writer, e := ws.Writer(ctx, websocket.MessageBinary)
				if e != nil {
					t.Fatal(e)
				}
				for data := raw.data; len(data) > 0; {
					n := min(4096, len(data))
					if _, e = writer.Write(data[:n]); e != nil {
						break
					}
					data = data[n:]
				}
				_ = writer.Close()
			} else {
				_ = ws.Write(ctx, websocket.MessageBinary, raw.data)
			}
			select {
			case n := <-done:
				if n <= 0 || n > MaxFrameBytes {
					t.Fatal("malformed message exceeded read bound", n)
				}
			case <-time.After(time.Second):
				f.handler.Stop()
				t.Fatal("oversized post-control message survived")
			}
		})
	}
}
