package server

import (
	"context"
	"sync"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
	api "getarm.org/internal/workspaceapi"
	"getarm.org/internal/workspacegraph"
)

type graphFlight struct {
	done   chan struct{}
	result api.RefreshResult
	err    error
}
type workspaceGraphState struct {
	mu         sync.Mutex
	life       context.Context
	slots      chan struct{}
	flights    map[string]*graphFlight
	witnesses  map[string][]workspacegraph.Observation
	started    bool
	background map[string]bool
}

func (s *Server) graphState() (*workspaceGraphState, error) {
	s.workspaceStreamsMu.Lock()
	defer s.workspaceStreamsMu.Unlock()
	if s.workspaceCallsClosed {
		return nil, model.Err("backend_stopping", "workspace graph lifecycle is closed")
	}
	if s.workspaceCallsContext == nil {
		life := s.lifecycleContext
		if life == nil {
			life = context.Background()
		}
		s.workspaceCallsContext, s.workspaceCallsCancel = context.WithCancel(life)
	}
	if s.graph == nil {
		s.graph = &workspaceGraphState{life: s.workspaceCallsContext, slots: make(chan struct{}, 2), flights: map[string]*graphFlight{}, witnesses: map[string][]workspacegraph.Observation{}, background: map[string]bool{}}
	}
	return s.graph, nil
}
func (g *workspaceGraphState) observations(id string) []workspacegraph.Observation {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := append([]workspacegraph.Observation{}, g.witnesses[id]...)
	for i := range out {
		out[i].Report.Neighbors = append([]workspacegraph.Neighbor{}, out[i].Report.Neighbors...)
	}
	return out
}
func (g *workspaceGraphState) publish(id string, next []workspacegraph.Observation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.life.Err() != nil {
		return
	}
	if _, ok := g.witnesses[id]; !ok && len(g.witnesses) >= workspace.MaxWorkspaces {
		return
	}
	current := map[string]workspacegraph.Observation{}
	for _, o := range g.witnesses[id] {
		current[o.Report.Source] = o
	}
	for _, o := range next {
		if old, ok := current[o.Report.Source]; ok && old.Boot == o.Boot {
			if old.Report.Sequence > o.Report.Sequence {
				continue
			}
			if old.Report.Sequence == o.Report.Sequence {
				o.Deadline = min(o.Deadline, old.Deadline)
			}
		}
		o.Report.Neighbors = append([]workspacegraph.Neighbor{}, o.Report.Neighbors...)
		current[o.Report.Source] = o
	}
	if len(current) > workspacegraph.MaxNodes {
		return
	}
	out := make([]workspacegraph.Observation, 0, len(current))
	for _, o := range current {
		out = append(out, o)
	}
	g.witnesses[id] = out
}
func (g *workspaceGraphState) coalesce(ctx context.Context, id string, fn func(context.Context) (api.RefreshResult, error)) (api.RefreshResult, error) {
	life, cancel := context.WithCancel(g.life)
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	defer cancel()
	ctx = life
	g.mu.Lock()
	if g.life.Err() != nil {
		g.mu.Unlock()
		return api.RefreshResult{}, model.Err("backend_stopping", "workspace graph is closing")
	}
	if f, ok := g.flights[id]; ok {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return api.RefreshResult{}, ctx.Err()
		case <-g.life.Done():
			return api.RefreshResult{}, g.life.Err()
		case <-f.done:
			return f.result, f.err
		}
	}
	if len(g.flights) >= workspace.MaxWorkspaces {
		g.mu.Unlock()
		return api.RefreshResult{}, model.Err("too_many_requests", "bounded graph refreshes occupied")
	}
	f := &graphFlight{done: make(chan struct{})}
	g.flights[id] = f
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.flights, id); close(f.done); g.mu.Unlock() }()
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	case <-ctx.Done():
		f.err = ctx.Err()
		return f.result, f.err
	case <-g.life.Done():
		f.err = g.life.Err()
		return f.result, f.err
	}
	f.result, f.err = fn(ctx)
	return f.result, f.err
}

// Only existing active bindings are refreshed. Startup inspection does not
// initialize identities/listeners/schema; no candidate is automatically proved,
// no join is finalized and no pending native message is retransmitted.
func (s *Server) startGraphRefresh() {
	g, e := s.graphState()
	if e != nil {
		return
	}
	s.workspaceStreamsMu.Lock()
	if s.workspaceCallsClosed || g.started {
		s.workspaceStreamsMu.Unlock()
		return
	}
	g.started = true
	s.workspaceCalls.Add(1)
	s.workspaceStreamsMu.Unlock()
	go func() {
		defer s.workspaceCalls.Done()
		tick := uint64(0)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-g.life.Done():
				return
			case <-timer.C:
			}
			interval := 15 * time.Second
			if _, e := s.workspaceService(); e == nil {
				read, stop := context.WithTimeout(g.life, 3*time.Second)
				records, e := s.Manager.WorkspaceRead(read)
				stop()
				if e == nil {
					for _, root := range records.Instances {
						if root.State != "active" {
							continue
						}
						interval = min(interval, root.Policy.RefreshInterval)
						id, timeout := root.ID, min(8*time.Second, root.Policy.MembershipTTL/2)
						// At most one scheduled waiter per workspace; an occupied
						// explicit/coalesced flight does not create ticker backlog.
						g.mu.Lock()
						busy := g.background[id] || g.flights[id] != nil
						if !busy {
							g.background[id] = true
						}
						g.mu.Unlock()
						if busy {
							continue
						}
						ctx, done, e := s.beginWorkspaceCall(g.life)
						if e != nil {
							g.mu.Lock()
							delete(g.background, id)
							g.mu.Unlock()
							return
						}
						go func() {
							defer done()
							defer func() { g.mu.Lock(); delete(g.background, id); g.mu.Unlock() }()
							ctx, stop := context.WithTimeout(ctx, timeout)
							defer stop()
							_, _ = s.workspaceRefresh(ctx, id)
						}()
					}
				}
			}
			tick++
			// Deterministic bounded early jitter avoids a synchronized 15s
			// cadence;1s is the explicit infrastructure lower bound.
			jitter := time.Duration(tick%11) * interval / 100
			timer.Reset(max(time.Second, interval-jitter))
		}
	}()
}
