package manager

import (
	"context"
	"sort"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/usage"
)

type workloadSample struct {
	view     model.WorkloadUsage
	observed time.Time
}

// Caller holds m.mu. Published snapshots are immutable; decorate a copy so
// status reads and final persistence cannot mutate the sampler's evidence.
func (m *Manager) attachUsage(l *model.Lease) {
	if observation, ok := m.usages[l.ID]; ok {
		sample := observation.view
		sample.Live = m.jobs[l.ID] != nil
		sample.Stale = time.Since(observation.observed) > 3*time.Second
		l.Usage = &sample
	}
}
func (m *Manager) Usage(id string) (model.UsageView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(id)
	if err != nil {
		return model.UsageView{}, err
	}
	result := model.UsageView{Lease: l.ID, Host: l.Host, State: l.State, Usage: l.Usage}
	if result.Usage == nil {
		result.Detail = "No workload observation is available; only ARM-supervised runs have attributable process usage"
		if j := m.jobs[id]; j != nil {
			result.Detail = "First observation pending; CPU requires two snapshots of the captured group"
			if j.identity == nil {
				result.Detail = j.observationError
			}
		} else if l.Process != nil {
			result.Detail = "No sample was captured before the supervised workload stopped; no usage is inferred from its saved PID"
		}
	}
	return result, nil
}
func (m *Manager) sampleUsageLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := map[string]usage.Snapshot{}
	offset := 0
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
		}
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			return
		}
		ids := make([]string, 0, len(m.jobs))
		jobs := map[string]*job{}
		for id, j := range m.jobs {
			ids = append(ids, id)
			jobs[id] = j
		}
		m.mu.Unlock()
		for id := range previous {
			if jobs[id] == nil {
				delete(previous, id)
			}
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			offset = 0
			continue
		}
		offset %= len(ids)
		// Bound each batch; rotate to give every job a chance when the host is busy.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		sampled := 0
		for sampled < len(ids) && sampled < 32 {
			select {
			case <-m.stop:
				cancel()
				return
			default:
			}
			if ctx.Err() != nil {
				break
			}
			id := ids[(offset+sampled)%len(ids)]
			j := jobs[id]
			sampled++
			var view model.WorkloadUsage
			observed := time.Now()
			if j.identity == nil {
				view = model.WorkloadUsage{Observed: time.Now().UTC(), Live: true, Detail: j.observationError, CPUQuality: "unavailable", MemoryQuality: "unavailable", Members: []model.ProcessUsage{}}
			} else {
				snapshot := usage.Collect(ctx, *j.identity)
				var prior *usage.Snapshot
				if old, ok := previous[id]; ok {
					prior = &old
				}
				view = usage.View(snapshot, prior)
				observed = snapshot.Finished
				previous[id] = snapshot
			}
			m.mu.Lock()
			if m.jobs[id] == j && !m.closing {
				m.usages[id] = workloadSample{view: view, observed: observed}
				if view.IdentityVerified && view.MemberCount > 0 {
					summary := view
					j.lastUsage = &summary
				}
				// Policy transitions and their notification outbox are durable;
				// collection itself never holds the admission lock.
				if err := m.evaluateUsage(id, view, observed); err != nil {
					failed := m.usages[id]
					failed.view.Detail += "; policy evaluation failed: " + err.Error()
					m.usages[id] = failed
				}
			}
			m.mu.Unlock()
		}
		cancel()
		offset = (offset + max(1, sampled)) % len(ids)
	}
}
