package manager

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sshservice"
	"golang.org/x/crypto/ssh"
)

// Hold the completion of an ACTUAL original SSH input read after its real
// connection delivered EOF. No synthetic completion channel can settle it.
type heldSSHInputCompletion struct {
	ssh.Channel
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (h *heldSSHInputCompletion) Read(p []byte) (int, error) {
	n, err := h.Channel.Read(p)
	if err != nil {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return n, err
}

func sshSettlementFixture(t *testing.T, failedGate bool) (*sshManagerFixture, <-chan *sshRuntime, <-chan *heldSSHInputCompletion, func()) {
	t.Helper()
	m := newManager(t)
	if failedGate {
		m.mu.Lock()
		err := m.ensureSSHRecords()
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.db.Exec(`CREATE TRIGGER fail_ssh_settlement_gate BEFORE INSERT ON events WHEN NEW.type='lease.started' BEGIN SELECT RAISE(ABORT,'owned pre-gate journal refusal'); END`); err != nil {
			t.Fatal(err)
		}
	}
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	actual := make(chan *sshRuntime, 1)
	inputs := make(chan *heldSSHInputCompletion, 1)
	f := newSSHManagerFixtureWithAdmission(t, m, time.Minute, func(ctx context.Context, q model.SSHExecutionRequest, e sshservice.Execution, ch ssh.Channel) (model.Lease, sshservice.Process, error) {
		held := &heldSSHInputCompletion{Channel: ch, entered: make(chan struct{}), release: released}
		q.Resources.Memory = 64
		l, p, err := m.AcquireSSH(ctx, q, e, held, model.Resources{CPU: 1, Memory: 1024}, time.Minute, time.Now().Add(time.Minute))
		switch process := p.(type) {
		case *sshProcess:
			actual <- process.runtime
		case *failedSSHTransport:
			actual <- process.runtime
		default:
			t.Errorf("admission did not transfer original transport: %T %v", p, err)
		}
		inputs <- held
		return l, p, err
	})
	return f, actual, inputs, release
}
func awaitSSHRuntime(t *testing.T, c <-chan *sshRuntime) *sshRuntime {
	t.Helper()
	select {
	case r := <-c:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("original admission did not transfer its runtime")
		return nil
	}
}
func awaitSSHInput(t *testing.T, c <-chan *heldSSHInputCompletion) *heldSSHInputCompletion {
	t.Helper()
	select {
	case h := <-c:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("original admission did not publish its input witness")
		return nil
	}
}
func assertSSHSettlementCharged(t *testing.T, m *Manager, r *sshRuntime) {
	t.Helper()
	if r == nil || r.leaseHold == nil || r.leaseHold.job == nil {
		t.Fatal("native fixture did not bind its original runtime/job")
	}
	awaitTransportSignal(t, r.leaseHold.ready, "native result was not staged independently of transport")
	select {
	case <-r.transportJoined:
		t.Fatal("held original input was falsely joined")
	default:
	}
	select {
	case <-r.leaseHold.job.done:
		t.Fatal("native readiness incorrectly became settlement")
	default:
	}
	s, err := m.Status()
	if err != nil || s.Reserved.CPU != .5 || s.Reserved.Memory != 64 {
		t.Fatal("SSH lost charge before original transport join", s.Reserved, err)
	}
	l, err := m.Inspect(r.leaseHold.original.ID)
	if err != nil || !consuming(l.State) || l.Ended != nil {
		t.Fatal(l, err)
	}
}
func assertSSHSettlementFinished(t *testing.T, f *sshManagerFixture, r *sshRuntime) {
	t.Helper()
	awaitTransportSignal(t, f.serverDone, "Serve waited for settlement before joining its original connection")
	awaitTransportSignal(t, r.leaseHold.done, "positive native plus original IO join did not settle")
	if r.leaseHold.err != nil {
		t.Fatal(r.leaseHold.err)
	}
	l, err := f.m.Inspect(r.leaseHold.original.ID)
	if err != nil || l.State != "terminal" || l.Ended == nil {
		t.Fatal(l, err)
	}
	s, err := f.m.Status()
	if err != nil || s.Reserved.CPU != 0 || s.Reserved.Memory != 0 {
		t.Fatal(s.Reserved, err)
	}
}
func TestSSHNativeExitKeepsChargeUntilOriginalInputJoin(t *testing.T) {
	f, actual, inputs, release := sshSettlementFixture(t, false)
	defer release()
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	session.Stdin = input
	output, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Start("printf ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(output, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	r := awaitSSHRuntime(t, actual)
	held := awaitSSHInput(t, inputs)
	awaitTransportSignal(t, held.entered, "original connection did not reach its actual input EOF")
	awaitTransportSignal(t, r.leaseHold.job.nativeDone, "actual native readiness was coupled to transport join")
	assertSSHSettlementCharged(t, f.m, r)
	release()
	assertSSHSettlementFinished(t, f, r)
	writer.Close()
	if err = session.Wait(); err != nil {
		t.Fatal(err)
	}
}
func TestSSHFailedNativeGateKeepsChargeUntilOriginalInputJoin(t *testing.T) {
	f, actual, inputs, release := sshSettlementFixture(t, true)
	defer release()
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	marker := filepath.Join(f.q.CWD, "never-executed")
	request := make(chan error, 1)
	go func() { request <- session.Start("touch " + sshTestQuote(marker)) }()
	r := awaitSSHRuntime(t, actual)
	held := awaitSSHInput(t, inputs)
	awaitTransportSignal(t, held.entered, "failed gate did not retain its real input copy")
	awaitTransportSignal(t, r.leaseHold.job.nativeDone, "failed native gate did not resolve native readiness")
	assertSSHSettlementCharged(t, f.m, r)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("failed private gate executed requested work", err)
	}
	release()
	assertSSHSettlementFinished(t, f, r)
	select {
	case err = <-request:
		if err == nil {
			t.Fatal("failed native gate accepted execution")
		}
	case <-time.After(time.Second):
		t.Fatal("original failed request did not return")
	}
}
