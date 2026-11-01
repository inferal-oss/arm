package residentbirth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sync"

	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/internal/residentservice"
)

// WorkerFenceData is bounded bootstrap DATA, never native authority. It has a
// distinct domain and cannot enter a legacy P1 fence constructor or parser.
type WorkerFenceData struct {
	Schema       uint32                       `json:"schema"`
	Domain       string                       `json:"domain"`
	Home         string                       `json:"home"`
	Identities   residentrefs.FenceIdentities `json:"identities"`
	IntentBytes  int                          `json:"intent_bytes"`
	IntentSHA256 string                       `json:"intent_sha256"`
}

type WorkerInheritedFencePending struct{ state *workerInheritedFenceState }
type workerInheritedFenceState struct {
	currentTried          bool
	current               *workerCurrentFenceCall
	footerTried           bool
	footer                *workerRootBoundFooterCall
	mu                    sync.Mutex
	self                  *WorkerInheritedFencePending
	custody               *residentrefs.FenceOriginal
	journal               *residentrefs.JournalView
	rootContext           context.Context
	active                *workerInheritedFenceOperation
	waiters               map[*inheritedFenceWaiter]struct{}
	verifyTried, verified bool
	expected              WorkerFenceData
	original              residentrefs.FenceIdentities
	intent                []byte
	intentCopies          uint32
	decoded               residentservice.WorkerBirthIntentData
	bytesRead, readCalls  int
	before, after         os.FileInfo
	firstError            error
}
type workerInheritedFenceOperation struct {
	done           chan struct{}
	kind           string
	abnormal       bool
	normalFinished bool
	cause          any
}

func NewWorkerInheritedFencePending(destination **WorkerInheritedFencePending, rootContext context.Context, custody *residentrefs.FenceOriginal) error {
	if destination == nil || *destination != nil || rootContext == nil || custody == nil {
		return errOriginalFence
	}
	if err := custody.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	p := &WorkerInheritedFencePending{}
	*destination = p // retain actual pending owner before later state/view claims
	p.state = &workerInheritedFenceState{self: p, custody: custody, rootContext: rootContext}
	journal, err := custody.ClaimIntentOriginalView()
	p.state.journal = journal
	return err
}

func sameWorkerFenceContextData(a, b context.Context) bool {
	return a != nil && b != nil && reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
}

func (p *WorkerInheritedFencePending) begin(ctx context.Context, kind string) (*workerInheritedFenceOperation, error) {
	if p == nil || p.state == nil || p.state.self != p || !sameWorkerFenceContextData(ctx, p.state.rootContext) {
		return nil, errOriginalFence
	}
	s := p.state
	if err := s.custody.RequireOriginalWorkerCore(); err != nil {
		return nil, err
	}
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
		if err := ctx.Err(); err != nil {
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
			case <-ctx.Done():
				return nil, ctx.Err()
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
		} else {
			if !s.verified {
				s.mu.Unlock()
				return nil, errOriginalFence
			}
			switch kind {
			case "native-validation", "database-before-connect":
			case "worker-current-journal":
				if s.currentTried || s.current != nil || s.footer == nil || !s.completedFooterLocked(s.footer.service) {
					s.mu.Unlock()
					return nil, errOriginalFence
				}
				s.currentTried = true
			case "worker-rootbound-footer":
				if s.footerTried || s.footer != nil {
					s.mu.Unlock()
					return nil, errOriginalFence
				}
				s.footerTried = true
			default:
				s.mu.Unlock()
				return nil, errOriginalFence
			}
		}
		op := &workerInheritedFenceOperation{kind: kind, done: make(chan struct{})}
		s.active = op
		s.mu.Unlock()
		return op, nil
	}
}

