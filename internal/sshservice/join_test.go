package sshservice

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func joinSignal(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func joinPending(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal(message)
	case <-time.After(20 * time.Millisecond):
	}
}

// This real TCP/SSH fixture exposes the original Serve completion. Its cleanup
// cancels that same owner and waits; it does not adopt a PID or spawn a job.
func joinedSSHFixture(t *testing.T, s Server, user ssh.Signer) (*ssh.Client, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err == nil {
			_ = s.Serve(ctx, c, Permit{"armuser", user.PublicKey()})
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = l.Close()
		joinSignal(t, done, "original SSH Serve did not join")
	})
	client, err := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "armuser", Auth: []ssh.AuthMethod{ssh.PublicKeys(user)}, HostKeyCallback: ssh.FixedHostKey(s.HostKey.PublicKey()), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, cancel, done
}

type heldProtocolProcess struct {
	waitEntered, cancelSeen, waitDone, joinEntered chan struct{}
	releaseWait, releaseTransport                  chan struct{}
}

func newHeldProtocolProcess() *heldProtocolProcess {
	return &heldProtocolProcess{make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})}
}
func (p *heldProtocolProcess) Wait(ctx context.Context) (Exit, error) {
	close(p.waitEntered)
	defer close(p.waitDone)
	<-ctx.Done()
	close(p.cancelSeen)
	<-p.releaseWait // explicit bounded test cleanup after cancellation is observed
	return Exit{}, ctx.Err()
}
func (p *heldProtocolProcess) JoinTransport() {
	<-p.waitDone
	close(p.joinEntered)
	<-p.releaseTransport
}
func (*heldProtocolProcess) Signal(context.Context, string) error { return nil }
func (*heldProtocolProcess) Resize(context.Context, Window) error { return nil }

func TestSSHServeJoinsCancellationAtExecutionCuts(t *testing.T) {
	for _, cut := range []string{"before-execute", "during-execute", "before-wait", "during-wait"} {
		t.Run(cut, func(t *testing.T) {
			user := testKey(t)
			var calls atomic.Int32
			executing, executeCanceled, releaseExecute := make(chan struct{}), make(chan struct{}), make(chan struct{})
			p := newHeldProtocolProcess()
			var cancel context.CancelFunc
			s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(ctx context.Context) error { return ctx.Err() }, Execute: func(ctx context.Context, _ Execution, _ ssh.Channel) (Process, error) {
				calls.Add(1)
				close(executing)
				if cut == "during-execute" {
					<-ctx.Done()
					close(executeCanceled)
					<-releaseExecute
					return nil, ctx.Err()
				}
				if cut == "before-wait" {
					cancel()
				}
				return p, nil
			}}
			client, stop, done := joinedSSHFixture(t, s, user)
			cancel = stop
			var executeOnce, waitOnce, transportOnce sync.Once
			releaseExecution := func() { executeOnce.Do(func() { close(releaseExecute) }) }
			releaseWait := func() { waitOnce.Do(func() { close(p.releaseWait) }) }
			releaseTransport := func() { transportOnce.Do(func() { close(p.releaseTransport) }) }
			unblock := func() { releaseExecution(); releaseWait(); releaseTransport() }
			t.Cleanup(unblock)
			session, err := client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if cut == "before-execute" {
				cancel()
				joinSignal(t, done, "canceled idle SSH session did not join")
				if calls.Load() != 0 {
					t.Fatal("canceled idle session executed")
				}
				return
			}
			requestDone := make(chan struct{})
			go func() { defer close(requestDone); _ = session.Start("fixture command") }()
			joinSignal(t, executing, "Execute did not start")
			if cut == "during-execute" {
				cancel()
				joinSignal(t, executeCanceled, "Execute did not observe cancellation")
				joinPending(t, done, "Serve returned while its admitted Execute was still cleaning up")
				unblock()
			} else {
				joinSignal(t, p.waitEntered, "published Process Wait did not start")
				if cut == "during-wait" {
					cancel()
				}
				joinSignal(t, p.cancelSeen, "Wait did not observe cancellation")
				joinPending(t, done, "Serve returned while original Wait cleanup remained")
				releaseWait()
				joinSignal(t, p.joinEntered, "Serve did not reach mandatory transport join")
				joinPending(t, done, "Serve returned before original transport join completed")
				// The common cleanup owns each release exactly once.
				unblock()
			}
			joinSignal(t, done, "original canceled workers did not join")
			joinSignal(t, requestDone, "original SSH request did not return")
			if calls.Load() != 1 {
				t.Fatal("execution was replayed", calls.Load())
			}
		})
	}
	t.Run("deadline-clear", testSSHDeadlineClearRetainsHandshakeWorker)
}

type failedDeadlineClearConn struct {
	net.Conn
	armed             atomic.Bool
	readHeld, release chan struct{}
	once              sync.Once
	failure           error
}

func (c *failedDeadlineClearConn) SetDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		c.armed.Store(true)
		return c.failure
	}
	return c.Conn.SetDeadline(deadline)
}
func (c *failedDeadlineClearConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && c.armed.Load() {
		c.once.Do(func() { close(c.readHeld) })
		<-c.release
	}
	return n, err
}

func testSSHDeadlineClearRetainsHandshakeWorker(t *testing.T) {
	user := testKey(t)
	failure := errors.New("private deadline-clear failure")
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, outcome := make(chan struct{}), make(chan error, 1)
	s := Server{HostKey: testKey(t), Handshake: time.Second, StartTimeout: time.Second, Revalidate: func(ctx context.Context) error { return ctx.Err() }, Execute: func(context.Context, Execution, ssh.Channel) (Process, error) {
		t.Error("deadline failure executed work")
		return nil, context.Canceled
	}}
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			outcome <- err
			return
		}
		outcome <- s.Serve(ctx, &failedDeadlineClearConn{Conn: c, readHeld: held, release: release, failure: failure}, Permit{"armuser", user.PublicKey()})
	}()
	t.Cleanup(func() {
		unblock()
		cancel()
		_ = l.Close()
		joinSignal(t, done, "original deadline-failed Serve did not join")
	})
	dialDone := make(chan struct{})
	go func() {
		defer close(dialDone)
		client, _ := ssh.Dial("tcp", l.Addr().String(), &ssh.ClientConfig{User: "armuser", Auth: []ssh.AuthMethod{ssh.PublicKeys(user)}, HostKeyCallback: ssh.FixedHostKey(s.HostKey.PublicKey()), Timeout: time.Second})
		if client != nil {
			defer client.Close()
			_ = client.Wait() // server owns the close; do not race its clear cut
		}
	}()
	joinSignal(t, held, "actual post-handshake read worker did not reach the held close cut")
	joinPending(t, done, "deadline-clear failure discarded its successful handshake worker")
	unblock()
	joinSignal(t, done, "original handshake worker did not join after its own release")
	if err = <-outcome; !errors.Is(err, failure) {
		t.Fatal("original deadline-clear failure was changed", err)
	}
	joinSignal(t, dialDone, "original private SSH dial did not return")
}

func TestSSHStartTimerJoinsAlreadyRunningCloseCallback(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	owner := newSessionStartTimer(time.Millisecond, func() { close(entered); <-release })
	joinSignal(t, entered, "start timeout callback did not run")
	joined := make(chan struct{})
	go func() { owner.join(); close(joined) }()
	joinPending(t, joined, "timer Stop falsely joined an already-running close")
	unblock()
	joinSignal(t, joined, "original timer close callback did not join")
	if owner.stop() {
		t.Fatal("running callback was reported as prevented")
	}
}
