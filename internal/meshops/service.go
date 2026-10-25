package meshops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

// StartRequest contains a resolved native connector, never arbitrary browser
// executable/home/options. Browser adapters must resolve their closed draft.
type StartRequest struct {
	Key        string            `json:"key"`
	Kind       string            `json:"kind"`
	Connect    *ConnectRequest   `json:"connect,omitempty"`
	Reconnect  *ReconnectRequest `json:"reconnect,omitempty"`
	Disconnect *DisconnectDraft  `json:"disconnect,omitempty"`
	Provision  *ProvisionRequest `json:"provision,omitempty"`
}
type DisconnectDraft struct {
	Pair   string `json:"pair,omitempty"`
	Host   string `json:"host"`
	Reason string `json:"reason"`
}
type Decision struct {
	Key            string `json:"key"`
	ReviewRevision int64  `json:"review_revision"`
	ReviewHash     string `json:"review_sha256"`
	Approve        bool   `json:"approve"`
}

const PreparationNotice = "Preparation may start ordinary ARM backends and restore previously approved enabled listeners. New sockets are reserved without listening; new endpoints and peer trust require exact confirmation. Cancelling preserves ordinary backends, identities and pre-existing trust/listeners; new pending reservations are closed or expire."

type Operation struct {
	Notice          string                    `json:"notice"`
	ResumeFrom      string                    `json:"resume_from,omitempty"`
	ParentKey       string                    `json:"parent_key,omitempty"`
	ResolvedBy      string                    `json:"resolved_by,omitempty"`
	Key             string                    `json:"key"`
	Kind            string                    `json:"kind"`
	Revision        int64                     `json:"revision"`
	Phase           string                    `json:"phase"`
	Targets         []string                  `json:"targets"`
	Updated         time.Time                 `json:"updated_at"`
	WorkerLive      bool                      `json:"worker_live"`
	HasReview       bool                      `json:"has_review"`
	HasResult       bool                      `json:"has_result"`
	Review          json.RawMessage           `json:"review,omitempty"`
	ReviewRevision  int64                     `json:"review_revision"`
	ReviewHash      string                    `json:"review_sha256,omitempty"`
	ReviewExpires   time.Time                 `json:"review_expires_at,omitempty"`
	CancelRequested bool                      `json:"cancel_requested"`
	Approved        bool                      `json:"approved"`
	Result          json.RawMessage           `json:"result,omitempty"`
	RecoveryHash    string                    `json:"recovery_sha256,omitempty"`
	RecoveryEffect  string                    `json:"recovery_effect,omitempty"`
	Handoff         *mesh.CoordinationHandoff `json:"handoff,omitempty"`
	Error           *model.Error              `json:"error,omitempty"`
}

func Project(op mesh.Coordination) Operation {
	out := Operation{HasReview: len(op.Review) > 0, HasResult: len(op.Result) > 0, Notice: PreparationNotice, ResumeFrom: op.ResumeFrom, ParentKey: op.ParentKey, ResolvedBy: op.ResolvedBy, Key: op.Key, Kind: op.Kind, Revision: op.Revision, Phase: op.Phase, Targets: op.Targets, Updated: op.Updated, WorkerLive: op.Worker != "" && time.Now().Before(op.WorkerExpires), Review: op.Review, ReviewRevision: op.ReviewRevision, ReviewHash: op.ReviewHash, ReviewExpires: op.ReviewExpires, CancelRequested: op.CancelRequested, Approved: op.Approved, Result: op.Result, Error: op.Error}
	out.Handoff = op.Handoff
	if op.Error != nil {
		message := op.Error.Message
		if len(message) > 4096 {
			message = message[:4096]
		}
		out.Error = &model.Error{Code: op.Error.Code, Message: message}
	}
	if op.ResolvedBy == "" && !out.WorkerLive && (op.Phase == "incomplete" || op.Worker != "") {
		out.RecoveryHash, _ = mesh.CoordinationRecoveryHash(op)
		out.RecoveryEffect = mesh.CoordinationHandoffEffect
	}
	return out
}

func ProjectSummary(op mesh.Coordination) Operation {
	out := Project(op)
	out.Review = nil
	out.Result = nil
	return out
}

type Service struct {
	Home          string
	Context       context.Context
	New           func(context.Context) *Coordinator
	mu            sync.Mutex
	workers       map[string]context.CancelFunc
	closing       bool
	wg            sync.WaitGroup
	watchers      map[uint64]watcher
	watchSequence uint64
	watchCancel   context.CancelFunc
	lastChanges   map[string]Change
	closed        chan struct{}
}