func (s *workerInheritedFenceState) finish(op *workerInheritedFenceOperation, normal *bool, resultErr *error) {
	cause := recover()
	s.mu.Lock()
	if s.active != op {
		s.mu.Unlock()
		panic("original worker fence operation identity lost")
	}
	if !*normal {
		op.abnormal, op.cause = true, cause
		if s.firstError == nil {
			s.firstError = &fenceAbnormal{cause: cause}
		}
	} else {
		if *resultErr == nil {
			*resultErr = s.rootContext.Err()
		}
		if *resultErr != nil && !errors.Is(*resultErr, residentrefs.ErrBusy) && s.firstError == nil {
			s.firstError = *resultErr
		}
		if *resultErr == nil && op.kind == "verify" {
			s.verified = true
		}
		s.active = nil
		op.normalFinished = true
		close(op.done)
	}
	s.mu.Unlock()
	if cause != nil {
		panic(cause)
	}
}

func workerFenceShapeData(d WorkerFenceData) bool {
	if d.Schema != 2 || d.Domain != "arm-resident-worker-fence-v1" || d.IntentBytes <= 0 || d.IntentBytes > residentrefs.MaxWireBytes-36 || len(d.IntentSHA256) != 64 || d.Home == "" || len(d.Home) > 4096 {
		return false
	}
	sum, err := hex.DecodeString(d.IntentSHA256)
	return err == nil && len(sum) == 32 && hex.EncodeToString(sum) == d.IntentSHA256
}

