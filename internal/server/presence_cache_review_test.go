package server

import (
	"context"
	"github.com/yrashk/arm/internal/model"
	"testing"
	"time"
)

func TestPeerCachedRequestDoesNotTouchUnavailableManager(t *testing.T) {
	s, self, target := peerReviewFixture(t)
	// A cached request must not invoke Manager/SQL, even if that dependency is
	// unavailable. The cache timestamp is the actual projection timestamp.
	original := s.Manager
	s.Manager = nil
	defer func() { s.Manager = original }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := queryPeer(ctx, self, target); err != nil {
		t.Fatal("cached protocol unexpectedly touched unavailable Manager", err)
	}
	s.peerCacheMu.Lock()
	s.peerCache.At = time.Now().Add(-4 * time.Second)
	s.peerCacheMu.Unlock()
	if _, err := queryPeer(ctx, self, target); err == nil {
		t.Fatal("stale cache served as current")
	}
}
func TestPeerCacheWorkerOriginalJoinAndFailure(t *testing.T) {
	s, _, _ := peerReviewFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.peerCacheLoop(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("original cache worker did not join")
	}
	s.peerCacheMu.RLock()
	err := s.peerCacheErr
	s.peerCacheMu.RUnlock()
	if err == nil {
		t.Fatal("cancelled worker projection presented as fresh")
	}
}
func TestPeerCompleteSnapshotTotalsMatchItems(t *testing.T) {
	s := model.PeerSnapshot{HeldCount: 1, Held: model.Resources{Memory: 2}, Items: []model.PeerItem{{State: "active", Resources: model.Resources{Memory: 1}}}}
	if validPeerSnapshot(s) {
		t.Fatal("inconsistent complete item totals accepted")
	}
}
