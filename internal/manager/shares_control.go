package manager

import (
	"context"
	"sync"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// Fixed-size native preflight/reconciliation is bounded control work, not an
// uncharged manifest traversal. Close cancels and joins it before releasing the
// authority/provider. All complete tree reads belong to an admitted worker.
func (m *Manager) beginShareControl(parent context.Context) (context.Context, func(), error) {
	if m.closing || m.draining {
		return nil, nil, model.Err("backend_stopping", "share control is unavailable while backend stops/drains")
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	if len(m.shares.controls) >= 8 {
		return nil, nil, model.Err("too_many_requests", "all eight bounded native share control slots are occupied")
	}
	id := local.ID()
	ctx, cancel := context.WithCancel(parent)
	m.shares.controls[id] = cancel
	m.wg.Add(1)
	var once sync.Once
	finish := func() {
		once.Do(func() { cancel(); m.mu.Lock(); delete(m.shares.controls, id); m.mu.Unlock(); m.wg.Done() })
	}
	return ctx, finish, nil
}
func (m *Manager) cancelShareScopes() {
	m.mu.Lock()
	cancels := []context.CancelFunc{}
	if m.shares != nil {
		for _, cancel := range m.shares.controls {
			cancels = append(cancels, cancel)
		}
		for _, w := range m.shares.workers {
			cancels = append(cancels, w.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}
