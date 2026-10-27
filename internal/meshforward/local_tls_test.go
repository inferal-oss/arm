package meshforward

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCompatibleLocalTLSKeepsLegacyControlPrefix(t *testing.T) {
	token := strings.Repeat("a", 64)
	digest, err := TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer client.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- ServeLocalCompatibleTLS(context.Background(), server, &tls.Config{NextProtos: []string{"arm.local.forward.v1"}}, digest, func(context.Context) (net.Conn, error) {
			return nil, errors.New("destination refused")
		})
	}()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = io.WriteString(client, "ARM-FORWARD/2 "+token+"\n"); err != nil {
		t.Fatal(err)
	}
	ack := make([]byte, len("ARM-FORWARD/2 REFUSED\n"))
	if _, err = io.ReadFull(client, ack); err != nil || string(ack) != "ARM-FORWARD/2 REFUSED\n" {
		t.Fatalf("legacy control prefix lost while selecting TLS: %q %v", ack, err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("legacy local connection did not join")
	}
}
