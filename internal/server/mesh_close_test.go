package server

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/mesh"
)

func meshCloseFixture(t *testing.T) (*Server, *meshService) {
	t.Helper()
	s := meshFixture(t)
	status, err := s.Manager.Status()
	if err != nil {
		t.Fatal(err)
	}
	address := meshAddress(t)
	store, err := mesh.Init(s.home, status.Host, "private-close-owner", address, address)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.startMesh(store); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeMesh)
	return s, s.mesh
}

func awaitMeshClose(t *testing.T, owner *meshClose) meshCloseResult {
	t.Helper()
	select {
	case <-owner.done:
		return owner.result
	case <-time.After(5 * time.Second):
		t.Fatal("original service close remains pending; no substitute owner or PID fallback")
		return meshCloseResult{}
	}
}

func TestMeshCloseReopenCleanCensusAfterExactOriginalReceipt(t *testing.T) {
	s, original := meshCloseFixture(t)
	owner, err := s.beginMeshClose()
	if err != nil || owner.service != original {
		t.Fatal("close did not retain the exact original service", err)
	}
	if result := awaitMeshClose(t, owner); !result.complete() {
		t.Fatal("original listener has no complete receipt", result)
	}
	if err = s.loadMesh(); err != nil {
		t.Fatal("same-identity native listener did not reopen", err)
	}
	if s.mesh == original || s.meshCloseBlocker() != nil {
		t.Fatal("idle census borrowed a different close or remained falsely blocked")
	}
}

func TestMeshCloseOriginalPendingHandlerCompletesWithoutPoisonOrUnrelatedClear(t *testing.T) {
	s, original := meshCloseFixture(t)
	if !original.enterRequest() {
		t.Fatal("original handler registration failed")
	}
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(original.leaveRequest) }
	t.Cleanup(finish)
	owner, err := s.beginMeshClose()
	if err != nil {
		t.Fatal(err)
	}
	if original.enterRequest() {
		t.Fatal("old service admitted a handler after close seal")
	}
	if blocker := s.meshCloseBlocker(); blocker == nil || blocker.Class != "unjoined" {
		t.Fatal("original pending handler was not visible", blocker)
	}
	// A different real listener can finish cleanly without claiming the old
	// admitted handler joined. Ordinary close above did not wait for that work.
	if err = s.loadMesh(); err != nil {
		t.Fatal(err)
	}
	other, err := s.beginMeshClose()
	if err != nil || other == owner {
		t.Fatal("new service borrowed the old close owner", err)
	}
	if result := awaitMeshClose(t, other); result.err != nil {
		t.Fatal(result.err)
	}
	if blocker := s.meshCloseBlocker(); blocker == nil || blocker.Class != "unjoined" {
		t.Fatal("unrelated clean close erased original pending duty", blocker)
	}
	finish()
	result := awaitMeshClose(t, owner)
	if result.err != nil || !result.serveJoined || !result.connectionsAndHandlersJoined || !result.storeClosed || !result.listenerClosed {
		t.Fatal("same original owner did not produce complete receipt", result)
	}
	if blocker := s.meshCloseBlocker(); blocker != nil {
		t.Fatal("known pending owner stayed poisoned after its actual join", blocker)
	}
}

type failingMeshListener struct{ net.Listener }

func (l failingMeshListener) Close() error {
	_ = l.Listener.Close()
	return errors.New("private-close-failure")
}

func TestMeshCloseMissingAndFailedOriginalReceiptsStayUnknownAfterCleanReopen(t *testing.T) {
	for _, fault := range []string{"missing-serve-owner", "listener-close-failure"} {
		t.Run(fault, func(t *testing.T) {
			s, original := meshCloseFixture(t)
			actualServe := original.serveDone
			s.meshMu.Lock()
			if fault == "missing-serve-owner" {
				original.serveDone = nil // original goroutine captured actualServe
			} else {
				original.listener = failingMeshListener{original.listener}
			}
			s.meshMu.Unlock()
			owner, _ := s.beginMeshClose()
			result := awaitMeshClose(t, owner)
			if result.err == nil {
				t.Fatal("missing/failed native owner became complete")
			}
			if fault == "missing-serve-owner" {
				// Test-only original channel proves the fixture itself ended; it
				// does not repair the product's missing retained ownership frame.
				select {
				case <-actualServe:
				case <-time.After(5 * time.Second):
					t.Fatal("private actual Serve did not finish")
				}
			}
			if blocker := s.meshCloseBlocker(); blocker == nil || blocker.Class != "unknown" {
				t.Fatal("unknown original close was not retained", blocker)
			}
			if err := s.loadMesh(); err != nil {
				t.Fatal(err)
			}
			other, err := s.beginMeshClose()
			if err != nil || awaitMeshClose(t, other).err != nil {
				t.Fatal("unrelated clean service control failed", err)
			}
			if blocker := s.meshCloseBlocker(); blocker == nil || blocker.Class != "unknown" {
				t.Fatal("later clean owner erased original failed/missing receipt", blocker)
			}
		})
	}
}

