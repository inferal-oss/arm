package meshforward

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

var errForwardRouteLost = errors.New("forward route lost after application bytes")

type failAfterFirstRead struct {
	net.Conn
	read bool
}

func (c *failAfterFirstRead) Read(p []byte) (int, error) {
	if c.read {
		return 0, errForwardRouteLost
	}
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.read = true
	}
	return n, err
}

func proxyRequest(g Grant, id string) Request {
	return Request{Version: 1, Workspace: g.Workspace, Generation: g.Generation, Source: g.Source, Target: g.Target, SourcePin: g.SourcePin, TargetPin: g.TargetPin, Service: g.Endpoint.Name, GrantRevision: g.Revision, ConnectionID: id, TTL: 5 * time.Second, RouteBindingHash: strings.Repeat("8", 64)}
}
func proxyIdentity(g Grant) Identity {
	return Identity{SourcePin: g.SourcePin, SourceSPKI: g.SourceSPKI, TargetPin: g.TargetPin, TargetSPKI: g.TargetSPKI, Account: g.Account}
}

func TestProxyCountsIndependentGrants(t *testing.T) {
	g1 := testGrant()
	g1.MaxConnections = 1
	g2 := g1
	g2.Endpoint = Endpoint{Name: "other", Network: "tcp", Address: "127.0.0.1:8081"}
	services := map[string]chan net.Conn{g1.Endpoint.Address: make(chan net.Conn, 1), g2.Endpoint.Address: make(chan net.Conn, 1)}
	p := &Proxy{Dial: func(_ context.Context, _, address string) (net.Conn, error) { return <-services[address], nil }, CheckCredit: func(context.Context, Grant) error { return nil }, Credit: func(_ context.Context, _ Grant, n int) (int, error) { return n, nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var peers []net.Conn
	var done []chan error
	start := func(g Grant, id string) {
		stream, client := net.Pipe()
		service, application := net.Pipe()
		peers = append(peers, client, application)
		services[g.Endpoint.Address] <- service
		ready := make(chan struct{})
		ended := make(chan error, 1)
		go func() {
			ended <- p.Open(ctx, proxyRequest(g, id), proxyIdentity(g), stream, func(context.Context) (Grant, error) { return g, nil }, func() error { close(ready); return nil })
		}()
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("proxy admission timed out")
		}
		done = append(done, ended)
	}
	start(g1, strings.Repeat("7", 32))
	start(g2, strings.Repeat("6", 32))
	if p.Active() != 2 {
		t.Fatalf("independent grants shared one connection ceiling: %d", p.Active())
	}
	stream, client := net.Pipe()
	defer client.Close()
	defer stream.Close()
	err := p.Open(ctx, proxyRequest(g1, strings.Repeat("5", 32)), proxyIdentity(g1), stream, func(context.Context) (Grant, error) { return g1, nil }, func() error { return nil })
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("third same-grant stream admitted: %v", err)
	}
	cancel()
	for _, peer := range peers {
		_ = peer.Close()
	}
	for _, ended := range done {
		select {
		case <-ended:
		case <-time.After(3 * time.Second):
			t.Fatal("proxy workers failed to join")
		}
	}
}

func TestProxySharesRateAcrossConcurrentStreams(t *testing.T) {
	g := testGrant()
	g.MaxConnections = 2
	g.BytesPerSecond = 64 << 10
	services := make(chan net.Conn, 2)
	p := &Proxy{Dial: func(_ context.Context, _, _ string) (net.Conn, error) { return <-services, nil }, CheckCredit: func(context.Context, Grant) error { return nil }, Credit: func(_ context.Context, _ Grant, n int) (int, error) { return n, nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var clients, applications []net.Conn
	var ended []chan error
	for i := 0; i < 2; i++ {
		stream, client := net.Pipe()
		service, application := net.Pipe()
		clients = append(clients, client)
		applications = append(applications, application)
		services <- service
		ready := make(chan struct{})
		done := make(chan error, 1)
		id := strings.Repeat(string(rune('7'+i)), 32)
		go func() {
			done <- p.Open(ctx, proxyRequest(g, id), proxyIdentity(g), stream, func(context.Context) (Grant, error) { return g, nil }, func() error { close(ready); return nil })
		}()
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("proxy admission timed out")
		}
		ended = append(ended, done)
	}
	start := time.Now()
	reads := make(chan error, 2)
	for i := 0; i < 2; i++ {
		client, application := clients[i], applications[i]
		go func() { _, err := client.Write(make([]byte, 64<<10)); reads <- err }()
		go func() { _, err := io.CopyN(io.Discard, application, 64<<10); reads <- err }()
	}
	for i := 0; i < 4; i++ {
		select {
		case err := <-reads:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("paced transfer stalled")
		}
	}
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Fatalf("two streams reset the grant rate: %s", elapsed)
	}
	cancel()
	for _, peer := range clients {
		_ = peer.Close()
	}
	for _, peer := range applications {
		_ = peer.Close()
	}
	for _, done := range ended {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("paced proxy workers failed to join")
		}
	}
}

