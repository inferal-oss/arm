package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshforward"
)

func forwardTLSHome(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state")
}

func TestForwardLocalTLSPreservesPrefetchedReadyBytes(t *testing.T) {
	home := forwardTLSHome(t)
	store, err := mesh.Init(home, strings.Repeat("d", 32), "source", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	serverTLS, err := store.LocalForwardServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	token := strings.Repeat("e", 64)
	joined := make(chan error, 1)
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			joined <- acceptErr
			return
		}
		server := tls.Server(raw, serverTLS)
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(5 * time.Second))
		if handshakeErr := server.Handshake(); handshakeErr != nil {
			joined <- handshakeErr
			return
		}
		line, readErr := bufio.NewReader(server).ReadString('\n')
		if readErr != nil || line != "ARM-FORWARD/2 "+token+"\n" {
			joined <- errors.New("local bearer did not arrive after pinned TLS handshake")
			return
		}
		_, writeErr := io.WriteString(server, "ARM-FORWARD/2 READY\nbody")
		joined <- writeErr
	}()
	client, err := dialForwardSourceTLS(context.Background(), home, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = confirmForwardSource(context.Background(), client, token); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, len("body"))
	if _, err = io.ReadFull(client, body); err != nil || string(body) != "body" {
		t.Fatalf("service bytes following READY were lost: %q %v", body, err)
	}
	select {
	case err = <-joined:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pinned source did not join")
	}
}

func TestForwardLocalTLSHandshakeCancellation(t *testing.T) {
	home := forwardTLSHome(t)
	store, err := mesh.Init(home, strings.Repeat("f", 32), "source", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		conn, dialErr := dialForwardSourceTLS(ctx, home, listener.Addr().String())
		if conn != nil {
			_ = conn.Close()
		}
		finished <- dialErr
	}()
	replacement, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	_ = replacement.SetReadDeadline(time.Now().Add(5 * time.Second))
	hello := make([]byte, 4096)
	n, err := replacement.Read(hello)
	if err != nil || n == 0 || hello[0] != 0x16 {
		t.Fatalf("client did not reach TLS handshake before cancellation: %d %v", n, err)
	}
	cancel()
	select {
	case err = <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake cancellation was not retained: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled TLS handshake did not stop")
	}
}

func TestForwardLocalPortHandoffNeverExposesBearer(t *testing.T) {
	const payload = "application-payload-sentinel"
	token := strings.Repeat("a", 64)
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	home := forwardTLSHome(t)
	store, err := mesh.Init(home, strings.Repeat("b", 32), "source", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	serverTLS, err := store.LocalForwardServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	served := make(chan error, 1)
	go func() {
		local, acceptErr := listener.Accept()
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		served <- meshforward.ServeLocalCompatibleTLS(context.Background(), local, serverTLS, digest, func(context.Context) (net.Conn, error) {
			proxy, service := net.Pipe()
			go func() {
				defer service.Close()
				request := make([]byte, len(payload))
				if _, readErr := io.ReadFull(service, request); readErr == nil && string(request) == payload {
					_, _ = io.WriteString(service, "accepted")
				}
			}()
			return proxy, nil
		})
	}()
	client, err := dialForwardSourceTLS(context.Background(), home, address)
	if err != nil {
		t.Fatalf("owned source certificate refused: %v", err)
	}
	if err = confirmForwardSource(context.Background(), client, token); err != nil {
		t.Fatalf("owned source did not confirm destination admission: %v", err)
	}
	if _, err = io.WriteString(client, payload); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("accepted"))
	if _, err = io.ReadFull(client, response); err != nil || string(response) != "accepted" {
		t.Fatalf("owned source did not bridge application bytes: %q %v", response, err)
	}
	_ = client.Close()
	_ = listener.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("owned source did not join")
	}

	// The authenticated backend could have returned this address just before
	// the original listener closed. A replacement now owns the exact port. It
	// can send a forged READY but cannot authenticate TLS.
	attacker, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("native exact-port handoff unavailable: %v", err)
	}
	defer attacker.Close()
	observed := make(chan []byte, 1)
	go func() {
		conn, acceptErr := attacker.Accept()
		if acceptErr != nil {
			observed <- nil
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		block := make([]byte, 4096)
		n, _ := conn.Read(block)
		observed <- append([]byte(nil), block[:n]...)
		_, _ = io.WriteString(conn, "ARM-FORWARD/2 READY\n")
	}()
	if stolen, err := dialForwardSourceTLS(context.Background(), home, address); err == nil {
		_ = stolen.Close()
		t.Fatal("forged READY accepted after exact-port handoff")
	}
	select {
	case first := <-observed:
		if len(first) == 0 || bytes.Contains(first, []byte(token)) || bytes.Contains(first, []byte(payload)) || bytes.Contains(first, []byte("ARM-FORWARD/2")) {
			t.Fatalf("bearer or application bytes reached replacement listener: %q", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement listener did not observe TLS handshake")
	}
}

func TestForwardLocalRejectsDifferentCertificateForSameHost(t *testing.T) {
	host := strings.Repeat("c", 32)
	home := forwardTLSHome(t)
	owned, err := mesh.Init(home, host, "owned", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owned.Close() })
	other, err := mesh.Init(forwardTLSHome(t), host, "other", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	otherTLS, err := other.LocalForwardServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		raw, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		conn := tls.Server(raw, otherTLS)
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_ = conn.Handshake()
	}()
	if conn, err := dialForwardSourceTLS(context.Background(), home, listener.Addr().String()); err == nil {
		_ = conn.Close()
		t.Fatal("different certificate for same host accepted")
	}
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement TLS server did not join")
	}
}
