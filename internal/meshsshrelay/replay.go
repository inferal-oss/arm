package meshsshrelay

import (
	"errors"
	"sync"
	"time"
)

// ReplayGate is an in-memory per-hop open guard. Its owner must keep the same
// instance across accepted sockets and stop accepting on restart until a fresh
// membership/route epoch; this is not durable operation-key deduplication.
type ReplayGate struct {
	mu       sync.Mutex
	seen     map[string]time.Time
	capacity int
}

func NewReplayGate(capacity int) (*ReplayGate, error) {
	if capacity < 1 || capacity > 4096 {
		return nil, errors.New("invalid replay capacity")
	}
	return &ReplayGate{seen: make(map[string]time.Time), capacity: capacity}, nil
}

// Admit keys the route in its workspace generation. Full guards deny rather
// than evict an unexpired route ID, which would permit immediate replay.
func (g *ReplayGate) Admit(now time.Time, o Open) error {
	if g == nil {
		return errors.New("missing SSH relay replay guard")
	}
	if err := o.Validate(now); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, until := range g.seen {
		if !until.After(now) {
			delete(g.seen, key)
		}
	}
	key := o.Workspace + ":" + o.Generation + ":" + o.Route
	if _, exists := g.seen[key]; exists {
		return errors.New("replayed SSH relay route")
	}
	if len(g.seen) >= g.capacity {
		return errors.New("SSH relay replay capacity exhausted")
	}
	g.seen[key] = o.Expires
	return nil
}