type heldMeshCloseConn struct {
	net.Conn
	once    sync.Once
	entered chan struct{}
	release <-chan struct{}
	err     error
}

func (c *heldMeshCloseConn) Close() error {
	c.once.Do(func() { close(c.entered); <-c.release; c.err = c.Conn.Close() })
	return c.err
}

type heldMeshAccept struct {
	net.Listener
	entered chan struct{}
	release <-chan struct{}
}

func (l heldMeshAccept) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &heldMeshCloseConn{Conn: c, entered: l.entered, release: l.release}, nil
}

func TestMeshCloseIncludesRealStalledTLSConnectionBeforeAnyHandler(t *testing.T) {
	s := meshFixture(t)
	status, _ := s.Manager.Status()
	native, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	store, err := mesh.Init(s.home, status.Host, "stalled-transport", native.Addr().String(), native.Addr().String())
	if err != nil {
		native.Close()
		t.Fatal(err)
	}
	entered, release, seen := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	allowClose := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(allowClose)
	listener := heldMeshAccept{Listener: native, entered: entered, release: release}
	m := &meshService{owner: s, store: store, listener: listener, connections: map[net.Conn]struct{}{}, serveDone: make(chan error, 1), drainDone: make(chan struct{})}
	m.http = &http.Server{Handler: http.HandlerFunc(m.serve), ConnState: func(c net.Conn, state http.ConnState) {
		m.connectionState(c, state)
		if state == http.StateNew {
			close(seen)
		}
	}}
	s.mesh = m
	serveDone := m.serveDone
	go func() { serveDone <- m.http.Serve(tls.NewListener(listener, store.ServerTLS())) }()
	t.Cleanup(func() {
		allowClose()
		owner, err := s.beginMeshClose()
		if err != nil {
			t.Error("private original close failed", err)
		}
		if owner != nil {
			select {
			case <-owner.done:
			case <-time.After(5 * time.Second):
				t.Error("original private connection owner remains unresolved")
			}
		}
	})
	raw, err := net.DialTimeout("tcp", native.Addr().String(), time.Second)
	if err != nil {
		allowClose()
		m.http.Close()
		t.Fatal(err)
	}
	defer raw.Close()
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("real pre-auth TLS connection was not registered")
	}
	m.mu.Lock()
	count, requests := len(m.connections), m.requests
	m.mu.Unlock()
	if count != 1 || requests != 0 {
		t.Fatal("stalled TLS control accidentally entered an HTTP handler", count, requests)
	}
	returned := make(chan *meshClose, 1)
	go func() { owner, _ := s.beginMeshClose(); returned <- owner }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned real connection did not reach close cut")
	}
	if blocker := s.meshCloseBlocker(); blocker == nil || blocker.Class != "unjoined" {
		t.Fatal("pre-handler connection was falsely complete", blocker)
	}
	allowClose()
	select {
	case owner := <-returned:
		if result := awaitMeshClose(t, owner); result.err != nil || !result.connectionsAndHandlersJoined {
			t.Fatal("original connection drain was not joined", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("original close owner remained unresolved after its real release")
	}
	if blocker := s.meshCloseBlocker(); blocker != nil {
		t.Fatal("same original completed connection remained pending", blocker)
	}
}

func TestMeshCloseRetainedCapacityKeepsUnknownOwners(t *testing.T) {
	s := meshFixture(t)
	s.meshClosers = make(map[*meshService]*meshClose)
	for range maxRetainedMeshClosers {
		m := &meshService{}
		s.meshClosers[m] = &meshClose{service: m, done: make(chan struct{})}
	}
	if s.canRetainMeshCloser() || s.meshCloseBlocker() == nil || len(s.meshClosers) != maxRetainedMeshClosers {
		t.Fatal("pending owners were dropped or unbounded admission allowed")
	}
	for _, owner := range s.meshClosers {
		owner.result.err = errors.New("original owner unknown")
		close(owner.done)
	}
	if s.canRetainMeshCloser() || !strings.Contains(s.meshCloseBlocker().Detail, "failed or missing") {
		t.Fatal("failed owners disappeared at capacity")
	}
	for _, owner := range s.meshClosers {
		owner.result = meshCloseResult{} // no error alone is not a complete receipt
	}
	if s.canRetainMeshCloser() || s.meshCloseBlocker() == nil {
		t.Fatal("partial receipt was treated as a positive joined owner")
	}
}
