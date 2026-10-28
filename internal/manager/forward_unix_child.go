package manager

import (
	"context"
	"reflect"
	"strings"
	"time"

	"getarm.org/internal/model"
)

// ForwardUnixChild is a process-local child of one original source listener.
// It carries no pathname or filesystem authority. Native Unix socket creation
// must wait for a separately charged managed-storage custody proof.
type ForwardUnixChild struct {
	parent     *ForwardListener
	ctx        context.Context
	cancel     context.CancelFunc
	joined     <-chan struct{}
	workerDone chan struct{}
	released   bool // protected by Manager.mu
	pairIssued bool // one original pair attempt, even after an uncertain write
	pair       *ForwardUnixPair
}

func (c *ForwardUnixChild) Context() context.Context { return c.ctx }

// IssueUnixChild cannot be reconstructed from a lease row, PID or request key.
// Exactly one child may be live under the exact original listener.
func (h *ForwardListener) IssueUnixChild(ctx context.Context, joined <-chan struct{}) (*ForwardUnixChild, error) {
	if h == nil || h.manager == nil || ctx == nil || joined == nil {
		return nil, model.Err("invalid_request", "live original listener and child join required")
	}
	select {
	case <-joined:
		return nil, model.Err("invalid_request", "original child join already closed")
	default:
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := h.remainingLocked(ctx); err != nil {
		return nil, err
	}
	if h.unixIssued {
		return nil, model.Err("operation_busy", "original listener already issued its Unix child")
	}
	life, cancel := context.WithCancel(h.ctx)
	c := &ForwardUnixChild{parent: h, ctx: life, cancel: cancel, joined: joined, workerDone: make(chan struct{})}
	h.unixChild = c
	h.unixIssued = true
	m.wg.Go(func() {
		defer close(c.workerDone)
		<-joined
		_ = c.FinishAfterJoin()
	})
	return c, nil
}

// Remaining checks the original parent and the native suspend-inclusive clock
// before each new child operation. A child cannot survive End or owner change.
func (c *ForwardUnixChild) Remaining(ctx context.Context) (time.Duration, error) {
	if c == nil || c.parent == nil || c.parent.manager == nil || ctx == nil || ctx.Err() != nil {
		return 0, model.Err("lease_inactive", "live original Unix child required")
	}
	h := c.parent
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.released || h.unixChild != c || c.ctx.Err() != nil {
		return 0, model.Err("lease_inactive", "original Unix child changed")
	}
	return h.remainingLocked(ctx)
}

// MarkCleanupUncertain is an irreversible in-process hold. It must be called
// before joined closes if a future native child cannot prove close/unlink.
// Ledger failure cannot clear the sticky hold while this process remains live.
func (c *ForwardUnixChild) MarkCleanupUncertain(reason string) error {
	if c == nil || c.parent == nil || c.parent.manager == nil {
		return model.Err("invalid_request", "original Unix child required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 4096 {
		return model.Err("invalid_request", "bounded cleanup failure reason required")
	}
	h := c.parent
	m := h.manager
	m.mu.Lock()
	if c.released || h.unixChild != c || h.settled || m.forwardListeners[h.id] != h {
		m.mu.Unlock()
		return model.Err("listener_owner_changed", "original Unix child no longer owns cleanup")
	}
	select {
	case <-c.joined:
		m.mu.Unlock()
		return model.Err("listener_owner_changed", "cleanup failure must precede original child join")
	default:
	}
	// Keep the sticky hold before releasing Manager.mu; interrupt the native
	// listener only after the lock is released.
	defer func() { m.mu.Unlock(); h.CloseOriginal() }()
	if h.unixFault == "" {
		h.unixFault = reason
	}
	c.cancel()
	l, err := m.get(h.id)
	if err != nil {
		return err
	}
	if l.ID != h.id || l.Request.Kind != forwardListenerKind || l.Request.Key != h.key || !consuming(l.State) || l.Host != h.result.Host || l.Owner != h.result.Owner || l.PID != 0 || l.Process != nil || !reflect.DeepEqual(l.Request, h.request) {
		return model.Err("listener_owner_changed", "durable listener changed; cleanup remains uncertain")
	}
	l.State, l.Ended = "needs_attention", nil
	l.Outcome, l.Failure = "forward_unix_cleanup_uncertain", h.unixFault
	l.Revision++
	if err = m.put(l, "forward.unix_cleanup_uncertain"); err != nil {
		return err
	}
	h.result = l
	return nil
}

// FinishAfterJoin releases only this child capability. It proves no native
// cleanup by itself; the future binder must call MarkCleanupUncertain on any
// failed close/unlink before closing joined.
func (c *ForwardUnixChild) FinishAfterJoin() error {
	if c == nil || c.parent == nil || c.parent.manager == nil {
		return model.Err("invalid_request", "original Unix child required")
	}
	select {
	case <-c.joined:
	default:
		return model.Err("listener_not_joined", "original Unix child workers remain owned")
	}
	h := c.parent
	m := h.manager
	m.mu.Lock()
	if c.released {
		m.mu.Unlock()
		return nil
	}
	if h.unixChild != c || m.forwardListeners[h.id] != h {
		m.mu.Unlock()
		return model.Err("listener_owner_changed", "original Unix child changed")
	}
	if h.unixFault != "" {
		m.mu.Unlock()
		return model.Err("needs_attention", "original Unix child cleanup remains uncertain")
	}
	if c.pair != nil {
		m.mu.Unlock()
		return model.Err("forward_unix_pair_unavailable", "planned pair requires original no-bind abort before child release")
	}
	l, err := m.get(h.id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if l.ID != h.id || l.Request.Kind != forwardListenerKind || l.Request.Key != h.key || !consuming(l.State) || l.Host != h.result.Host || l.Owner != h.result.Owner || l.PID != 0 || l.Process != nil || !reflect.DeepEqual(l.Request, h.request) {
		m.mu.Unlock()
		return model.Err("listener_owner_changed", "durable listener changed; child custody retained")
	}
	c.cancel()
	c.released = true
	h.unixChild = nil
	m.mu.Unlock()
	select {
	case <-h.joined:
		_, err = h.FinishAfterJoin()
		return err
	default:
		return nil
	}
}
