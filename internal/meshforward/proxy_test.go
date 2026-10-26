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
	p := &Proxy{Dial: func(_ context.Context, _, address string) (net.Conn, error) { return <-services[address], nil }}
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
	p := &Proxy{Dial: func(_ context.Context, _, _ string) (net.Conn, error) { return <-services, nil }}
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
