package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
)

func TestForwardUnixRemoteRawCloseDebtRetainsManagerCharges(t *testing.T) {
	s := shortForwardMeshFixture(t)
	s.Manager.Start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := s.Manager.Status()
		if err != nil {
			t.Fatal(err)
		}
		if status.CPU != nil && status.CPU.Known && status.CPU.Available >= .01 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 1<<20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native source capacity unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	joined, childJoined := make(chan struct{}), make(chan struct{})
	finishSource, finishChild := forwardUnixTestCallerJoins(t, joined, childJoined)
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := s.Manager.AcquireForwardListener(context.Background(), local.ID(), "private Unix native debt", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := child.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := &forwardSource{handle: h, binding: binding}
	w, err := r.startPrivateUnixWorker(context.Background(), pair)
	if err != nil {
		t.Fatal(err)
	}
	track := forwardUnixTestWorkerCleanup(t, w)
	fault := errors.New("injected first original remote raw close failure")
	opened := make(chan struct{}, 1)
	w.open = func(context.Context) (*meshforward.AdmittedRoute, error) {
		opened <- struct{}{}
		return nil, &meshsshrelay.NativeCloseUncertain{Cause: fault}
	}
	client, err := net.DialTimeout("unix", findForwardUnixSocket(t, s, h.LeaseID()), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	track(client)
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("original opener not reached")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); !errors.Is(err, fault) {
		t.Fatalf("remote native close uncertainty lost: %v", err)
	}
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	var sourceState, storageState string
	for _, lease := range status.Leases {
		if lease.ID == h.LeaseID() {
			sourceState = lease.State
		}
		if lease.Request.Kind == "storage" && lease.Request.Reason == "Forward Unix source directory for "+h.LeaseID() {
			storageState = lease.State
		}
	}
	if sourceState != "needs_attention" || storageState != "active" {
		t.Fatalf("remote original debt released source/storage: %q/%q", sourceState, storageState)
	}
	w.mu.Lock()
	var currentPair *forwardUnixWorkerPair
	for p := range w.active {
		currentPair = p
	}
	w.mu.Unlock()
	forwardUnixProcess.Lock()
	_, retainedWorker := forwardUnixProcess.workers[w]
	_, retainedPair := forwardUnixProcess.pairs[currentPair]
	forwardUnixProcess.Unlock()
	if !retainedWorker || !retainedPair {
		t.Fatal("remote raw close uncertainty released original process slot")
	}
	finishChild()
	finishSource()
}

func TestForwardUnixWorkerPendingCapAndCancellation(t *testing.T) {
	s := shortForwardMeshFixture(t)
	s.Manager.Start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := s.Manager.Status()
		if err != nil {
			t.Fatal(err)
		}
		if status.CPU != nil && status.CPU.Known && status.CPU.Available >= .01 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 1<<20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native source capacity unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
	joined, childJoined := make(chan struct{}), make(chan struct{})
	finishSource, finishChild := forwardUnixTestCallerJoins(t, joined, childJoined)
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	h, err := s.Manager.AcquireForwardListener(context.Background(), local.ID(), "private Unix pending worker", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
	if err != nil {
		t.Fatal(err)
	}
	child, err := h.IssueUnixChild(context.Background(), childJoined)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := child.IssueStoragePair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := &forwardSource{handle: h, binding: binding}
	w, err := r.startPrivateUnixWorker(context.Background(), pair)
	if err != nil {
		t.Fatal(err)
	}
	track := forwardUnixTestWorkerCleanup(t, w)
	opened := make(chan struct{}, 2)
	w.open = func(ctx context.Context) (*meshforward.AdmittedRoute, error) {
		opened <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	path := findForwardUnixSocket(t, s, h.LeaseID())
	clients := make([]net.Conn, 0, 3)
	for i := 0; i < 2; i++ {
		c, err := net.DialTimeout("unix", path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		track(c)
		clients = append(clients, c)
		select {
		case <-opened:
		case <-time.After(5 * time.Second):
			t.Fatal("pending opener was not registered")
		}
	}
	for _, c := range clients {
		if _, err := c.Write([]byte("unread application bytes")); err != nil {
			t.Fatal(err)
		}
	}
	third, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		track(third)
		clients = append(clients, third)
	}
	select {
	case <-opened:
		t.Fatal("third opener crossed the per-source pending cap")
	case <-time.After(150 * time.Millisecond):
	}
	w.mu.Lock()
	active := len(w.active)
	w.mu.Unlock()
	forwardUnixProcess.Lock()
	global := len(forwardUnixProcess.pairs) + len(forwardUnixProcess.pending)
	forwardUnixProcess.Unlock()
	if active != 2 || global < 2 || global > forwardUnixMaxPairs {
		t.Fatalf("pending pair capacity escaped bound: active=%d process=%d", active, global)
	}
	for _, c := range clients {
		_ = c.Close()
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.Close(closeCtx); err != nil {
		t.Fatalf("cancelled pending originals did not settle: %v", err)
	}
	finishChild()
	if err := child.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	finishSource()
	if _, err := h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
}

func findForwardUnixSocket(t *testing.T, s *Server, sourceID string) string {
	t.Helper()
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range status.Leases {
		if lease.Request.Kind != "storage" || lease.Request.Reason != "Forward Unix source directory for "+sourceID {
			continue
		}
		entries, err := os.ReadDir(lease.ManagedDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".sock") {
				return filepath.Join(lease.ManagedDir, entry.Name())
			}
		}
	}
	t.Fatal("charged original Unix socket not found")
	return ""
}
