package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
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
	return sshSettlementFixtureWithManager(t, newManager(t), failedGate)
}
func sshSettlementFixtureWithManager(t *testing.T, m *Manager, failedGate bool) (*sshManagerFixture, <-chan *sshRuntime, <-chan *heldSSHInputCompletion, func()) {
	t.Helper()
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

func sshRetainedSettlementManager(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	t.Cleanup(func() {
		if err := m.Close(); err == nil {
			t.Error("uncertain original SSH settlement was silently cleared at Close")
		}
	})
	return m
}
func assertSSHReconcileRefused(t *testing.T, m *Manager, id string) {
	t.Helper()
	l, err := m.Reconcile(id)
	var cause *model.Error
	if !errors.As(err, &cause) || cause.Code != "needs_attention" || !consuming(l.State) || l.Ended != nil {
		t.Fatal("Reconcile released an original SSH obligation", l, err)
	}
}
func TestSSHReconcileCannotBypassHeldOrFailedSettlement(t *testing.T) {
	m := sshRetainedSettlementManager(t)
	f, actual, inputs, release := sshSettlementFixtureWithManager(t, m, false)
	defer release()
	session, err := f.client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err = session.Start("true"); err != nil {
		t.Fatal(err)
	}
	r := awaitSSHRuntime(t, actual)
	held := awaitSSHInput(t, inputs)
	awaitTransportSignal(t, held.entered, "original input never reached EOF")
	assertSSHSettlementCharged(t, m, r)
	// A consuming attention row with its actually absent native group must not
	// bypass a live original IO hold, or the hold retained after persistence mismatch.
	m.mu.Lock()
	l, err := m.get(r.leaseHold.original.ID)
	if err == nil {
		l.State = "needs_attention"
		l.Revision++
		err = m.put(l, "test.ssh_attention")
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	assertSSHReconcileRefused(t, m, l.ID)
	release()
	awaitTransportSignal(t, r.leaseHold.done, "joined mismatched settlement did not return")
	if r.leaseHold.err == nil {
		t.Fatal("changed physical row was overwritten after join")
	}
	assertSSHReconcileRefused(t, m, l.ID)
	s, err := m.Status()
	if err != nil || s.Reserved.CPU != .5 || s.Reserved.Memory != 64 {
		t.Fatal(s.Reserved, err)
	}
}
func TestSSHStagingRejectsChangedNativeBinding(t *testing.T) {
	for _, field := range []string{"pid", "identity", "id"} {
		t.Run(field, func(t *testing.T) {
			m := sshRetainedSettlementManager(t)
			f, actual, inputs, release := sshSettlementFixtureWithManager(t, m, false)
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
			if err = session.Start("printf ready; IFS= read -r value"); err != nil {
				t.Fatal(err)
			}
			if _, err = io.ReadFull(output, make([]byte, 5)); err != nil {
				t.Fatal(err)
			}
			r := awaitSSHRuntime(t, actual)
			held := awaitSSHInput(t, inputs)
			m.mu.Lock()
			l, err := m.get(r.leaseHold.original.ID)
			rowID := r.leaseHold.original.ID
			if err == nil {
				if field == "id" {
					l.ID += "-replaced"
				} else if field == "pid" {
					l.PID++
				} else if l.Process != nil {
					identity := *l.Process
					identity.Start += "-replaced"
					l.Process = &identity
				} else {
					err = errors.New("native identity missing")
				}
				if err == nil {
					l.Revision++
					// Corrupt only the JSON row identity, preserving its original SQL key.
					if field == "id" {
						var raw []byte
						raw, err = json.Marshal(l)
						if err == nil {
							_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), rowID)
						}
					} else {
						err = m.put(l, "test.ssh_native_binding_changed")
					}
				}
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(writer, "finish\n"); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			awaitTransportSignal(t, r.leaseHold.job.nativeDone, "original child did not finish independently")
			awaitTransportSignal(t, held.entered, "original input did not reach real EOF")
			assertSSHSettlementCharged(t, m, r)
			var cause *model.Error
			if !errors.As(r.leaseHold.err, &cause) || cause.Code != "ssh_owner_changed" {
				t.Fatal("changed native binding reached settlement", r.leaseHold.err)
			}
			release()
			awaitTransportSignal(t, r.leaseHold.done, "changed binding cleanup did not return")
			stored, err := m.Inspect(rowID)
			if err != nil || stored.ID != l.ID || stored.PID != l.PID || !reflect.DeepEqual(stored.Process, l.Process) || stored.State == "terminal" || stored.Ended != nil {
				t.Fatal("original waiter overwrote changed native binding", stored, err)
			}
			assertSSHReconcileRefused(t, m, rowID)
		})
	}
}
func TestSSHRecoveredDescriptorCannotSettleWithoutOriginalJoin(t *testing.T) {
	for _, pid := range []int{0, 2147483647} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "arm")
			m, err := Open(home, model.Resources{CPU: 1, Memory: 1024})
			if err != nil {
				t.Fatal(err)
			}
			cpuReading(m, 64, 64, 0, 64, 0)
			q := request("")
			q.Resources = model.Resources{CPU: .5, Memory: 64}
			l, err := m.Acquire(q, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Persist an actual nonterminal SSH run, then lose its process-local owner.
			raw, err := json.Marshal(RunRequest{Request: q, SSH: &model.SSHRunDescriptor{}})
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			l.PID = pid
			l.State = "active"
			err = m.put(l, "test.saved_ssh")
			if err == nil {
				_, err = m.db.Exec(`UPDATE leases SET run=? WHERE id=?`, string(raw), l.ID)
			}
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			m, err = Open(home, model.Resources{CPU: 1, Memory: 1024})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			stored, err := m.Inspect(l.ID)
			if err != nil || stored.State != "needs_attention" || stored.Ended != nil || stored.Outcome != "ssh_supervision_lost" {
				t.Fatal(stored, err)
			}
			assertSSHReconcileRefused(t, m, l.ID)
			s, err := m.Status()
			if err != nil || s.Reserved.CPU != .5 || s.Reserved.Memory != 64 {
				t.Fatal(s.Reserved, err)
			}
		})
	}
}
