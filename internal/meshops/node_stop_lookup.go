package meshops

import (
	"context"
	"encoding/json"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

// LookupStop is an explicit bounded inspection interval owned by this existing
// Service. Ordinary Get/list/watch never start SSH or automatically retry it.
func (s *Service) LookupStop(key string, revision int64) (Operation, error) {
	release := func() {}
	if s.Register != nil {
		var err error
		release, err = s.Register()
		if err != nil {
			return Operation{}, err
		}
	}
	defer release()
	s.mu.Lock()
	if s.closing || s.Context.Err() != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("backend_stopping", "operator coordinator is closing")
	}
	if len(s.workers) >= 2 || s.workers[key] != nil {
		s.mu.Unlock()
		return Operation{}, model.Err("worker_active", "original operation still has an active owner")
	}
	op, exists, err := mesh.ReadCoordination(s.Home, key)
	if err != nil {
		s.mu.Unlock()
		return Operation{}, err
	}
	if !exists || op.Kind != "stop" || op.Revision != revision || !op.Approved || op.Worker != "" && time.Now().Before(op.WorkerExpires) {
		s.mu.Unlock()
		return Operation{}, model.Err("lookup_only", "exact inactive approved Stop receipt required")
	}
	var review StopReview
	if err = nodestop.Decode(op.Review, &review); err != nil {
		s.mu.Unlock()
		return Operation{}, err
	}
	var request StartRequest
	if err = mesh.Decode(op.Request, &request); err != nil || request.Stop == nil || request.Kind != "stop" || request.Key != key || request.Stop.Host != review.Plan.Target.Host {
		s.mu.Unlock()
		return Operation{}, model.Err("idempotency_conflict", "original immutable Stop request differs")
	}
	if len(op.Result) > 0 {
		var prior StopResult
		if err = nodestop.Decode(op.Result, &prior); err != nil {
			s.mu.Unlock()
			return Operation{}, err
		}
		if err = prior.Validate(review); err != nil {
			s.mu.Unlock()
			return Operation{}, err
		}
		if prior.Status == "stopped" {
			s.mu.Unlock()
			return Project(op), nil
		}
	}
	ctx, cancel := context.WithTimeout(s.Context, 60*time.Second)
	c := s.New(ctx)
	if c == nil {
		cancel()
		s.mu.Unlock()
		return Operation{}, model.Err("node_stop_unavailable", "native Stop inspector unavailable")
	}
	if s.workers == nil {
		s.workers = map[string]context.CancelFunc{}
	}
	s.workers[key] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.workers, key); s.mu.Unlock(); s.wg.Done() }()
	result, inspectErr := c.inspectOriginalStop(review)
	if err = result.Validate(review); err != nil {
		return Operation{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return Operation{}, err
	}
	updated, err := mesh.RecordStopLookup(s.Home, key, revision, raw, inspectErr)
	if err != nil {
		return Operation{}, err
	}
	return Project(updated), nil
}
