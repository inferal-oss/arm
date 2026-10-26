package server

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/meshforward"
)

func TestForwardOriginalOpenerFailureRetainedWithoutReplayOrApplicationDisclosure(t *testing.T) {
	token := strings.Repeat("a", 64)
	digest, err := meshforward.TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, refused := range []bool{false, true} {
		client, local := net.Pipe()
		remote, application := net.Pipe()
		calls := 0
		done := make(chan [2]string, 1)
		go func() {
			first, second := serveForwardLocal(t.Context(), local, digest, func(context.Context) (net.Conn, error) {
				calls++
				if refused {
					return nil, errors.New("original destination admission refused: " + strings.Repeat("bounded", 256))
				}
				return remote, nil
			})
			done <- [2]string{first, second}
		}()
		// Authentication succeeds, but a malformed application URL must never be
		// retained in private diagnostics. The original opener executes only once.
		_, _ = io.WriteString(client, "GET /private-application-value%zz HTTP/1.1\r\nHost: service\r\nProxy-Authorization: Bearer "+token+"\r\n\r\n")
		result := <-done
		client.Close()
		remote.Close()
		application.Close()
		if calls != 1 {
			t.Fatalf("original opener replayed %d times", calls)
		}
		if result[1] != "connection_failed" {
			t.Fatal("connection failure marker missing", result)
		}
		if refused && (len(result[0]) != 1024 || !strings.HasPrefix(result[0], "original destination admission refused:")) {
			t.Fatal("original bounded opener rejection missing", result)
		}
		if !refused && result[0] != "" {
			t.Fatal("application failure misattributed to opener", result)
		}
		if strings.Contains(strings.Join(result[:], " "), "private-application-value") || strings.Contains(strings.Join(result[:], " "), token) {
			t.Fatal("application data or bearer retained", result)
		}
	}
}
