package rsyncengine

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type capHandshakeConn struct {
	net.Conn
	first     sync.Once
	closeOnce sync.Once
	ready     chan struct{}
	gate      <-chan struct{}
	closed    chan struct{}
}

func (c *capHandshakeConn) Read(p []byte) (int, error) {
	c.first.Do(func() {
		close(c.ready)
		select {
		case <-c.gate:
		case <-c.closed:
		}
	})
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	return c.Conn.Read(p)
}
func (c *capHandshakeConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestStagingCapReviewSnapshotsLocalPolicyBeforeHandshake(t *testing.T) {
	for _, tc := range []struct {
		name             string
		receiver, sender Role
	}{{"server-receiver", ServerReceiver, ClientSender}, {"client-receiver", ClientReceiver, ServerSender}} {
		t.Run(tc.name, func(t *testing.T) {
			source, stage := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "file"), []byte("private staged bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			a, b := net.Pipe()
			gate := make(chan struct{})
			ready := make(chan struct{})
			wrapped := &capHandshakeConn{Conn: b, ready: ready, gate: gate, closed: make(chan struct{})}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var limit int64
			receiverDone, senderDone := make(chan error, 1), make(chan error, 1)
			go func() {
				_, err := Run(ctx, wrapped, tc.receiver, stage, Metadata{ReceiverWriteLimit: &limit})
				receiverDone <- err
			}()
			go func() { _, err := Run(ctx, a, tc.sender, source, Metadata{}); senderDone <- err }()
			receiverJoined, senderJoined := false, false
			defer func() {
				cancel()
				wrapped.Close()
				a.Close()
				if !receiverJoined {
					<-receiverDone
				}
				if !senderJoined {
					<-senderDone
				}
			}()
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("original receiver did not reach delayed handshake")
			}
			// The receiver has read its policy and is blocked on the original stream.
			// Channel release orders this mutation before any later protocol reads.
			limit = 1 << 20
			close(gate)
			err := <-receiverDone
			receiverJoined = true
			<-senderDone
			senderJoined = true
			if err == nil || !strings.Contains(err.Error(), "receiver write limit exceeded") {
				t.Fatal("later caller mutation expanded captured receiver policy", err)
			}
			entries, err := os.ReadDir(stage)
			if err != nil || len(entries) != 0 {
				t.Fatal("zero captured allowance published stage bytes", len(entries), err)
			}
		})
	}
}
