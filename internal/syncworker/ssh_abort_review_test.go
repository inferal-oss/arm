package syncworker

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type stalledSSHReviewConn struct {
	net.Conn
	stalled   atomic.Bool
	entered   chan struct{}
	stopped   chan struct{}
	enterOnce sync.Once
	closeOnce sync.Once
}

func (c *stalledSSHReviewConn) Write(p []byte) (int, error) {
	if c.stalled.Load() {
		c.enterOnce.Do(func() { close(c.entered) })
		<-c.stopped
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}
func (c *stalledSSHReviewConn) Close() error {
	c.closeOnce.Do(func() { close(c.stopped) })
	return c.Conn.Close()
}

func TestStagingCapReviewActualSSHAbortUnblocksStalledWriterAndJoins(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	host, user := signer(t), signer(t)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(user.PublicKey().Marshal()) {
			return nil, errors.New("wrong fixture key")
		}
		return nil, nil
	}}
	config.AddHostKey(host)
	serverDone := make(chan error, 1)
	go func() {
		raw, e := listener.Accept()
		if e != nil {
			serverDone <- e
			return
		}
		conn, channels, requests, e := ssh.NewServerConn(raw, config)
		if e != nil {
			_ = raw.Close()
			serverDone <- e
			return
		}
		defer conn.Close()
		go ssh.DiscardRequests(requests)
		incoming, ok := <-channels
		if !ok {
			serverDone <- errors.New("fixture channel missing")
			return
		}
		ch, reqs, e := incoming.Accept()
		if e != nil {
			serverDone <- e
			return
		}
		go ssh.DiscardRequests(reqs)
		_, _ = io.Copy(io.Discard, ch)
		serverDone <- conn.Wait()
	}()
	raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	wire := &stalledSSHReviewConn{Conn: raw, entered: make(chan struct{}), stopped: make(chan struct{})}
	defer wire.Close()
	conn, channels, requests, err := ssh.NewClientConn(wire, "fixture", &ssh.ClientConfig{User: "fixture", Auth: []ssh.AuthMethod{ssh.PublicKeys(user)}, HostKeyCallback: ssh.FixedHostKey(host.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(conn, channels, requests)
	defer client.Close()
	ch, reqs, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	var closes, joins atomic.Int32
	session, err := newOwnedSingleStreamSession(ch, func() error { joins.Add(1); return client.Wait() }, func() error { closes.Add(1); return wire.Close() })
	if err != nil {
		t.Fatal(err)
	}
	handle, err := session.OpenTransfer(context.Background(), batch{})
	if err != nil {
		t.Fatal(err)
	}
	wire.stalled.Store(true)
	writeDone := make(chan error, 1)
	go func() { _, e := handle.Write([]byte("blocked SSH payload")); writeDone <- e }()
	select {
	case <-wire.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("original SSH writer did not enter deterministic stall")
	}
	abortDone := make(chan error, 1)
	go func() { abortDone <- handle.Abort() }()
	abortBlocked := false
	select {
	case err := <-abortDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		abortBlocked = true
		_ = wire.Close()
		<-abortDone // join the original stalled abort before reporting its failure
	}
	select {
	case <-writeDone:
		// SSH can accept a channel write before its carrier write finishes.
		// Completion of the original blocked workers, not a synthetic write
		// error, is the required abort witness.
	case <-time.After(2 * time.Second):
		t.Fatal("original SSH write did not join")
	}
	_ = handle.Join()
	_ = session.Abort()
	_ = session.Join()
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("original authenticated SSH server did not join")
	}
	if abortBlocked {
		t.Fatal("abort blocked on raw SSH channel close; original carrier cleanup and joins completed")
	}
	if closes.Load() != 1 || joins.Load() != 1 {
		t.Fatalf("carrier close=%d original wait=%d", closes.Load(), joins.Load())
	}
}
