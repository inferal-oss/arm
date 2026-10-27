package server

import (
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

func TestForwardSourceTLSKeepsOriginalFailureClassification(t *testing.T) {
	store, err := mesh.Init(filepath.Join(t.TempDir(), "state"), strings.Repeat("a", 32), "source-tls", "127.0.0.1:1", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	config, err := store.LocalForwardServerTLS()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := mesh.LocalForwardClientTLS(store.Config().Identity)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, refused := range []bool{false, true} {
		t.Run(map[bool]string{true: "original-opener-refusal", false: "application-refusal"}[refused], func(t *testing.T) {
			client, local := net.Pipe()
			remote, application := net.Pipe()
			defer client.Close()
			defer remote.Close()
			defer application.Close()
			done := make(chan [2]string, 1)
			calls := 0
			go func() {
				a, b := serveForwardLocalTLS(t.Context(), local, config, digest, func(context.Context) (net.Conn, error) {
					calls++
					if refused {
						return nil, errors.New("original destination admission refused: " + strings.Repeat("bounded", 256))
					}
					return remote, nil
				})
				done <- [2]string{a, b}
			}()
			secure := tls.Client(client, pin)
			_ = secure.SetDeadline(time.Now().Add(5 * time.Second))
			if err := secure.HandshakeContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			_, err := io.WriteString(secure, "GET /private-application-value%zz HTTP/1.1\r\nHost: service\r\nProxy-Authorization: Bearer "+token+"\r\n\r\n")
			if err != nil {
				t.Fatal(err)
			}
			// Consume the TLS close-notify so the original writer can join.
			_, _ = io.Copy(io.Discard, secure)
			var result [2]string
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("original TLS connection did not join")
			}
			if calls != 1 || result[1] != "connection_failed" {
				t.Fatal("original classification or once-only opening lost", calls, result)
			}
			if refused && (len(result[0]) != 1024 || !strings.HasPrefix(result[0], "original destination admission refused:")) {
				t.Fatal("original bounded opener refusal lost", result)
			}
			if !refused && result[0] != "" {
				t.Fatal("application failure attributed to opener", result)
			}
			if strings.Contains(strings.Join(result[:], " "), "private-application-value") || strings.Contains(strings.Join(result[:], " "), token) {
				t.Fatal("private application data retained", result)
			}
		})
	}
}
