package manager

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// This exercises an ordinary cleanup refusal after real original socket,
// directory and worker-join proof. It does not diagnose native in-use FDs.
func TestForwardUnixStorageAttentionReturnsWithoutReleasingOriginalPair(t *testing.T) {
	m, _, child, _, _ := newShortForwardUnixPairFixture(t)
	pair, err := child.IssueStoragePair(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Inspect(pair.storage)
	if err != nil {
		t.Fatal(err)
	}
	workersJoined := make(chan struct{})
	bound, err := pair.BindOriginal(t.Context(), workersJoined)
	if err != nil {
		t.Fatal(err)
	}
	close(workersJoined)
	// Own only the asynchronous cleanup scheduling slot, so the test can
	// deterministically publish its refusal after the ending ACK. It never
	// supplies successful activity, reclamation or resource-credit evidence.
	m.mu.Lock()
	m.storageJobs[pair.storage] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.storageJobs, pair.storage)
		m.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bound.CloseAfterJoin(ctx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("original close caller did not return after cancellation")
			}
		}
	}()
	until := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		lease, readErr := m.get(pair.storage)
		ready := readErr == nil && lease.State == "ending" && pair.data.Phase == "terminal"
		if ready {
			_, err = m.storageFailed(lease, errors.New("fixture ordinary cleanup requires reconciliation"))
		}
		m.mu.Unlock()
		if readErr != nil || err != nil {
			t.Fatal(readErr, err)
		}
		if ready {
			break
		}
		if !time.Now().Before(until) {
			t.Fatal("original native terminal and storage ending ACK did not arrive")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err = <-done:
		joined = true
	case <-time.After(time.Second):
		t.Fatal("completed storage refusal left original close polling")
	}
	if errorCode(err) != "forward_unix_cleanup_uncertain" || ctx.Err() != nil {
		t.Fatal("close did not return retained cleanup uncertainty before cancellation", err, ctx.Err())
	}
	m.mu.Lock()
	retained := pair.native == bound && child.pair == pair && pair.data.Phase == "terminal" && pair.data.NativeTerminal != nil
	m.mu.Unlock()
	if !retained || !bound.dirCloseAttempted || bound.dirCloseErr != nil || bound.closeErr != nil {
		t.Fatal("native close proof or original retained pair was lost")
	}
	after, err := m.Inspect(pair.storage)
	if err != nil || after.State != "needs_attention" || after.Ended != nil || after.Request.Resources != before.Request.Resources || after.Artifact == nil || after.Artifact.NamespaceRemoved {
		t.Fatal("storage refusal released its charged original", after, err)
	}
	if _, err := os.Stat(after.ManagedDir); err != nil {
		t.Fatal("controlled refusal removed the retained namespace", err)
	}
}
