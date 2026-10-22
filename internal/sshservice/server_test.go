package sshservice

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type fixtureProcess struct {
	done    chan struct{}
	signals chan string
	windows chan Window
}

func (p *fixtureProcess) Wait(ctx context.Context) (Exit, error) {
	select {
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	case <-p.done:
		return Exit{Code: 23}, nil
	}
}
func (p *fixtureProcess) Signal(ctx context.Context, s string) error {
	select {
	case p.signals <- s:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *fixtureProcess) Resize(ctx context.Context, w Window) error {
	select {
	case p.windows <- w:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func sshFixture(t *testing.T, s Server, permit Permit, clientKey ssh.Signer) (*ssh.Client, context.CancelFunc) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		done <- s.Serve(ctx, c, permit)
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{User: permit.Account, Auth: []ssh.AuthMethod{ssh.PublicKeys(clientKey)}, HostKeyCallback: ssh.FixedHostKey(s.HostKey.PublicKey()), Timeout: time.Second})
	if err != nil {
		listener.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		cancel()
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("SSH protocol worker did not stop")
		}
	})
	return client, cancel
}
func TestSSHOneAuthenticatedSessionAndClosedRequests(t *testing.T) {
	user := testKey(t)
	var calls atomic.Int32
	captured := make(chan Execution, 1)
	p := &fixtureProcess{make(chan struct{}), make(chan string, 2), make(chan Window, 2)}
	s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(context.Context) error { return nil }, Execute: func(ctx context.Context, q Execution, ch ssh.Channel) (Process, error) {
		calls.Add(1)
		captured <- q
		_, _ = ch.Write([]byte("fixture reply"))
		return p, nil
	}}
	client, _ := sshFixture(t, s, Permit{"armuser", user.PublicKey()}, user)
	if ok, _, err := client.SendRequest("tcpip-forward", true, ssh.Marshal(struct {
		Address string
		Port    uint32
	}{"127.0.0.1", 1234})); err != nil || ok {
		t.Fatal(err)
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = client.NewSession(); err == nil {
		t.Fatal("second session channel accepted")
	}
	if _, _, err = client.OpenChannel("direct-tcpip", nil); err == nil {
		t.Fatal("forwarding channel accepted")
	}
	if session.Setenv("SSH_AUTH_SOCK", "/other/socket") == nil {
		t.Fatal("environment accepted")
	}
	if session.RequestSubsystem("sftp") == nil {
		t.Fatal("SFTP inherited execution permission")
	}
	if calls.Load() != 0 {
		t.Fatal("nonexecution request launched work")
	}
	if err = session.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1}); err != nil {
		t.Fatal(err)
	}
	if err = session.WindowChange(30, 100); err != nil {
		t.Fatal(err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Start("private fixture command"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("fixture reply"))
	if _, err = io.ReadFull(stdout, got); err != nil {
		t.Fatal(err)
	}
	q := <-captured
	if q.Kind != "exec" || q.Command != "private fixture command" || q.Terminal == nil || q.Terminal.Window.Columns != 100 || q.Terminal.Window.Rows != 30 {
		t.Fatal("request snapshot changed", q)
	}
	ok, err := session.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{"second"}))
	if err != nil || ok {
		t.Fatal("second exec accepted", ok, err)
	}
	if err = session.Signal(ssh.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-p.signals:
		if got != "TERM" {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("signal not routed")
	}
	if err = session.WindowChange(40, 120); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-p.windows:
		if w.Columns != 120 || w.Rows != 40 {
			t.Fatal(w)
		}
	case <-time.After(time.Second):
		t.Fatal("resize not routed")
	}
	close(p.done)
	err = session.Wait()
	exit, ok := err.(*ssh.ExitError)
	if !ok || exit.ExitStatus() != 23 {
		t.Fatal("exit status lost", err)
	}
	if calls.Load() != 1 {
		t.Fatal("execution replayed")
	}
}
func TestSSHWaitingSessionExpiresWithoutExecution(t *testing.T) {
	user := testKey(t)
	var calls atomic.Int32
	s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: 30 * time.Millisecond, Revalidate: func(context.Context) error { return nil }, Execute: func(context.Context, Execution, ssh.Channel) (Process, error) { calls.Add(1); return nil, nil }}
	client, _ := sshFixture(t, s, Permit{"armuser", user.PublicKey()}, user)
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	time.Sleep(70 * time.Millisecond)
	if err = session.Start("too late"); err == nil {
		t.Fatal("expired execution accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("timed-out session called execution adapter")
	}
}
func TestSSHFinalAuthorizationAndCancellationBeforeExecution(t *testing.T) {
	user := testKey(t)
	var denied atomic.Bool
	var calls atomic.Int32
	s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(context.Context) error {
		if denied.Load() {
			return context.Canceled
		}
		return nil
	}, Execute: func(context.Context, Execution, ssh.Channel) (Process, error) { calls.Add(1); return nil, nil }}
	client, _ := sshFixture(t, s, Permit{"armuser", user.PublicKey()}, user)
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	denied.Store(true)
	if err = session.Start("not authorized"); err == nil {
		t.Fatal("changed grant executed")
	}
	if calls.Load() != 0 {
		t.Fatal("authorization failure reached adapter")
	}
}
func TestTerminalPayloadClosedAndBounded(t *testing.T) {
	good := ssh.Marshal(struct {
		Name                         string
		Columns, Rows, Width, Height uint32
		Modes                        string
	}{"xterm", 80, 24, 0, 0, string([]byte{0})})
	if _, err := parseTerminal(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{good[:len(good)-1], append(append([]byte{}, good...), 0), make([]byte, 2049), ssh.Marshal(struct {
		Name                         string
		Columns, Rows, Width, Height uint32
		Modes                        string
	}{"xterm;exec", 80, 24, 0, 0, string([]byte{0})}), ssh.Marshal(struct {
		Name                         string
		Columns, Rows, Width, Height uint32
		Modes                        string
	}{"xterm", 1001, 24, 0, 0, string([]byte{0})}), ssh.Marshal(struct {
		Name                         string
		Columns, Rows, Width, Height uint32
		Modes                        string
	}{"xterm", 80, 24, 0, 0, string([]byte{255, 0})})} {
		if _, err := parseTerminal(bad); err == nil {
			t.Fatal("malformed terminal accepted")
		}
	}
}