func (s *Service) Get(key string) (Operation, error) {
	op, ok, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		return Operation{}, e
	}
	if !ok {
		return Operation{}, model.Err("not_found", "mesh operator key not found")
	}
	return Project(op), nil
}
func (s *Service) List() ([]Operation, error) {
	ops, e := mesh.ListCoordinations(s.Home)
	if e != nil {
		return nil, e
	}
	out := make([]Operation, 0, len(ops))
	for _, op := range ops {
		out = append(out, ProjectSummary(op))
	}
	return out, nil
}
func validateStart(q StartRequest) (any, string, error) {
	if !mesh.ValidKey(q.Key) {
		return nil, "", model.Err("invalid_request", "exact operation key is required")
	}
	switch q.Kind {
	case "connect":
		if q.Connect == nil || q.Connect.Reconnect != nil || q.Reconnect != nil || q.Disconnect != nil || q.Provision != nil {
			return nil, "", model.Err("invalid_request", "connect draft required")
		}
		r := q.Connect
		if r.Destination == "" || len(r.Destination) > 512 || strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 {
			return nil, "", model.Err("invalid_request", "bounded destination and reason required")
		}
		if e := (mesh.Connector{ID: "operator", Destination: r.Destination, Binary: r.Binary, Home: r.RemoteHome, Reason: r.Reason}).Validate(); e != nil {
			return nil, "", e
		}
		if e := r.Forward.Validate(); e != nil {
			return nil, "", e
		}
		if e := r.Reverse.Validate(); e != nil {
			return nil, "", e
		}
		return *r, "ssh:" + r.Destination, nil
	case "reconnect":
		if q.Reconnect == nil || q.Connect != nil || q.Disconnect != nil || q.Provision != nil {
			return nil, "", model.Err("invalid_request", "exact reconnect draft required")
		}
		if e := q.Reconnect.validate(); e != nil {
			return nil, "", e
		}
		return *q.Reconnect, "host:" + q.Reconnect.Draft.Host, nil
	case "disconnect":
		if q.Disconnect == nil || q.Connect != nil || q.Reconnect != nil || q.Provision != nil || len(q.Disconnect.Host) != 32 || strings.TrimSpace(q.Disconnect.Reason) == "" || len(q.Disconnect.Reason) > 4096 {
			return nil, "", model.Err("invalid_request", "exact stable target and reason required")
		}
		return struct{ Host, Pair, Reason string }{Host: q.Disconnect.Host, Pair: q.Disconnect.Pair, Reason: q.Disconnect.Reason}, "host:" + q.Disconnect.Host, nil
	default:
		return nil, "", model.Err("invalid_request", "operation must be connect, reconnect or disconnect")
	}
}
func (s *Service) Start(q StartRequest) (Operation, error) {
	immutable, e := json.Marshal(q)
	if e != nil {
		return Operation{}, e
	}
	if e = mesh.Decode(immutable, &q); e != nil {
		return Operation{}, e
	}
	canonical, target, e := validateStart(q)
	if e != nil {
		return Operation{}, e
	}
	digest, e := mesh.ReviewDigest(canonical)
	if e != nil {
		return Operation{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.Context.Err() != nil {
		return Operation{}, model.Err("backend_stopping", "operator coordinator is stopping")
	}
	if op, ok, e := mesh.ReadCoordination(s.Home, q.Key); e != nil {
		return Operation{}, e
	} else if ok {
		if op.Kind != q.Kind || op.Digest != digest {
			return Operation{}, model.Err("idempotency_conflict", "operator key belongs to another draft")
		}
		return Project(op), nil
	}
	if len(s.workers) >= 2 {
		return Operation{}, model.Err("busy", "at most two mesh operator workflows may run")
	}
	ctx, cancel := context.WithCancel(s.Context)
	c := s.New(ctx)
	if c == nil {
		cancel()
		return Operation{}, fmt.Errorf("missing mesh coordinator")
	}
	if e = c.begin(q.Key, q.Kind, digest, target); e != nil {
		cancel()
		return Operation{}, e
	}
	raw, e := json.Marshal(q)
	if e == nil {
		e = c.guard.Change(func(op *mesh.Coordination) error { op.Request = raw; return nil })
	}
	if e != nil {
		cancel()
		_ = c.saveResult(nil, e)
		return Operation{}, e
	}
	c.Machine = false
	c.Print = func(any) error { return nil }
	c.Confirm = func(plan any, yes, dry bool) error { return awaitReview(ctx, c.guard, plan) }
	if s.workers == nil {
		s.workers = make(map[string]context.CancelFunc)
	}
	s.workers[q.Key] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.workers, q.Key); s.mu.Unlock() }()
		if q.Kind == "connect" {
			_, _ = c.Connect(q.Key, *q.Connect, true, false)
		} else if q.Kind == "reconnect" {
			_, _ = c.Reconnect(q.Key, *q.Reconnect, true, false)
		} else {
			_, _ = c.Disconnect(q.Key, q.Disconnect.Host, q.Disconnect.Reason, true, false)
		}
	}()
	return s.Get(q.Key)
}
func awaitReview(ctx context.Context, g *mesh.CoordinationGuard, plan any) error {
	raw, e := json.Marshal(plan)
	if e != nil {
		return e
	}
	hash, e := mesh.ReviewDigest(plan)
	if e != nil {
		return e
	}
	expires := time.Now().UTC().Add(90 * time.Second)
	if e = g.Change(func(op *mesh.Coordination) error {
		op.Phase = "awaiting_review"
		op.Review = raw
		op.ReviewHash = hash
		op.ReviewExpires = expires
		op.ReviewRevision = op.Revision + 1
		return nil
	}); e != nil {
		return e
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		op, e := g.Read()
		if e != nil {
			return e
		}
		if op.CancelRequested {
			return context.Canceled
		}
		if op.Phase == "review_approved" && op.Approved {
			return nil
		}
		if op.Phase == "review_declined" {
			return model.Err("confirmation_declined", "operator declined this exact mesh review")
		}
		if !time.Now().Before(expires) {
			return model.Err("review_expired", "pending mesh review expired; exact reservations will be aborted")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (s *Service) Decide(q Decision) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return Operation{}, model.Err("backend_stopping", "coordinator is stopping")
	}
	if _, e := mesh.DecideCoordination(s.Home, q.Key, q.ReviewRevision, q.ReviewHash, q.Approve); e != nil {
		return Operation{}, e
	}
	return s.Get(q.Key)
}
func (s *Service) Cancel(key string, revision int64) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		return Operation{}, e
	}
	if !ok {
		return Operation{}, model.Err("not_found", "operator key not found")
	}
	if op.Revision != revision {
		return Operation{}, model.Err("review_changed", "operator revision changed; refresh first")
	}
	op, e = mesh.CancelCoordination(s.Home, key, revision)
	if e != nil {
		return Operation{}, e
	}
	if cancel := s.workers[key]; cancel != nil {
		cancel()
	}
	return Project(op), nil
}
func (s *Service) Close() {
	s.mu.Lock()
	wasClosing := s.closing
	s.closing = true
	if !wasClosing && s.closed != nil {
		close(s.closed)
	}
	if s.watchCancel != nil {
		s.watchCancel()
	}
	for _, cancel := range s.workers {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// Resume is an explicit new bounded attempt of the original immutable draft.
// It never changes the source receipt and does not accept replacement options.
func (s *Service) Resume(key string, revision int64) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.Context.Err() != nil {
		return Operation{}, model.Err("backend_stopping", "operator coordinator is stopping")
	}
	if len(s.workers) >= 2 {
		return Operation{}, model.Err("busy", "operator worker limit reached")
	}
	original, ok, e := mesh.ReadCoordination(s.Home, key)
	if e != nil {
		return Operation{}, e
	}
	if !ok {
		return Operation{}, model.Err("not_found", "operator key not found")
	}
	if original.Revision != revision || original.Phase != "incomplete" || original.Worker != "" || original.ResolvedBy != "" {
		return Operation{}, model.Err("review_changed", "only this exact inactive incomplete receipt may resume")
	}
	var q StartRequest
	if e = mesh.Decode(original.Request, &q); e != nil {
		return Operation{}, e
	}
	canonical, target, e := validateStart(q)
	if e != nil {
		return Operation{}, e
	}
	digest, e := mesh.ReviewDigest(canonical)
	if e != nil || digest != original.Digest {
		return Operation{}, model.Err("idempotency_conflict", "stored immutable intent does not match receipt")
	}
	ctx, cancel := context.WithCancel(s.Context)
	c := s.New(ctx)
	c.attemptParent = key
	if e = c.begin(key, q.Kind, digest, target); e != nil {
		cancel()
		return Operation{}, e
	}
	attemptKey := c.guard.Key()
	c.Machine = false
	c.Print = func(any) error { return nil }
	c.Confirm = func(plan any, yes, dry bool) error { return awaitReview(ctx, c.guard, plan) }
	if s.workers == nil {
		s.workers = make(map[string]context.CancelFunc)
	}
	s.workers[attemptKey] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.workers, attemptKey); s.mu.Unlock() }()
		if q.Kind == "connect" {
			_, _ = c.Connect(q.Key, *q.Connect, true, false)
		} else if q.Kind == "reconnect" {
			_, _ = c.Reconnect(q.Key, *q.Reconnect, true, false)
		} else {
			_, _ = c.Disconnect(q.Key, q.Disconnect.Host, q.Disconnect.Reason, true, false)
		}
	}()
	return s.Get(attemptKey)
}
