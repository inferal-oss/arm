package manager

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

func transportJoinFixture(t *testing.T) (*sshManagerFixture, <-chan *sshProcess) {
	t.Helper()
	m := newManager(t)
	actual := make(chan *sshProcess, 1)
	f := newSSHManagerFixtureWithAdmission(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, execution sshservice.Execution, channel ssh.Channel) (model.Lease, sshservice.Process, error) {
		lease, process, err := m.AcquireSSH(ctx, q, execution, channel, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute))
		if err == nil {
			actual <- process.(*sshProcess)
		}
		return lease, process, err
	})
	return f, actual
}

func awaitTransportSignal(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func TestSSHRealSessionNormalExitJoinsInputCopyWithoutClientEOF(t *testing.T) {
	f, actual := transportJoinFixture(t)
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	session.Stdin = input // deliberately no client EOF until server transport joins
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Start("printf ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(out, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	process := <-actual
	awaitTransportSignal(t, f.serverDone, "normal session deadlocked on a still-open input reader")
	for _, done := range []<-chan struct{}{process.runtime.inputDone, process.runtime.outputDone, process.runtime.closeDone} {
		awaitTransportSignal(t, done, "SSH returned before an actual copy/callback completed")
	}
	writer.Close()
	if err = session.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSSHRealCanceledSessionRetainsRunningNativeRuntimeCleanup(t *testing.T) {
	f, actual := transportJoinFixture(t)
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Start("printf ready;exec sleep 30"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(out, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	process := <-actual
	// Hold the actual product runtime cleanup mutex, not a fake Process which
	// ignores cancellation. The original AfterFunc must start and remain owned.
	process.runtime.mu.Lock()
	var released sync.Once
	release := func() { released.Do(process.runtime.mu.Unlock) }
	defer release()
	f.cancel()
	awaitTransportSignal(t, process.runtime.closeStarted, "original runtime close callback did not start")
	select {
	case <-f.serverDone:
		t.Fatal("outer handler returned while real runtime cleanup was held")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-process.runtime.closeDone:
		t.Fatal("held native runtime cleanup falsely reported complete")
	default:
	}
	release()
	awaitTransportSignal(t, f.serverDone, "original canceled session did not join its own workers")
	for _, done := range []<-chan struct{}{process.runtime.inputDone, process.runtime.outputDone, process.runtime.closeDone} {
		awaitTransportSignal(t, done, "canceled transport returned before copy/callback join")
	}
	// This transport join never waits on job.done or declares the lease free.
	// Existing Manager supervision remains responsible for interruption/expiry.
	_, _, lease := f.snapshot()
	eventually(t, func() bool {
		current, e := f.m.Inspect(lease.ID)
		return e == nil && (current.State == "terminal" || current.State == "needs_attention")
	})
}

func TestSSHRealFailedLaunchJoinsUnpublishedRuntime(t *testing.T) {
	m := newManager(t)
	m.mu.Lock()
	err := m.ensureSSHRecords()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER fail_ssh_started BEFORE INSERT ON events WHEN NEW.type='lease.started' BEGIN SELECT RAISE(ABORT,'private partial-launch fault'); END`); err != nil {
		t.Fatal(err)
	}
	returned := make(chan *failedSSHTransport, 1)
	var f *sshManagerFixture
	f = newSSHManagerFixtureWithAdmissionReadHold(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, execution sshservice.Execution, channel ssh.Channel) (model.Lease, sshservice.Process, error) {
		// The actual peer stops delivering encrypted packets before the failure
		// reply/close, so it cannot acknowledge a channel-close request.
		f.peerReads.hold.Store(true)
		lease, process, err := m.AcquireSSH(ctx, q, execution, channel, model.Resources{CPU: 1}, time.Minute, time.Now().Add(time.Minute))
		cleanup, ok := process.(*failedSSHTransport)
		if err == nil || !ok {
			t.Error("partial launch did not transfer cleanup-only duty")
			return lease, process, err
		}
		cleanup.runtime.mu.Lock()
		started := cleanup.runtime.copiesStarted
		cleanup.runtime.mu.Unlock()
		if !started {
			t.Error("fault did not cover a real started IO copy")
		}
		select {
		case <-cleanup.runtime.inputDone:
			t.Error("input copy unexpectedly ended before connection shutdown")
		default:
		}
		if _, e := cleanup.Wait(ctx); e == nil {
			t.Error("cleanup handle accepted Wait")
		}
		if cleanup.Signal(ctx, "TERM") == nil || cleanup.Resize(ctx, sshservice.Window{}) == nil {
			t.Error("cleanup handle acquired execution authority")
		}
		if _, statusErr := m.Status(); statusErr != nil {
			t.Error(statusErr)
		}
		returned <- cleanup
		return lease, process, err
	}, true)
	defer f.peerReads.releaseRead()
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	marker := filepath.Join(f.q.CWD, "must-not-execute")
	request := make(chan error, 1)
	go func() { request <- session.Start("touch " + sshTestQuote(marker)) }()
	var cleanup *failedSSHTransport
	select {
	case cleanup = <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("failed admission blocked before transferring its original IO join")
	}
	awaitTransportSignal(t, f.peerReads.held, "actual SSH peer did not withhold failure/close packet delivery")
	// No release/cancel/client-close occurs before this positive original join.
	awaitTransportSignal(t, f.serverDone, "failed runtime depended on a peer channel-close acknowledgement")
	for _, done := range []<-chan struct{}{cleanup.runtime.inputDone, cleanup.runtime.outputDone, cleanup.runtime.closeDone} {
		awaitTransportSignal(t, done, "partial-launch transport copy/callback was not joined")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("failed process gate executed the requested command", err)
	}
	f.peerReads.releaseRead()
	select {
	case err = <-request:
		if err == nil {
			t.Fatal("failed cleanup-only handle was accepted as execution")
		}
	case <-time.After(time.Second):
		t.Fatal("original failed request did not return")
	}
}
