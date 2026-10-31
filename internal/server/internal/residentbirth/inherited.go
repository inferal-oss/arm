package residentbirth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"

	"getarm.org/internal/residentrefs"
)

// DATA only, fixed names/layout. Actual root retains both concrete fence owners.
type P1FenceData struct {
	Home         string                       `json:"home"`
	Identities   residentrefs.FenceIdentities `json:"identities"`
	IntentBytes  int                          `json:"intent_bytes"`
	IntentSHA256 string                       `json:"intent_sha256"`
}

func (h *FreshFence) BootstrapOriginalData(prepared *PreparedFence) (P1FenceData, error) {
	if h == nil || h.owner == nil {
		return P1FenceData{}, errOriginalFence
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self != h || prepared == nil || prepared != o.prepared || prepared.owner != o || o.active != nil || o.firstError != nil || !o.bound || !o.intentCommitted || len(o.intent) == 0 || len(o.intent) > maxIntentBytes {
		return P1FenceData{}, errOriginalFence
	}
	ids := residentrefs.FenceIdentities{Launch: fenceSharedID(o.state.launch.id), Intent: fenceSharedID(o.state.journal.id), Home: fenceSharedID(o.state.home.id), Daemon: fenceSharedID(o.state.daemon.id), Directory: fenceSharedID(o.state.directory.id), Ledger: fenceSharedID(o.state.ledger.id)}
	hash := sha256.Sum256(o.intent)
	return P1FenceData{Home: o.path, Identities: ids, IntentBytes: len(o.intent), IntentSHA256: hex.EncodeToString(hash[:])}, nil
}
func fenceSharedID(id fenceIdentity) residentrefs.Identity {
	return residentrefs.Identity{Device: id.device, Inode: id.inode, UID: id.uid, GID: id.gid, Mode: id.mode}
}

type InheritedFencePending struct{ state *inheritedFenceState }
type inheritedFenceWaiter struct{ identity byte }
type inheritedFenceState struct {
	waiters               map[*inheritedFenceWaiter]struct{}
	mu                    sync.Mutex
	self                  *InheritedFencePending
	custody               *residentrefs.FenceOriginal
	journal               *residentrefs.JournalView
	rootContext           context.Context
	active                *inheritedFenceOperation
	verifyTried, verified bool
	expected              P1FenceData
	original              residentrefs.FenceIdentities
	intent                []byte
	bytesRead             int
	footerTried           bool
	footer                *p1RootBoundFooterCall
	currentTried          bool
	current               *p1CurrentFenceCall
	firstError            error
}
type inheritedFenceOperation struct {
	done           chan struct{}
	kind           string
	abnormal       bool
	normalFinished bool // final normal metadata only, never native closure
	cause          any
}

func NewInheritedFencePending(rootContext context.Context, custody *residentrefs.FenceOriginal) (*InheritedFencePending, error) {
	if rootContext == nil || custody == nil {
		return nil, errOriginalFence
	}
	p := &InheritedFencePending{}
	s := &inheritedFenceState{self: p, custody: custody, rootContext: rootContext}
	p.state = s
	journal, err := custody.ClaimIntentOriginalView()
	if err != nil {
		return nil, err
	}
	s.journal = journal
	return p, nil
}

// Request cancellation affects only queue admission; native effects retain the
// independent original root lifetime. No caller context replaces that lifetime.
func (p *InheritedFencePending) originalWaitContext() context.Context {
	if p == nil || p.state == nil {
		return nil
	}
	return p.state.rootContext
}
func (p *InheritedFencePending) begin(waitContext context.Context, kind string) (*inheritedFenceOperation, error) {
	if p == nil || p.state == nil || p.state.self != p || waitContext == nil {
		return nil, errOriginalFence
	}
	s := p.state
	waiter := &inheritedFenceWaiter{}
	queued := false
	defer func() {
		if queued {
			s.mu.Lock()
			delete(s.waiters, waiter)
			s.mu.Unlock()
		}
	}()
	for {
		s.mu.Lock()
		if err := waitContext.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if s.firstError != nil {
			s.mu.Unlock()
			return nil, s.firstError
		}
		if active := s.active; active != nil {
			if kind == "verify" {
				s.mu.Unlock()
				return nil, ErrFencePending
			}
			if !queued {
				if len(s.waiters) >= 64 {
					s.mu.Unlock()
					return nil, residentrefs.ErrBusy
				}
				if s.waiters == nil {
					s.waiters = make(map[*inheritedFenceWaiter]struct{})
				}
				s.waiters[waiter] = struct{}{}
				queued = true
			}
			done := active.done
			s.mu.Unlock()
			select {
			case <-waitContext.Done():
				return nil, waitContext.Err()
			case <-s.rootContext.Done():
				return nil, s.rootContext.Err()
			case <-done:
				continue
			}
		}
		if queued {
			delete(s.waiters, waiter)
			queued = false
		}
		if kind == "verify" {
			if s.verifyTried {
				s.mu.Unlock()
				return nil, errOriginalFence
			}
			s.verifyTried = true
		} else if !s.verified {
			s.mu.Unlock()
			return nil, errOriginalFence
		} else if kind == "rootbound-footer" {
			if s.footerTried || s.footer != nil || s.bytesRead != len(s.intent) || len(s.intent) == 0 {
				s.mu.Unlock()
				return nil, errOriginalFence
			}
			s.footerTried = true
		} else if kind == "p1-current-journal" {
			if s.currentTried || s.current != nil || s.footer == nil {
				s.mu.Unlock()
				return nil, errOriginalFence
			}
			s.currentTried = true
		}
		op := &inheritedFenceOperation{kind: kind, done: make(chan struct{})}
		s.active = op
		s.mu.Unlock()
		return op, nil
	}
}

func (s *inheritedFenceState) finish(op *inheritedFenceOperation, normal *bool, resultErr *error) {
	cause := recover()
	s.mu.Lock()
	if s.active != op {
		s.mu.Unlock()
		panic("original inherited fence operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if s.firstError == nil {
			s.firstError = &fenceAbnormal{cause: cause}
		}
	} else {
		if *resultErr != nil && !errors.Is(*resultErr, residentrefs.ErrBusy) && s.firstError == nil {
			s.firstError = *resultErr
		}
		if *resultErr == nil && op.kind == "verify" {
			s.verified = true
		}
		s.active = nil
		close(op.done)
		op.normalFinished = true
	}
	s.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}
func (p *InheritedFencePending) VerifyOriginal(ctx context.Context, expected P1FenceData) (resultErr error) {
	op, err := p.begin(ctx, "verify")
	if err != nil {
		return err
	}
	s := p.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	s.expected = expected
	resultErr = s.verifyBody(ctx)
	normal = true
	return resultErr
}
func (s *inheritedFenceState) verifyBody(ctx context.Context) error {
	if ctx == nil || s.expected.IntentBytes <= 0 || s.expected.IntentBytes > maxIntentBytes {
		return errOriginalFence
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.custody.MaterializeOriginal(ctx); err != nil {
		return err
	}
	ids, err := s.custody.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	if ids != s.expected.Identities {
		return errOriginalFence
	}
	s.original = ids
	before, err := s.journal.StatOriginal(ctx)
	if err != nil {
		return err
	}
	if before.Size() != int64(s.expected.IntentBytes) {
		return errOriginalFence
	}
	s.intent = make([]byte, s.expected.IntentBytes)
	for calls := 0; s.bytesRead < len(s.intent); calls++ {
		if calls >= 32 {
			return io.ErrNoProgress
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := s.journal.ReadAtOriginal(ctx, s.intent[s.bytesRead:], int64(s.bytesRead))
		s.bytesRead += n
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	digest := sha256.Sum256(s.intent)
	if hex.EncodeToString(digest[:]) != s.expected.IntentSHA256 {
		return errOriginalFence
	}
	if err = s.custody.ValidateEmptyLedgerOriginal(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// Uses the independent resident root's context, never a request/worker/launcher
// cancellation. Native issuer retains this ACTUAL fence pointer before binding.
func (p *InheritedFencePending) ValidateForOriginalNative() (home string, identities residentrefs.FenceIdentities, resultErr error) {
	op, err := p.begin(p.originalWaitContext(), "native-validation")
	if err != nil {
		return home, identities, err
	}
	s := p.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	identities, resultErr = s.custody.ValidateOriginal(s.rootContext)
	if resultErr == nil && identities != s.original {
		resultErr = errOriginalFence
	}
	if resultErr == nil {
		home = s.expected.Home
		resultErr = s.rootContext.Err()
	}
	normal = true
	return home, identities, resultErr
}

// The original database owner calls through its actual ManagerClaim immediately
// before physical driver entry; a copied empty-ledger DTO is insufficient.
func (p *InheritedFencePending) ValidateForOriginalDatabase() (resultErr error) {
	op, err := p.begin(p.originalWaitContext(), "database-before-connect")
	if err != nil {
		return err
	}
	s := p.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	current, err := s.custody.ValidateOriginal(s.rootContext)
	resultErr = err
	if resultErr == nil && current != s.original {
		resultErr = errOriginalFence
	}
	if resultErr == nil {
		resultErr = s.custody.ValidateEmptyLedgerOriginal(s.rootContext)
	}
	normal = true
	return resultErr
}

// Pure selectors from this ACTUAL verified inherited owner, not a constructor
// from them. Native issuer retains the actual pointer before opening its refs.
func (p *InheritedFencePending) NativeSelectorsOriginal() (P1FenceData, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return P1FenceData{}, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.verified || s.active != nil || s.firstError != nil {
		return P1FenceData{}, errOriginalFence
	}
	return s.expected, nil
}

// Bounded DATA copy for the actual original root's canonical cross-journal
// comparison. Neither the journal/file nor its reference-close owner is lent.
func (p *InheritedFencePending) IntentOriginalData() ([]byte, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return nil, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.verified || s.active != nil || s.firstError != nil || len(s.intent) == 0 || len(s.intent) > maxIntentBytes {
		return nil, errOriginalFence
	}
	return append([]byte(nil), s.intent...), nil
}
