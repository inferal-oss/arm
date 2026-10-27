package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

func TestForwardListenerPortAndResourcesJoinBeforeSettlement(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	joined := make(chan struct{})
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("a", 64)}
	h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("1", 32), "original source listener test", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		t.Fatal(err)
	}
	l, err := h.Lease()
	if err != nil || l.State != "active" || len(l.Ports) != 1 || l.Ports[0].Port == 0 || l.Ports[0].State != "held" {
		t.Fatalf("forward listener not held: %+v %v", l, err)
	}
	if _, err = m.AcquireForwardListener(context.Background(), strings.Repeat("1", 32), "original source listener test", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, make(chan struct{})); errorCode(err) != "idempotency_conflict" {
		t.Fatalf("duplicate original key admitted: %v", err)
	}
	if _, err = m.RecoverForwardListener(l.ID, "premature cleanup"); errorCode(err) != "needs_attention" {
		t.Fatalf("live original listener recovered: %v", err)
	}
	ending, err := m.End(l.ID, "revoked")
	if err != nil || ending.State != "ending" {
		t.Fatalf("early settlement: %+v %v", ending, err)
	}
	select {
	case <-h.Context().Done():
	default:
		t.Fatal("release did not cancel original listener")
	}
	if _, err = h.FinishAfterJoin(); errorCode(err) != "listener_not_joined" {
		t.Fatalf("closed before original join: %v", err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU < .01 || status.Reserved.Memory < 1<<20 {
		t.Fatalf("port/resource charge lost before join: %+v %v", status.Reserved, err)
	}
	close(joined)
	terminal, err := h.FinishAfterJoin()
	if err != nil || terminal.State != "terminal" || terminal.Outcome != "revoked" || terminal.Ports[0].State != "observed_available" {
		t.Fatalf("joined listener settlement: %+v %v", terminal, err)
	}
	status, err = m.Status()
	if err != nil || status.Reserved.CPU != 0 || status.Reserved.Memory != 0 {
		t.Fatalf("charge not settled after join: %+v %v", status.Reserved, err)
	}
	if settled, err := m.RecoverForwardListener(l.ID, "idempotent read"); err != nil || settled.State != "terminal" {
		t.Fatalf("settled listener changed: %+v %v", settled, err)
	}
}

func TestForwardListenerJoinedPortAllowsResetProof(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	joined := make(chan struct{})
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "web", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "source reset proof", binding, time.Minute, model.Resources{CPU: .0001, Memory: 1 << 20}, joined)
	if err != nil {
		_ = m.Close()
		t.Fatal(err)
	}
	if _, err = m.End(h.LeaseID(), "stopped"); err != nil {
		t.Fatal(err)
	}
	close(joined)
	if l, e := h.FinishAfterJoin(); e != nil || l.State != "terminal" {
		t.Fatalf("source not joined: %+v %v", l, e)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	proof, err := database.ProveDatabaseSettled(t.Context(), source, m.owner)
	if err != nil || !proof.Complete || !proof.Settled {
		t.Fatal("joined source not recognized by reset proof", proof, err)
	}
}
