package meshsshrelay

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/workspace"
)

func identity(i mesh.Identity) workspace.Identity {
	return workspace.Identity{Host: i.Host, Name: i.Name, Certificate: i.Certificate, Fingerprint: i.Fingerprint, SPKI: i.SPKI, Expires: i.Expires}
}

type routeFixture struct {
	a, b, c                   *mesh.Store
	relay, destination        *Handler
	relayServer, targetServer *httptest.Server
	open                      Open
	relayDial                 atomic.Int32
	finalSource               chan string
	relayBytes                atomic.Int64
	relaySeen                 sync.Map
}

func routeTransportFixture(t *testing.T) *routeFixture {
	t.Helper()
	f := &routeFixture{finalSource: make(chan string, 4)}
	listenerA, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	listenerC, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	makeNode := func(host, name, address string) *mesh.Store {
		s, e := mesh.InitPrepared(filepath.Join(t.TempDir(), name), strings.Repeat(host, 32), name, address, address)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	f.a = makeNode("a", "relay", listenerA.Addr().String())
	f.b = makeNode("b", "origin", "127.0.0.1:40001")
	f.c = makeNode("c", "destination", listenerC.Addr().String())
	for _, edge := range [][2]*mesh.Store{{f.a, f.b}, {f.a, f.c}} {
		pair := strings.Repeat("e", 64)
		if edge[1] == f.c {
			pair = strings.Repeat("f", 64)
		}
		for _, nodes := range [][2]*mesh.Store{{edge[0], edge[1]}, {edge[1], edge[0]}} {
			if _, e = nodes[0].Pair(nodes[1].Config().Identity, mesh.Grant{Permissions: []string{"observe-host"}}, pair, "isolated routed carrier transport"); e != nil {
				t.Fatal(e)
			}
			if e = nodes[0].Prove(nodes[1].Config().Identity.Host, pair); e != nil {
				t.Fatal(e)
			}
			if e = nodes[0].Activate(nodes[1].Config().Identity.Host, pair); e != nil {
				t.Fatal(e)
			}
		}
	}
	a, b, c := identity(f.a.Config().Identity), identity(f.b.Config().Identity), identity(f.c.Config().Identity)
	incoming, _ := f.a.Resolve(b.Host)
	f.open = Open{Workspace: strings.Repeat("1", 32), Generation: strings.Repeat("2", 32), Route: strings.Repeat("3", 32), Source: b.Host, Target: c.Host, SourceGeneration: 1, TargetGeneration: 1, DirectoryRevision: 7, DirectoryPin: a.Fingerprint, DirectorySPKI: a.SPKI, SourcePin: b.Fingerprint, TargetPin: c.Fingerprint, SSHHostKeyPin: strings.Repeat("6", 64), Path: []string{b.Host, a.Host, c.Host}, Hop: 1, Class: ClassSSH, Expires: time.Now().Add(10 * time.Second), Pair: incoming.Pair, EnrollmentRevision: incoming.Revision}
	makeHandler := func(store *mesh.Store) *Handler {
		h := &Handler{}
		h.AuthorizeOuter = func(ctx context.Context, raw []byte) (Outer, error) {
			p, e := store.PeerByCert(raw)
			return Outer{Source: p.Identity.Host, Target: store.Config().Identity.Host, Pair: p.Pair, Revision: p.Revision, Certificate: p.Identity.Fingerprint, Expires: time.Now().Add(time.Minute)}, e
		}
		h.Authorize = func(ctx context.Context, outer Outer, o Open) (Permit, error) {
			if o.Workspace != f.open.Workspace || o.Generation != f.open.Generation || o.Source != b.Host || o.Target != c.Host {
				return Permit{}, fmt.Errorf("test ledger binding differs")
			}
			p := Permit{Local: identity(store.Config().Identity), Origin: b, Destination: c, Expires: o.Expires, TransitRate: 64 << 20}
			if o.Final() {
				var e error
				p.Inner, e = store.RoutedSSHTLS(c, b, true)
				return p, e
			}
			return p, nil
		}
		h.ReserveTransit = func(_ context.Context, life context.Context, _ <-chan struct{}, _ Outer, _ Open, _ Permit) (TransitReservation, error) {
			return TransitReservation{Context: life, CheckActive: func(ctx context.Context) error { return ctx.Err() }, End: func() {}, Finish: func() {}}, nil
		}
		h.Revalidate = func(ctx context.Context, o Outer, open Open, p Permit) error {
			peer, e := store.Resolve(o.Source)
			if e != nil {
				return e
			}
			if peer.State != "active" || !peer.Proven || peer.Pair != o.Pair || peer.Revision != o.Revision || peer.Identity.Fingerprint != o.Certificate {
				return fmt.Errorf("hop revoked")
			}
			return store.WorkspaceIdentityCurrent(p.Local)
		}
		h.Forward = func(ctx context.Context, outer Outer, o Open, p Permit, previous net.Conn) {
			// This deliberately fixed fixture mirrors production's registry-only next
			// hop. It never accepts an address, URL, port or generic RPC from control.
			next, e := store.Resolve(o.Path[o.Hop+1])
			if e != nil {
				return
			}
			remote, _ := f.c.Resolve(store.Config().Identity.Host)
			forward, e := o.Forward(time.Now(), NextAuthority{Local: store.Config().Identity.Host, DirectPeer: next.Identity.Host, Pair: next.Pair, EnrollmentRevision: remote.Revision, Workspace: o.Workspace, Generation: o.Generation, SSHRelayPermit: true, ValidUntil: time.Now().Add(time.Minute)})
			if e != nil {
				return
			}
			config, e := store.ClientTLS(next)
			if e != nil {
				return
			}
			f.relayDial.Add(1)
			conn, e := DialCarrier(ctx, next.Identity.Address, config, forward, func(context.Context) error { return h.Revalidate(ctx, outer, o, p) })
			if e != nil {
				return
			}
			defer conn.Close()
			// Capture only to demonstrate that the relay sees opaque inner records.
			first := make(chan struct{})
			go func() {
				_, _ = io.Copy(conn, &routeCapture{Reader: previous, bytes: &f.relayBytes, seen: &f.relaySeen})
				_ = conn.Close()
				close(first)
			}()
			_, _ = io.Copy(previous, conn)
			_ = previous.Close()
			_ = conn.Close()
			<-first
		}
		h.Final = func(ctx context.Context, outer Outer, o Open, p Permit, inner *tls.Conn) {
			f.finalSource <- inner.ConnectionState().PeerCertificates[0].DNSNames[0]
			var raw [64]byte
			n, e := io.ReadFull(inner, raw[:])
			if e != nil {
				return
			}
			_, _ = inner.Write(raw[:n])
		}
		store.OnChange(h.ClosePeer)
		return h
	}
	f.relay = makeHandler(f.a)
	f.destination = makeHandler(f.c)
	makeServer := func(listener net.Listener, store *mesh.Store, h *Handler) *httptest.Server {
		s := httptest.NewUnstartedServer(h)
		s.Listener.Close()
		s.Listener = listener
		s.TLS = store.ServerTLS()
		s.StartTLS()
		t.Cleanup(func() { h.Stop(); h.Wait(); s.Close() })
		return s
	}
	f.relayServer = makeServer(listenerA, f.a, f.relay)
	f.targetServer = makeServer(listenerC, f.c, f.destination)
	return f
}

type routeCapture struct {
	io.Reader
	bytes *atomic.Int64
	seen  *sync.Map
}

func (c *routeCapture) Read(p []byte) (int, error) {
	n, e := c.Reader.Read(p)
	c.bytes.Add(int64(n))
	if n > 0 {
		c.seen.Store(string(append([]byte{}, p[:n]...)), true)
	}
	return n, e
}
func (f *routeFixture) dial(ctx context.Context, o Open) (*tls.Conn, error) {
	peer, e := f.b.Resolve(f.a.Config().Identity.Host)
	if e != nil {
		return nil, e
	}
	outer, e := f.b.ClientTLS(peer)
	if e != nil {
		return nil, e
	}
	inner, e := f.b.RoutedSSHTLS(identity(f.b.Config().Identity), identity(f.c.Config().Identity), false)
	if e != nil {
		return nil, e
	}
	return Dial(ctx, peer.Identity.Address, outer, inner, o, func(context.Context) error { return nil })
}
func TestThreeHopOpaqueNestedTLSOriginPreservedWithoutDirectPeer(t *testing.T) {
	f := routeTransportFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, e := f.dial(ctx, f.open)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	body := []byte(strings.Repeat("opaque private routed body ", 4)[:64])
	if _, e = conn.Write(body); e != nil {
		t.Fatal(e)
	}
	got := make([]byte, len(body))
	if _, e = io.ReadFull(conn, got); e != nil || string(got) != string(body) {
		t.Fatal(string(got), e)
	}
	select {
	case origin := <-f.finalSource:
		if origin != mesh.SAN(f.b.Config().Identity.Host) {
			t.Fatal("relay replaced original source", origin)
		}
	case <-time.After(time.Second):
		t.Fatal("final handler not reached")
	}
	if f.relayDial.Load() != 1 || f.relayBytes.Load() == 0 {
		t.Fatal("relay path not used")
	}
	f.relaySeen.Range(func(key, value any) bool {
		if strings.Contains(key.(string), string(body)) {
			t.Error("relay observed plaintext")
		}
		return true
	})
	if _, e = f.b.Resolve(f.c.Config().Identity.Host); e == nil {
		t.Fatal("origin gained direct target")
	}
	if _, e = f.c.Resolve(f.b.Config().Identity.Host); e == nil {
		t.Fatal("destination gained direct origin")
	}
}
func TestRoutedCarrierRejectsLoopsForeignClassesAndRouteReplay(t *testing.T) {
	f := routeTransportFixture(t)
	for _, mutate := range []func(*Open){func(o *Open) { o.Path = []string{o.Source, o.Path[1], o.Source, o.Target} }, func(o *Open) { o.Class = "ssh-exec" }, func(o *Open) { o.Class = "acquire" }, func(o *Open) { o.Class = "native" }, func(o *Open) { o.Hop = 0 }, func(o *Open) { o.Expires = time.Now().Add(time.Hour) }} {
		o := f.open
		mutate(&o)
		if e := o.Validate(time.Now()); e == nil {
			t.Fatal("invalid route accepted", o)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	conn, e := f.dial(ctx, f.open)
	if e != nil {
		t.Fatal(e)
	}
	conn.Close()
	if conn, e = f.dial(ctx, f.open); e == nil {
		conn.Close()
		t.Fatal("same routed stream ID replayed after closure")
	}
}
func TestRoutedCarrierImmediateRevocationAbortsRelayAndFinal(t *testing.T) {
	f := routeTransportFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, e := f.dial(ctx, f.open)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if e = f.a.Revoke(f.b.Config().Identity.Host, "private relay edge revocation"); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, e := conn.Read(make([]byte, 1)); done <- e }()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("revoked route readable")
		}
	case <-time.After(time.Second):
		t.Fatal("edge revocation did not abort stream")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("slow relay cancellation")
	}
}
