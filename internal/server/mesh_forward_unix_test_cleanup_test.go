package server

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// The fixture closes its Manager in Cleanup, so caller joins must be closed
// even when an earlier assertion stops the test.
func forwardUnixTestCallerJoins(t *testing.T, joined, childJoined chan struct{}) (func(), func()) {
	t.Helper()
	var sourceOnce, childOnce sync.Once
	finishSource := func() { sourceOnce.Do(func() { close(joined) }) }
	finishChild := func() { childOnce.Do(func() { close(childJoined) }) }
	t.Cleanup(func() {
		finishChild()
		finishSource()
	})
	return finishSource, finishChild
}

// Test-only registry removal follows the actual worker join. It cannot turn
// uncertain native cleanup into production credit.
func forwardUnixTestWorkerCleanup(t *testing.T, w *forwardUnixWorker) func(net.Conn) {
	t.Helper()
	var mu sync.Mutex
	var connections []net.Conn
	t.Cleanup(func() {
		mu.Lock()
		owned := append([]net.Conn(nil), connections...)
		mu.Unlock()
		for _, conn := range owned {
			_ = conn.Close()
		}
		w.cancel()
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
			t.Error("original forward Unix worker did not join during test cleanup")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = w.Close(ctx)
		if ctx.Err() != nil {
			t.Error("original forward Unix close did not finish during test cleanup")
			return
		}
		w.mu.Lock()
		forwardUnixProcess.Lock()
		for pair := range w.active {
			delete(forwardUnixProcess.pairs, pair)
		}
		delete(forwardUnixProcess.workers, w)
		forwardUnixProcess.Unlock()
		w.mu.Unlock()
	})
	return func(conn net.Conn) {
		if conn == nil {
			return
		}
		mu.Lock()
		connections = append(connections, conn)
		mu.Unlock()
	}
}