// Canonical bounded DATA checks are shared with refusal tests. This cannot
// construct a native fence or supply any of the actual inherited identities.
func decodeWorkerFenceIntentData(raw []byte, expected WorkerFenceData) (residentservice.WorkerBirthIntentData, error) {
	var decoded residentservice.WorkerBirthIntentData
	if !workerFenceShapeData(expected) || len(raw) != expected.IntentBytes {
		return decoded, errOriginalFence
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expected.IntentSHA256 {
		return decoded, errOriginalFence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF || decoded.Validate() != nil {
		return residentservice.WorkerBirthIntentData{}, errOriginalFence
	}
	canonical, err := json.Marshal(decoded)
	if err != nil || !bytes.Equal(canonical, raw) || decoded.Home != expected.Home || decoded.Fence != expected.Identities {
		return residentservice.WorkerBirthIntentData{}, errOriginalFence
	}
	return decoded, nil
}

func (p *WorkerInheritedFencePending) VerifyOriginal(ctx context.Context, expected WorkerFenceData) (resultErr error) {
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

func (s *workerInheritedFenceState) verifyBody(ctx context.Context) error {
	if !workerFenceShapeData(s.expected) || s.journal == nil {
		return errOriginalFence
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
	s.before, err = s.journal.StatOriginal(ctx)
	if err != nil {
		return err
	}
	if s.before.Size() != int64(s.expected.IntentBytes) {
		return errOriginalFence
	}
	s.intent = make([]byte, s.expected.IntentBytes)
	for s.bytesRead < len(s.intent) {
		if s.readCalls >= 32 {
			return io.ErrNoProgress
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		s.readCalls++
		n, readErr := s.journal.ReadAtOriginal(ctx, s.intent[s.bytesRead:], int64(s.bytesRead))
		if n < 0 || n > len(s.intent)-s.bytesRead {
			return errOriginalFence
		}
		s.bytesRead += n
		if readErr != nil && !(errors.Is(readErr, io.EOF) && s.bytesRead == len(s.intent)) {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	s.after, err = s.journal.StatOriginal(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(s.before, s.after) || s.before.Size() != s.after.Size() || !s.before.ModTime().Equal(s.after.ModTime()) {
		return errOriginalFence
	}
	s.decoded, err = decodeWorkerFenceIntentData(s.intent, s.expected)
	if err != nil || s.decoded.Fence != ids {
		return errOriginalFence
	}
	if err = s.custody.ValidateEmptyLedgerOriginal(ctx); err != nil {
		return err
	}
	current, err := s.custody.ValidateOriginal(ctx)
	if err != nil {
		return err
	}
	if current != ids {
		return errOriginalFence
	}
	return ctx.Err()
}

func (p *WorkerInheritedFencePending) ValidateForOriginalNative() (home string, identities residentrefs.FenceIdentities, resultErr error) {
	if p == nil || p.state == nil {
		return home, identities, errOriginalFence
	}
	s := p.state
	op, err := p.begin(s.rootContext, "native-validation")
	if err != nil {
		return home, identities, err
	}
	normal := false
	defer s.finish(op, &normal, &resultErr)
	identities, resultErr = s.custody.ValidateOriginal(s.rootContext)
	if resultErr == nil && identities != s.original {
		resultErr = errOriginalFence
	}
	if resultErr == nil {
		home = s.expected.Home
	}
	normal = true
	return home, identities, resultErr
}

func (p *WorkerInheritedFencePending) ValidateForOriginalDatabase() (resultErr error) {
	if p == nil || p.state == nil {
		return errOriginalFence
	}
	s := p.state
	op, err := p.begin(s.rootContext, "database-before-connect")
	if err != nil {
		return err
	}
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

func (p *WorkerInheritedFencePending) NativeSelectorsOriginal() (WorkerFenceData, error) {
	if p == nil || p.state == nil || p.state.self != p {
		return WorkerFenceData{}, errOriginalFence
	}
	s := p.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.verified || s.active != nil || s.firstError != nil {
		return WorkerFenceData{}, errOriginalFence
	}
	return s.expected, nil
}

// Crosses only actual original opaque holders. Matching DATA or a native ID
// cannot substitute for service and fence recipients of the same fixed13 claim.
func (p *WorkerInheritedFencePending) ValidateOriginalBirth(view *residentservice.WorkerServeBirthView) (residentservice.WorkerBirthSelectorsData, error) {
	if p == nil || p.state == nil || p.state.self != p || view == nil {
		return residentservice.WorkerBirthSelectorsData{}, errOriginalFence
	}
	s := p.state
	selectors, err := view.ValidateOriginalFence(s.rootContext, s.custody)
	if err != nil {
		return selectors, err
	}
	s.mu.Lock()
	valid := s.verified && s.active == nil && s.firstError == nil && selectors.Home == s.expected.Home && selectors.IntentSHA256 == s.expected.IntentSHA256 && selectors.DebitID == s.decoded.ServiceDebitID && selectors.ProfileSHA256 == s.decoded.ProfileSHA256 && selectors.ContractSHA256 == s.decoded.ContractSHA256
	s.mu.Unlock()
	if !valid {
		return residentservice.WorkerBirthSelectorsData{}, errOriginalFence
	}
	return selectors, nil
}

// IntentOriginalData lends bounded DATA only. The actual root consumes two
// copies: initial cross-journal binding and prepared-state comparison. It lends
// no journal/File/FD, closer, original issuer or live native-state authority.
func (p *WorkerInheritedFencePending) IntentOriginalData() ([]byte, error) {
	if p == nil || p.state == nil || p.state.self != p || p.state.custody == nil || p.state.rootContext == nil {
		return nil, errOriginalFence
	}
	s := p.state
	if err := s.custody.RequireOriginalWorkerCore(); err != nil {
		return nil, err
	}
	if err := s.rootContext.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.verified || !s.verifyTried || s.active != nil || s.firstError != nil || !workerFenceShapeData(s.expected) || len(s.intent) != s.expected.IntentBytes || s.bytesRead != len(s.intent) || s.readCalls <= 0 || s.readCalls > 32 || s.before == nil || s.after == nil || s.journal == nil || s.original != s.expected.Identities || s.decoded.Validate() != nil || s.intentCopies >= 2 {
		return nil, errOriginalFence
	}
	s.intentCopies++ // retain the original bounded allocation attempt first
	return workerIntentSnapshotData(s.intent)
}

// Private DATA copy helper only. Actual original lineage remains mandatory in
// IntentOriginalData; this helper cannot produce a view, native claim or proof.
func workerIntentSnapshotData(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > residentrefs.MaxWireBytes-36 {
		return nil, errOriginalFence
	}
	return append([]byte(nil), raw...), nil
}
