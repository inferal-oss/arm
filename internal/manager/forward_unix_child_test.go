package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

func newForwardUnixChildFixture(t *testing.T) (*Manager, *ForwardListener, chan struct{}) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "arm")
	if err = os.MkdirAll(home, 0700); err != nil {
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
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "original Unix child fixture", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		_ = m.Close()
		t.Fatal(err)
	}
	return m, h, joined
}

func TestForwardUnixChildExactOriginalAndJoin(t *testing.T) {
	m, h, joined := newForwardUnixChildFixture(t)
	childJoined := make(chan struct{})
	c, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.IssueUnixChild(context.Background(), make(chan struct{})); errorCode(err) != "operation_busy" {
		t.Fatalf("second child admitted: %v", err)
	}
	if _, err = c.Remaining(context.Background()); err != nil {
		t.Fatalf("live original child denied: %v", err)
	}
	if err = c.FinishAfterJoin(); errorCode(err) != "listener_not_joined" {
		t.Fatalf("child settled before join: %v", err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); errorCode(err) != "listener_not_joined" {
		t.Fatalf("parent settled with child outstanding: %v", err)
	}
	if _, err = m.End(h.LeaseID(), "stopped"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Remaining(context.Background()); errorCode(err) != "lease_inactive" {
		t.Fatalf("ended parent authorized child: %v", err)
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); err != nil {
		t.Fatalf("joined original child failed: %v", err)
	}
	l, err := m.Inspect(h.LeaseID())
	if err != nil || l.State != "terminal" {
		t.Fatalf("parent charge not settled after both joins: %+v %v", l, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardUnixChildNeverReissuedAfterJoin(t *testing.T) {
	m, h, joined := newForwardUnixChildFixture(t)
	childJoined := make(chan struct{})
	c, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if _, err = h.IssueUnixChild(context.Background(), make(chan struct{})); errorCode(err) != "operation_busy" {
		t.Fatalf("second child issued after original join: %v", err)
	}
	close(joined)
	if l, err := h.FinishAfterJoin(); err != nil || l.State != "terminal" {
		t.Fatalf("original source did not settle: %+v %v", l, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardUnixChildManagerCloseWaitsForOriginalJoin(t *testing.T) {
	m, h, joined := newForwardUnixChildFixture(t)
	childJoined := make(chan struct{})
	if _, err := h.IssueUnixChild(context.Background(), childJoined); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case <-h.Context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Close did not cancel original source")
	}
	close(joined)
	<-h.workerDone
	select {
	case err := <-closed:
		t.Fatalf("Manager.Close returned before original child join: %v", err)
	default:
	}
	close(childJoined)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Manager.Close did not finish after both original joins")
	}
}

func TestForwardUnixChildCleanupFaultRetainsChargeAndRecoveryFence(t *testing.T) {
	m, h, joined := newForwardUnixChildFixture(t)
	childJoined := make(chan struct{})
	c, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.MarkCleanupUncertain("Port reclamation unresolved: native unlink unresolved"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Context().Done():
	default:
		t.Fatal("fault did not cancel original child")
	}
	close(childJoined)
	if err = c.FinishAfterJoin(); errorCode(err) != "needs_attention" {
		t.Fatalf("uncertain child released: %v", err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); errorCode(err) != "needs_attention" {
		t.Fatalf("uncertain parent settled: %v", err)
	}
	l, err := m.Inspect(h.LeaseID())
	if err != nil || l.State != "needs_attention" || l.Outcome != "forward_unix_cleanup_uncertain" {
		t.Fatalf("fault not retained: %+v %v", l, err)
	}
	if _, err = m.RecoverForwardListener(h.LeaseID(), "TCP socket is gone"); errorCode(err) != "needs_attention" {
		t.Fatalf("TCP-only recovery erased native fault: %v", err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU < .01 || status.Reserved.Memory < 1<<20 {
		t.Fatalf("uncertain child lost resource charge: %+v %v", status.Reserved, err)
	}
	home := m.home
	_ = m.Close() // Attention is expected; this fixture has no native child socket.
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = source.DB.QueryRow(`SELECT data FROM leases WHERE id=?`, h.LeaseID()).Scan(&raw); err != nil {
		source.Close()
		t.Fatal(err)
	}
	var stored model.Lease
	if err = json.Unmarshal(raw, &stored); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	if stored.Outcome != "forward_unix_cleanup_uncertain" {
		t.Fatalf("close erased native cleanup marker: %+v", stored)
	}
	reopened, err := Open(home, model.Resources{CPU: 1, Memory: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	l, err = reopened.Inspect(h.LeaseID())
	if err != nil || l.State != "needs_attention" || l.Outcome != "forward_unix_cleanup_uncertain" {
		t.Fatalf("restart erased native cleanup marker: %+v %v", l, err)
	}
	if _, err = reopened.RecoverForwardListener(h.LeaseID(), "TCP socket is gone after restart"); errorCode(err) != "needs_attention" {
		t.Fatalf("restart allowed TCP-only native recovery: %v", err)
	}
}
