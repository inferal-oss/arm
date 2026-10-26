package sshservice

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestOriginalExecutionTransportClosesStalledReadAndCannotBeBorrowed(t *testing.T) {
	user := testKey(t)
	readJoined := make(chan struct{})
	s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(context.Context) error { return nil }, Execute: func(ctx context.Context, _ Execution, ch ssh.Channel) (Process, error) {
		closeOriginal, err := OriginalTransportCloser(ctx, ch)
		if err != nil {
			t.Error(err)
			close(readJoined)
			return nil, err
		}
		if _, err := OriginalTransportCloser(context.Background(), ch); err == nil {
			t.Error("borrowed closer outside original context")
		}
		if _, err := OriginalTransportCloser(ctx, nil); err == nil {
			t.Error("borrowed closer for another channel")
		}
		if _, err := OriginalTransportCloser(ctx, &struct{ ssh.Channel }{ch}); err == nil {
			t.Error("borrowed closer through a substitute channel wrapper")
		}
		bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		closed := make(chan struct{})
		stop := context.AfterFunc(bounded, func() { defer close(closed); _ = closeOriginal() })
		_, err = io.ReadFull(ch, make([]byte, 8))
		if !stop() {
			<-closed
		}
		close(readJoined)
		if err == nil {
			t.Error("stalled read unexpectedly succeeded")
		}
		return nil, errors.New("cancelled original pre-exec read")
	}}
	client, _, serverDone := joinedSSHFixture(t, s, user)
	ch, _, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.OpenChannel("session", nil); err == nil {
		t.Fatal("original connection allowed another invocation")
	}
	_, _ = ch.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{"fixed preparation"}))
	joinSignal(t, readJoined, "original transport failed to unblock pre-exec read")
	joinSignal(t, serverDone, "original server transport did not join")
}