func TestProxyRouteLossAfterApplicationBytesIsUnknownAndJoined(t *testing.T) {
	g := testGrant()
	stream, client := net.Pipe()
	service, application := net.Pipe()
	defer client.Close()
	defer application.Close()
	p := &Proxy{Dial: func(context.Context, string, string) (net.Conn, error) { return service, nil }, CheckCredit: func(context.Context, Grant) error { return nil }, Credit: func(_ context.Context, _ Grant, n int) (int, error) { return n, nil }}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- p.Open(t.Context(), proxyRequest(g, strings.Repeat("9", 32)), proxyIdentity(g),
			&failAfterFirstRead{Conn: stream}, func(context.Context) (Grant, error) { return g, nil },
			func() error { close(ready); return nil })
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy admission timed out")
	}
	wrote := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("effect"))
		wrote <- err
	}()
	got := make([]byte, len("effect"))
	if _, err := io.ReadFull(application, got); err != nil || string(got) != "effect" {
		t.Fatalf("application did not receive bytes before route loss: %q %v", got, err)
	}
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source write did not settle")
	}
	select {
	case err := <-done:
		if !errors.Is(err, errForwardRouteLost) || !strings.Contains(err.Error(), "outcome unknown") {
			t.Fatalf("route loss after application bytes lost unknown outcome: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not join after route loss")
	}
	if active := p.Active(); active != 0 {
		t.Fatalf("route loss retained %d active streams", active)
	}
}

func TestProxyCreditFailureNeverWritesApplicationBytes(t *testing.T) {
	g := testGrant()
	stream, client := net.Pipe()
	service, application := net.Pipe()
	defer client.Close()
	defer application.Close()
	denied := errors.New("durable credit unavailable")
	p := &Proxy{
		Dial:        func(context.Context, string, string) (net.Conn, error) { return service, nil },
		CheckCredit: func(context.Context, Grant) error { return nil },
		Credit:      func(context.Context, Grant, int) (int, error) { return 0, denied },
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- p.Open(t.Context(), proxyRequest(g, strings.Repeat("a", 32)), proxyIdentity(g), stream,
			func(context.Context) (Grant, error) { return g, nil }, func() error { close(ready); return nil })
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy admission timed out")
	}
	_ = client.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	_ = application.SetReadDeadline(time.Now().Add(3 * time.Second))
	var one [1]byte
	if n, err := application.Read(one[:]); n != 0 || err == nil {
		t.Fatalf("application received uncredited bytes: %d %v", n, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, denied) || !strings.Contains(err.Error(), "outcome unknown") {
			t.Fatalf("credit denial lost unknown outcome: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("credit-denied proxy did not join")
	}
	if p.Active() != 0 {
		t.Fatal("credit-denied proxy retained active stream")
	}
}

func TestPacedWriterUsesExactFinalCredit(t *testing.T) {
	writer, reader := net.Pipe()
	defer writer.Close()
	defer reader.Close()
	pacer, err := newPacer(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	remaining := 4
	w := pacedWriter{ctx: t.Context(), dst: writer, pacer: pacer, check: func() error { return nil },
		credit: func(_ context.Context, n int) (int, error) {
			if remaining == 0 {
				return 0, errors.New("aggregate credit exhausted")
			}
			granted := min(n, remaining)
			remaining -= granted
			return granted, nil
		}}
	done := make(chan struct {
		n   int
		err error
	}, 1)
	go func() {
		n, err := w.Write([]byte("abcde"))
		done <- struct {
			n   int
			err error
		}{n, err}
	}()
	got := make([]byte, 4)
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "abcd" {
		t.Fatalf("final prepaid bytes differ: %q %v", got, err)
	}
	select {
	case result := <-done:
		if result.n != 4 || result.err == nil || remaining != 0 {
			t.Fatalf("writer exceeded final credit: %+v remaining=%d", result, remaining)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer stalled at credit exhaustion")
	}
}
