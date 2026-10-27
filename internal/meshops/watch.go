package meshops

import (
	"context"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"time"
)

type Change struct {
	Key      string `json:"key"`
	Revision int64  `json:"revision"`
}
type watcher struct {
	key     string
	changes chan Change
}

// Watch emits bounded invalidations only. Clients fetch exact durable snapshots;
// stream delivery is not evidence of commit and never triggers mutation replay.
func (s *Service) Watch(ctx context.Context, key string) (<-chan Change, error) {
	if key != "" && !mesh.ValidKey(key) {
		return nil, model.Err("invalid_request", "invalid workflow key filter")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, model.Err("backend_stopping", "operator coordinator is stopping")
	}
	if len(s.watchers) >= 16 {
		return nil, model.Err("busy", "at most sixteen operator watches may run")
	}
	if len(s.lastChanges) > 128 {
		return nil, model.Err("history_full", "workflow watch roster exceeds its bound")
	}
	if s.watchers == nil {
		s.watchers = map[uint64]watcher{}
	}
	if s.closed == nil {
		s.closed = make(chan struct{})
	}
	done := s.closed
	s.watchSequence++
	id := s.watchSequence
	ch := make(chan Change, 128)
	s.watchers[id] = watcher{key: key, changes: ch}
	for _, change := range s.lastChanges {
		if key == "" || change.Key == key {
			select {
			case ch <- change:
			default:
				delete(s.watchers, id)
				close(ch)
				return nil, model.Err("history_full", "workflow replay exceeds bound")
			}
		}
	}
	if s.watchCancel == nil {
		watchCtx, cancel := context.WithCancel(s.Context)
		s.watchCancel = cancel
		s.wg.Add(1)
		go s.watchLoop(watchCtx)
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if w, ok := s.watchers[id]; ok {
			delete(s.watchers, id)
			close(w.changes)
		}
	}()
	return ch, nil
}
func (s *Service) watchLoop(ctx context.Context) {
	defer s.wg.Done()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for id, w := range s.watchers {
			close(w.changes)
			delete(s.watchers, id)
		}
		s.watchCancel = nil
	}()
	for {
		ops, e := s.List()
		s.mu.Lock()
		if e != nil || len(ops) > 128 {
			s.lastChanges = nil
			s.mu.Unlock()
			return
		}
		next := make(map[string]Change, len(ops))
		for _, op := range ops {
			change := Change{Key: op.Key, Revision: op.Revision}
			next[op.Key] = change
			if old := s.lastChanges[op.Key]; old.Revision != op.Revision {
				for id, w := range s.watchers {
					if w.key != "" && w.key != op.Key {
						continue
					}
					select {
					case w.changes <- change:
					default:
						close(w.changes)
						delete(s.watchers, id)
					}
				}
			}
		}
		s.lastChanges = next
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
