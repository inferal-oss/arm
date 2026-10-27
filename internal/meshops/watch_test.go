package meshops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchReportsOnlyBoundedInvalidationsAndCloses(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	if e := local.Prepare(home); e != nil {
		t.Fatal(e)
	}
	s := &Service{Home: home, Context: context.Background()}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changes, e := s.Watch(ctx, "watched")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mesh.ClaimCoordination(home, "other", "connect", strings.Repeat("a", 64), "ssh:other"); e != nil {
		t.Fatal(e)
	}
	g, e := mesh.ClaimCoordination(home, "watched", "connect", strings.Repeat("a", 64), "ssh:watched")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case change := <-changes:
		if change.Key != "watched" || change.Revision < 1 {
			t.Fatal(change)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no durable change invalidation")
	}
	if e = g.Change(func(op *mesh.Coordination) error { op.Phase = "awaiting_review"; return nil }); e != nil {
		t.Fatal(e)
	}
	select {
	case change := <-changes:
		if change.Key != "watched" {
			t.Fatal("filter leaked unrelated key")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no next revision")
	}
	s.Close()
	for range changes {
	} // closes even when subscription context is still live
}

func TestWatchOversizedReplayRefusesAndCorruptHistoryCloses(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	if e := local.Prepare(home); e != nil {
		t.Fatal(e)
	}
	s := &Service{Home: home, Context: context.Background(), lastChanges: map[string]Change{}}
	for i := 0; i < 129; i++ {
		key := fmt.Sprint(i)
		s.lastChanges[key] = Change{Key: key, Revision: 1}
	}
	if _, e := s.Watch(context.Background(), ""); e == nil {
		t.Fatal("oversized replay accepted")
	}
	s.Close()
	s = &Service{Home: home, Context: context.Background()}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, e := s.Watch(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = mesh.ClaimCoordination(home, "corrupt", "connect", strings.Repeat("e", 64), "ssh:corrupt"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no first observation")
	}
	keyHash := sha256.Sum256([]byte("corrupt"))
	path := filepath.Join(home, "mesh-lifecycle", "coordinator-operation-"+hex.EncodeToString(keyHash[:])+".json")
	if e = os.WriteFile(path, []byte(`{"broken":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("corrupt history looked healthy")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("corrupt history did not close watch")
	}
}
