package workspace

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ProofTransport MUST authenticate the exact candidate via normal pinned TLS and
// honor ctx/force-close its owned socket. Slots stay charged until it returns;
// a noncompliant injected adapter cannot cause unbounded goroutine creation.
type ProofTransport interface {
	Prove(context.Context, Challenge) (Proof, error)
}
type Service struct {
	store  *Store
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
	mu     sync.Mutex
	closed bool
}

func NewService(parent context.Context, store *Store) *Service {
	ctx, cancel := context.WithCancel(parent)
	return &Service{store: store, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 2)}
}
func (s *Service) Close() { s.mu.Lock(); s.closed = true; s.cancel(); s.mu.Unlock() }
func (s *Service) Activate(ctx context.Context, p Principal, key string, transport ProofTransport) (Operation, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Operation{}, problem("backend_stopping", "workspace service closing")
	}
	s.mu.Unlock()
	saved, found, e := s.store.MemberLookup(ctx, p, key)
	if e != nil {
		return Operation{}, e
	}
	if found && saved.Phase == "completed" {
		return saved, nil
	}
	if transport == nil {
		return Operation{}, problem("invalid_request", "pinned candidate proof transport required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Operation{}, problem("backend_stopping", "workspace service closing")
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.mu.Unlock()
		return Operation{}, problem("too_many_requests", "bounded workspace proof slots are occupied")
	}
	s.mu.Unlock()
	// Admit a durable proving state before I/O, and never hold its transaction
	// or store mutex while a transport is blocked or the operator is reviewing.
	c, op, e := s.store.BeginProof(ctx, p, key)
	if e != nil || op.Phase == "completed" {
		<-s.slots
		return op, e
	}
	proofCtx, cancel := context.WithTimeout(s.ctx, min(5*time.Second, c.Expires.Sub(s.store.now())))
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	type answer struct {
		proof Proof
		err   error
	}
	result := make(chan answer, 1)
	go func() {
		defer func() { <-s.slots }()
		proof, e := transport.Prove(proofCtx, c)
		result <- answer{proof, e}
	}()
	var a answer
	select {
	case a = <-result:
	case <-proofCtx.Done():
		a.err = proofCtx.Err()
	}
	if a.err != nil {
		return s.interrupted(p, key, op.Revision)
	}
	// A close between proof return and commit cancels the commit context. The
	// final store transaction also fences boot, review, current deny/delegation.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return s.interrupted(p, key, op.Revision)
	}
	resultOp, e := s.store.CompleteProof(proofCtx, p, key, op.Revision, a.proof)
	s.mu.Unlock()
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return s.interrupted(p, key, op.Revision)
	}
	return resultOp, e
}

func (s *Service) interrupted(p Principal, key string, revision int64) (Operation, error) {
	record, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	// A committed/cancelled/newer operation rejects this diagnostic CAS. A lost
	// receipt never turns a known activation back into an unresolved attempt.
	_ = s.store.RecordProofFailure(record, p, key, revision)
	return Operation{}, problem("workspace_outcome_unknown", "candidate proof interrupted; inspect exact admission key "+key+", never silently replay")
}
