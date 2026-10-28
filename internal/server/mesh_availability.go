package server

// This component is prepared independently of Serve/UI activation. Its owner
// must retain it, invalidate changed peers and Close/join it before Manager.Close.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"net"
	"reflect"
	"slices"
	"sort"
	"sync"
	"syscall"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

// These observations are advisory. None is an admission token or a promise of
// completion, and no recovery path invokes a mutating mesh request.
type meshAvailabilityState struct {
	received              leaseclock.Sample             // private boot-scoped elapsed freshness anchor
	Host                  string                        `json:"host"`
	State                 string                        `json:"state"`
	Failure               string                        `json:"failure,omitempty"`
	Pair                  string                        `json:"pair,omitempty"`
	Revision              int                           `json:"revision,omitempty"`
	Fingerprint           string                        `json:"fingerprint,omitempty"`
	LastSuccess           *time.Time                    `json:"last_success_at,omitempty"`
	NextAttempt           *time.Time                    `json:"next_attempt_at,omitempty"`
	Scope                 *model.NodeAccessScope        `json:"scope,omitempty"`
	Resources             *meshResourceRead             `json:"resources,omitempty"`
	DeclarationState      string                        `json:"declaration_state"`
	Declared              *nodeavailability.Declaration `json:"owner_declared,omitempty"`
	declarationObservedAt *time.Time
}

// The existing status response contains lease details under some grants. Only
// this deliberately narrow projection is retained by the autonomous observer.
type meshResourceRead struct {
	Host         string          `json:"host"`
	ReportedAt   time.Time       `json:"reported_at"`
	CPU          *meshCPURead    `json:"cpu"`
	Memory       *meshMemoryRead `json:"memory"`
	Disk         *meshByteRead   `json:"disk"`
	Capabilities []string        `json:"capabilities,omitempty"`
}
type meshReadAge struct {
	ObservationAt time.Time     `json:"observation_at"`
	Stale         bool          `json:"stale"`
	Age           time.Duration `json:"age_ns"`
	StaleAfter    time.Duration `json:"stale_after_ns"`
}
type meshCPURead struct {
	meshReadAge
	Known     bool    `json:"known"`
	Available float64 `json:"available_cores"`
}
type meshByteRead struct {
	meshReadAge
	// Memory's existing response has quality but no known field. Unknown
	// quality must never become known merely because bytes default to zero.
	Known     bool   `json:"known"`
	Quality   string `json:"quality"`
	Available int64  `json:"available_bytes"`
}
type meshMemoryRead struct {
	meshByteRead
	EstimatedAvailable *int64 `json:"estimated_available_bytes"`
}

type meshAvailabilityAttempt struct {
	state     meshAvailabilityState
	service   *meshService
	identity  mesh.Identity
	failures  uint
	next      time.Time
	scheduled leaseclock.Sample
	nextTick  int64
}
type meshAvailability struct {
	activationMu   sync.Mutex // listener publication versus invalidation
	owner          *Server
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	mu             sync.Mutex
	peers          map[string]*meshAvailabilityAttempt
	listener       meshAvailabilityAttempt
	original       mesh.Config
	restore        bool
	cursor         string
	activeHost     string
	activeCancel   context.CancelFunc
	activeDone     chan struct{}
	clockLast      leaseclock.Sample
	clockError     error
	listenerCancel context.CancelFunc
	listenerDone   chan struct{}
	cancelled      map[<-chan struct{}]struct{}
}

// Capture Approval from the exact Store used for the failed startup attempt,
// under the startup administration lock. Never reread a newer approval here.
type meshStartupAttempt struct {
	Approval mesh.Config
	Err      error
}

func retryableMeshBind(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "listen" && (errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EADDRNOTAVAIL))
}

func newMeshAvailability(ctx context.Context, s *Server, startup meshStartupAttempt) *meshAvailability {
	ctx, cancel := context.WithCancel(ctx)
	m := &meshAvailability{owner: s, ctx: ctx, cancel: cancel, done: make(chan struct{}), peers: map[string]*meshAvailabilityAttempt{}}
	c := startup.Approval
	found := c.Identity.Host != ""
	m.original = mesh.Config{Identity: c.Identity, Listen: c.Listen}
	enabled := c.ListenerEnabled()
	m.original.Enabled = &enabled
	if c.Activation != nil {
		copy := *c.Activation
		m.original.Activation = &copy
	}
	m.restore = found && c.ListenerEnabled() && retryableMeshBind(startup.Err)
	m.listener.state.State = "idle"
	if m.restore {
		m.listener.state.State = "rejoining"
	} else if startup.Err != nil {
		m.listener.state.State, m.listener.state.Failure = "blocked", "listener_review_required"
	} else if !found {
		m.listener.state.State = "unconfigured"
	} else if !c.ListenerEnabled() {
		m.listener.state.State = "disabled"
	}
	go m.run()
	return m
}

// closeStartupMeshAvailability waits for startup to retain the singleton, then
// joins it outside administration exclusion. Restored TLS may accept Stop while
// Serve is still constructing the rest of its local interfaces.
func (s *Server) closeStartupMeshAvailability() {
	s.meshAdminMu.Lock()
	observer := s.meshAvailability.Load()
	s.meshAdminMu.Unlock()
	observer.Close()
}

func (m *meshAvailability) Close() {
	if m == nil {
		return
	}
	m.cancel()
	<-m.done
}

// CancelPeer is the nonjoining trust-change callback half. It may run while a
// store authorization writer is pending. The administrative caller must invoke
// JoinCancelled (or Invalidate) after releasing administration/store/mesh locks.
func (m *meshAvailability) CancelPeer(host string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for done := range m.cancelled {
		select {
		case <-done:
			delete(m.cancelled, done)
		default:
		}
	}
	if m.activeCancel != nil && m.activeHost == host {
		if m.cancelled == nil {
			m.cancelled = map[<-chan struct{}]struct{}{}
		}
		m.cancelled[m.activeDone] = struct{}{}
		m.activeCancel()
	}
	delete(m.peers, host)
}

// JoinCancelled captures only original callback cancellations already recorded
// at this boundary. New administrative callers retain/join their own handles.
// One sequential worker permits at most one unclosed probe handle; only
// positively closed receipts are pruned. Never call under administration locks.
func (m *meshAvailability) JoinCancelled() {
	if m == nil {
		return
	}
	m.mu.Lock()
	done := make([]<-chan struct{}, 0, len(m.cancelled))
	for original := range m.cancelled {
		done = append(done, original)
	}
	clear(m.cancelled)
	m.mu.Unlock()
	for _, original := range done {
		<-original
	}
}

// Invalidate cancels and joins the exact in-flight observation. Call only after
// releasing meshAdminMu, meshMu and store authorization locks: final generation
// check needs those locks. It never waits for remote execution or settles work.
func (m *meshAvailability) Invalidate(host string) {
	if m == nil {
		return
	}
	if host == "" {
		m.activationMu.Lock()
	}
	m.mu.Lock()
	var joined chan struct{}
	var listenerJoined chan struct{}
	if m.activeCancel != nil && (host == "" || m.activeHost == host) {
		m.activeCancel()
		joined = m.activeDone
	}
	if host == "" {
		clear(m.peers)
		m.restore = false
		m.listener.state.State = "disabled"
		if m.listenerCancel != nil {
			m.listenerCancel()
			listenerJoined = m.listenerDone
		}
	} else {
		delete(m.peers, host)
	}
	m.mu.Unlock()
	if host == "" {
		m.activationMu.Unlock()
	}
	if joined != nil {
		<-joined
	}
	if listenerJoined != nil {
		<-listenerJoined
	}
}

// Snapshot ages each nested resource observation without rewriting its source
// timestamp. A last-success observation alone cannot establish present capacity.
func (m *meshAvailability) Snapshot() (meshAvailabilityState, []meshAvailabilityState) {
	now, clockErr := m.readClock()
	m.owner.meshMu.Lock()
	service := m.owner.mesh
	var config mesh.Config
	if service != nil {
		config = service.store.Config()
	}
	m.owner.meshMu.Unlock()
	current := map[string]mesh.Peer{}
	authorized := map[string]bool{}
	for _, peer := range config.Peers {
		current[peer.Identity.Host] = peer
		if service != nil {
			_, err := service.store.WithPeer(peer.Identity.Host, func(live mesh.Peer) (any, error) {
				if live.Pair != peer.Pair || live.Revision != peer.Revision || !reflect.DeepEqual(live.Identity, peer.Identity) {
					return nil, model.Err("node_context_changed", "peer changed during snapshot")
				}
				return nil, nil
			})
			authorized[peer.Identity.Host] = err == nil
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	peers := make([]meshAvailabilityState, 0, len(m.peers))
	for _, p := range m.peers {
		peer, ok := current[p.state.Host]
		if service != nil && (!ok || peer.State != "active" || peer.Pair != p.state.Pair || peer.Revision != p.state.Revision || peer.Identity.Fingerprint != p.state.Fingerprint) {
			continue
		}
		var out meshAvailabilityState
		raw, _ := json.Marshal(p.state)
		_ = json.Unmarshal(raw, &out)
		out = projectAvailabilityPeer(out, p.identity, peer, time.Now())
		if service != nil && !authorized[peer.Identity.Host] {
			out.State, out.Failure, out.Resources, out.Scope = "unavailable", "peer_authorization_unavailable", nil, nil
			if out.Declared != nil {
				out.DeclarationState = "stale"
			}
		}
		elapsed, continuous := nodeObservationElapsed(p.state.received, now)
		if clockErr != nil || !continuous {
			elapsed = -1
		}
		if service == nil || !config.ListenerEnabled() {
			out.State, out.Failure, out.Resources, out.Scope = "offline", "local_mesh_unavailable", nil, nil
			if out.Declared != nil {
				out.DeclarationState = "stale"
			}
		}
		if service != nil && p.service != service {
			out.State, out.Failure, out.Resources, out.Scope = "unobserved", "local_mesh_generation_changed", nil, nil
			if out.Declared != nil {
				out.DeclarationState = "stale"
			}
		}
		if out.LastSuccess != nil && (elapsed < 0 || elapsed > nodeReadTTL) {
			if out.State == "reachable" {
				out.State = "stale"
			}
			if out.Declared != nil {
				out.DeclarationState = "stale"
			}
		}
		if out.Resources != nil && out.LastSuccess != nil {
			if out.Resources.CPU != nil {
				out.Resources.CPU.meshReadAge.advance(elapsed)
			}
			if out.Resources.Memory != nil {
				out.Resources.Memory.meshReadAge.advance(elapsed)
			}
			if out.Resources.Disk != nil {
				out.Resources.Disk.meshReadAge.advance(elapsed)
			}
		}
		peers = append(peers, out)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Host < peers[j].Host })
	var listener meshAvailabilityState
	raw, _ := json.Marshal(m.listener.state)
	_ = json.Unmarshal(raw, &listener)
	if service == nil && listener.State == "listening" {
		listener.State, listener.Failure = "offline", "local_mesh_unavailable"
	}
	return listener, peers
}

// Inspect is a cache-only proposed gateway read. It cannot initiate a probe,
// return a lease, transfer permissions or fall back to another selected host.
func (m *meshAvailability) Inspect(host string) (nodeavailability.Inspection, error) {
	if !exactNodeID(host) {
		return nodeavailability.Inspection{}, model.Err("invalid_request", "availability inspection requires an exact stable host ID")
	}
	_, peers := m.Snapshot()
	for _, state := range peers {
		if state.Host != host {
			continue
		}
		out := nodeavailability.Inspection{Schema: 1, Host: host, OwnerDeclared: state.Declared, DeclarationState: state.DeclarationState, ConnectionState: state.State, Failure: state.Failure, LastSuccess: state.LastSuccess, NextAttempt: state.NextAttempt, Resources: nodeavailability.ResourceStates{CPU: "unknown", Memory: "unknown", Disk: "unknown"}}
		if r := state.Resources; r != nil {
			if r.CPU != nil {
				out.Resources.CPU = availabilityResourceState(r.CPU.Known, r.CPU.Stale)
			}
			if r.Memory != nil {
				out.Resources.Memory = availabilityResourceState(r.Memory.EstimatedAvailable != nil && r.Memory.Quality == "estimated", r.Memory.Stale)
			}
			if r.Disk != nil {
				out.Resources.Disk = availabilityResourceState(r.Disk.Known, r.Disk.Stale)
			}
		}
		return out, nil
	}
	return nodeavailability.Inspection{}, model.Err("host_unavailable", "no observation exists for this exact current enrolled peer; no local fallback")
}

func availabilityResourceState(known, stale bool) string {
	if stale {
		return "stale"
	}
	if known {
		return "fresh"
	}
	return "unknown"
}

// Scheduling and freshness share native suspend-inclusive provenance. An
// uncertain clock latches unavailable for this owner; wall time cannot rebase it.
func (m *meshAvailability) readClock() (leaseclock.Sample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clockError != nil {
		return leaseclock.Sample{}, m.clockError
	}
	now, err := leaseclock.Read()
	if err == nil && (!now.Valid() || m.clockLast.Valid() && (now.Boot != m.clockLast.Boot || now.Tick < m.clockLast.Tick)) {
		err = model.Err("observation_clock_unavailable", "native observation clock continuity changed")
	}
	if err != nil {
		m.clockError = err
		return leaseclock.Sample{}, err
	}
	m.clockLast = now
	return now, nil
}

func (p *meshAvailabilityAttempt) due(now leaseclock.Sample) bool {
	if p.next.IsZero() {
		return true
	}
	return now.Valid() && p.scheduled.Valid() && now.Boot == p.scheduled.Boot && now.Tick >= p.scheduled.Tick && now.Tick >= p.nextTick
}

func (p *meshAvailabilityAttempt) schedule(now leaseclock.Sample, delay time.Duration) {
	if !now.Valid() || delay <= 0 || now.Tick > math.MaxInt64-int64(delay) {
		p.next = time.Time{}
		p.state.NextAttempt = nil
		return
	}
	p.scheduled, p.nextTick, p.next = now, now.Tick+int64(delay), now.Wall.Add(delay)
	next := p.next.UTC()
	p.state.NextAttempt = &next
}

func (a *meshReadAge) advance(elapsed time.Duration) {
	if elapsed < 0 || a.Age < 0 {
		a.Stale = true
		return
	}
	if elapsed > time.Duration(math.MaxInt64)-a.Age {
		a.Age = time.Duration(math.MaxInt64)
	} else {
		a.Age += elapsed
	}
	if a.StaleAfter <= 0 || a.Age > a.StaleAfter {
		a.Stale = true
	}
}

func recoveryDelay(failures uint) time.Duration {
	base := time.Second
	for i := uint(1); i < failures && base < 30*time.Second; i++ {
		base *= 2
	}
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base + time.Duration(rand.Int64N(int64(base))) // [base, 2*base), always <60s
}

func (m *meshAvailability) run() {
	defer close(m.done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			m.step()
			timer.Reset(time.Second) // one sequential peer probe per second at most
		}
	}
}

func sameListenerApproval(a, b mesh.Config) bool {
	return a.ListenerEnabled() && b.ListenerEnabled() && a.Listen == b.Listen && reflect.DeepEqual(a.Identity, b.Identity) && reflect.DeepEqual(a.Activation, b.Activation)
}

func (m *meshAvailability) step() {
	if m.ctx.Err() != nil {
		return
	}
	s := m.owner
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	if service == nil {
		m.restoreListener()
		return
	}
	c := service.store.Config()
	if !c.ListenerEnabled() {
		return
	}
	nativeNow, clockErr := m.readClock()
	if clockErr != nil {
		return
	}
	now := nativeNow.Wall
	eligible := map[string]mesh.Peer{}
	for _, peer := range c.Peers {
		if peer.State == "active" && peer.Proven {
			if _, err := service.store.WithPeer(peer.Identity.Host, func(current mesh.Peer) (any, error) { return nil, nil }); err == nil {
				eligible[peer.Identity.Host] = peer
			}
		}
	}
	m.mu.Lock()
	// Any successfully installed service consumes the startup recovery duty.
	// A later explicit close cannot resurrect it, even if another path opened it.
	m.restore = false
	if m.listener.state.State != "listening" {
		stamp := now.UTC()
		m.listener.state = meshAvailabilityState{State: "listening", LastSuccess: &stamp}
	}
	for host := range m.peers {
		if _, ok := eligible[host]; !ok {
			delete(m.peers, host)
		}
	}
	hosts := make([]string, 0, len(eligible))
	for host, peer := range eligible {
		p := m.peers[host]
		if p == nil || p.service != service || p.state.Pair != peer.Pair || p.state.Revision != peer.Revision || !reflect.DeepEqual(p.identity, peer.Identity) {
			p = &meshAvailabilityAttempt{service: service, identity: peer.Identity, state: meshAvailabilityState{Host: host, Pair: peer.Pair, Revision: peer.Revision, Fingerprint: peer.Identity.Fingerprint, State: "unobserved"}}
			m.peers[host] = p
		}
		if p.due(nativeNow) {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	if len(hosts) == 0 {
		m.mu.Unlock()
		return
	}
	chosen := hosts[0]
	for _, host := range hosts {
		if host > m.cursor {
			chosen = host
			break
		}
	}
	m.cursor = chosen
	p := m.peers[chosen]
	p.state.State = "rejoining"
	ctx, cancel := context.WithTimeout(m.ctx, nodeReadTimeout)
	joined := make(chan struct{})
	m.activeHost, m.activeCancel, m.activeDone = chosen, cancel, joined
	m.mu.Unlock()
	peer := eligible[chosen]
	observation, err := s.probeMeshAvailability(ctx, service, c.Identity.Host, peer)
	cancel()
	m.finish(service, peer, p, joined, observation, err)
}

func (m *meshAvailability) finish(service *meshService, peer mesh.Peer, p *meshAvailabilityAttempt, joined chan struct{}, observation meshAvailabilityState, probeErr error) {
	defer func() {
		m.mu.Lock()
		m.activeHost, m.activeCancel, m.activeDone = "", nil, nil
		close(joined)
		m.mu.Unlock()
	}()
	nativeNow, clockErr := m.readClock()
	if clockErr != nil {
		probeErr = model.Err("observation_clock_unavailable", "native observation clock continuity unavailable")
	}
	s := m.owner
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if s.mesh != service {
		m.mu.Lock()
		delete(m.peers, peer.Identity.Host)
		m.mu.Unlock()
		return
	}
	// Publish under the SAME current authorization lock. A preliminary Resolve
	// followed by a later cache assignment would allow revocation between them.
	_, authErr := service.store.WithPeer(peer.Identity.Host, func(current mesh.Peer) (any, error) {
		if !availabilityPeerValidAt(current, time.Now()) || current.Pair != peer.Pair || current.Revision != peer.Revision || !reflect.DeepEqual(current.Identity, peer.Identity) {
			return nil, model.Err("node_context_changed", "enrollment changed before publication")
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.peers[peer.Identity.Host] != p || m.ctx.Err() != nil {
			return nil, nil
		}
		if probeErr != nil {
			p.failures++
			p.state.State, p.state.Failure = availabilityFailure(probeErr)
			p.state.Resources, p.state.Scope = nil, nil
			if p.state.Declared != nil {
				p.state.DeclarationState = "stale"
			}
			p.schedule(nativeNow, recoveryDelay(p.failures))
		} else {
			p.failures = 0
			p.state = observation
			p.schedule(nativeNow, 10*time.Second+time.Duration(rand.Int64N(int64(5*time.Second))))
		}
		return nil, nil
	})
	if authErr != nil {
		m.mu.Lock()
		delete(m.peers, peer.Identity.Host)
		m.mu.Unlock()
	}
}

func availabilityFailure(err error) (string, string) {
	var api *model.Error
	if errors.As(err, &api) {
		switch api.Code {
		case "forbidden":
			return "denied", api.Code
		case "unsupported", "unsupported_operation":
			return "unsupported", api.Code
		case "node_context_changed":
			return "stale", api.Code
		case "host_unavailable":
			return "offline", api.Code
		case "identity_unavailable", "busy", "invalid_response", "response_too_large", "observation_clock_unavailable", "observation_timeout":
			return "unavailable", api.Code
		}
		return "unavailable", "remote_error"
	}
	return "unavailable", "observation_failed"
}

func availabilityPeerValidAt(peer mesh.Peer, now time.Time) bool {
	if peer.State != "active" || !peer.Proven || !now.Before(peer.Identity.Expires) || peer.Grant.Validate() != nil {
		return false
	}
	_, err := mesh.ValidateIdentity(peer.Identity)
	return err == nil
}

func projectAvailabilityPeer(out meshAvailabilityState, original mesh.Identity, current mesh.Peer, now time.Time) meshAvailabilityState {
	if !availabilityPeerValidAt(current, now) {
		out.State, out.Failure, out.Resources, out.Scope = "identity_unavailable", "identity_unavailable", nil, nil
		if out.Declared != nil {
			out.DeclarationState = "stale"
		}
	} else if !reflect.DeepEqual(original, current.Identity) {
		out.State, out.Failure, out.Resources, out.Scope = "stale", "node_context_changed", nil, nil
		out.Declared, out.DeclarationState = nil, "unsupported"
	}
	return out
}

func (m *meshAvailability) restoreListener() {
	nativeNow, clockErr := m.readClock()
	if clockErr != nil {
		return
	}
	m.mu.Lock()
	if !m.restore || !m.listener.due(nativeNow) {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, nodeReadTimeout)
	joined := make(chan struct{})
	m.listenerCancel, m.listenerDone = cancel, joined
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.listenerCancel, m.listenerDone = nil, nil
		close(joined)
		m.mu.Unlock()
	}()
	s := m.owner
	// A plain Mutex.Lock cannot be cancelled while a reviewed administration
	// operation owns it. Poll only this local lock with a bounded cancellable wait.
	for !s.meshAdminMu.TryLock() {
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	defer s.meshAdminMu.Unlock()
	m.activationMu.Lock()
	defer m.activationMu.Unlock()
	m.mu.Lock()
	valid := m.restore && ctx.Err() == nil
	m.mu.Unlock()
	check, clockErr := m.readClock()
	elapsed, continuous := nodeObservationElapsed(nativeNow, check)
	valid = valid && clockErr == nil && continuous && elapsed <= nodeReadTimeout
	if !valid {
		return
	}
	c, found, err := mesh.ReadRegistry(s.home)
	if err == nil && (!found || !sameListenerApproval(m.original, c)) {
		m.mu.Lock()
		m.restore = false
		m.listener.state.State = "disabled"
		m.mu.Unlock()
		return
	}
	if err == nil {
		err = s.meshAdmission()
	}
	if err == nil {
		check, clockErr = m.readClock()
		elapsed, continuous = nodeObservationElapsed(nativeNow, check)
		if ctx.Err() != nil || clockErr != nil || !continuous || elapsed > nodeReadTimeout {
			return
		}
		err = s.loadMesh()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.restore = false // later explicit closure must never resurrect this listener
		stamp := time.Now().UTC()
		m.listener.state = meshAvailabilityState{State: "listening", LastSuccess: &stamp}
		return
	}
	if !retryableMeshBind(err) {
		m.restore = false
		m.listener.state.State, m.listener.state.Failure = "blocked", "listener_review_required"
		return
	}
	m.listener.failures++
	m.listener.state.State, m.listener.state.Failure = "rejoining", "bind_unavailable"
	m.listener.schedule(nativeNow, recoveryDelay(m.listener.failures))
}

func (s *Server) probeMeshAvailability(ctx context.Context, service *meshService, source string, peer mesh.Peer) (meshAvailabilityState, error) {
	out := meshAvailabilityState{Host: peer.Identity.Host, Pair: peer.Pair, Revision: peer.Revision, Fingerprint: peer.Identity.Fingerprint}
	anchor, clockErr := leaseclock.Read()
	if clockErr != nil {
		return out, model.Err("observation_clock_unavailable", "native suspend-inclusive observation clock is unavailable")
	}
	leave, err := s.beginStopWork("inspection")
	if err != nil {
		return out, err
	}
	defer leave()
	if _, err := mesh.ValidateIdentity(peer.Identity); err != nil {
		return out, model.Err("identity_unavailable", "current peer identity cannot authenticate")
	}
	// Share the same global two-slot cap as chosen node inspection. Autonomous
	// recovery never waits holding a slot owned by another inspection.
	s.nodeMu.Lock()
	if s.nodeSlots == nil {
		s.nodeSlots = make(chan struct{}, 2)
		s.nodeInFlight = map[string]bool{}
		s.nodeCache = map[string]cachedNodeRead{}
	}
	if s.nodeInFlight[peer.Identity.Host] {
		s.nodeMu.Unlock()
		return out, model.Err("busy", "node inspection already in progress")
	}
	select {
	case s.nodeSlots <- struct{}{}:
	default:
		s.nodeMu.Unlock()
		return out, model.Err("busy", "observation slots occupied")
	}
	s.nodeInFlight[peer.Identity.Host] = true
	s.nodeMu.Unlock()
	defer func() { s.nodeMu.Lock(); delete(s.nodeInFlight, peer.Identity.Host); <-s.nodeSlots; s.nodeMu.Unlock() }()
	var scope model.NodeAccessScope
	raw, err := service.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)})
	if err != nil {
		return out, err
	}
	if err = decodeNodeRead(raw, &scope); err != nil {
		return out, err
	}
	if scope.Source != source || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 || scope.Observed.IsZero() {
		return out, model.Err("node_context_changed", "scope differs from original enrollment")
	}
	if err = scopeGrant(scope.Grant).Validate(); err != nil {
		return out, err
	}
	if !scopeGrant(scope.Grant).Has("observe-host") {
		return out, model.Err("forbidden", "resource observation is not granted")
	}
	raw, err = service.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: "status", Expected: &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}, Params: json.RawMessage(`{}`)})
	if err != nil {
		return out, err
	}
	// Decode into a narrow response projection; discarded status fields never
	// enter retained state or the availability snapshot.
	var resources meshResourceRead
	if len(raw) > 4<<20 || json.Unmarshal(raw, &resources) != nil || resources.Host != peer.Identity.Host || resources.ReportedAt.IsZero() {
		return out, model.Err("invalid_response", "resource report does not match the original destination")
	}
	out.DeclarationState = "unsupported"
	if slices.Contains(resources.Capabilities, nodeavailability.Capability) {
		raw, profileErr := service.store.Call(ctx, peer.Identity.Host, mesh.Request{Operation: nodeavailability.MetadataKey, Expected: &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}, Params: json.RawMessage(`{}`)})
		if profileErr == nil {
			var report nodeavailability.Report
			report, profileErr = nodeavailability.DecodeReport(raw, peer.Identity.Host)
			if profileErr == nil {
				out.Declared, out.DeclarationState = &report.OwnerDeclared, "reported"
				out.declarationObservedAt = &report.ObservedAt
			}
		}
		if profileErr != nil {
			state, _ := availabilityFailure(profileErr)
			if state != "denied" && state != "unsupported" {
				out.DeclarationState = "unavailable"
			}
		}
	}
	resources.Capabilities = nil // capability negotiation is not retained metadata
	now, clockErr := leaseclock.Read()
	transportAge, continuous := nodeObservationElapsed(anchor, now)
	if clockErr != nil || !continuous {
		return out, model.Err("observation_clock_unavailable", "observation clock continuity changed during read")
	}
	if transportAge > nodeReadTimeout {
		return out, model.Err("observation_timeout", "native whole-attempt deadline elapsed during read")
	}
	// Conservatively include the entire request duration; response timestamps
	// cannot remove network/optional-profile time from native resource age.
	if resources.CPU != nil {
		resources.CPU.meshReadAge.advance(transportAge)
	}
	if resources.Memory != nil {
		resources.Memory.meshReadAge.advance(transportAge)
	}
	if resources.Disk != nil {
		resources.Disk.meshReadAge.advance(transportAge)
	}
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if s.mesh != service {
		return out, model.Err("node_context_changed", "trust changed during observation")
	}
	_, err = service.store.WithPeer(peer.Identity.Host, func(current mesh.Peer) (any, error) {
		if !availabilityPeerValidAt(current, time.Now()) || current.Pair != peer.Pair || current.Revision != peer.Revision || !reflect.DeepEqual(current.Identity, peer.Identity) {
			return nil, model.Err("node_context_changed", "trust changed during observation")
		}
		stamp := now.Wall.UTC()
		out.received = now
		out.State, out.LastSuccess, out.Scope, out.Resources = "reachable", &stamp, &scope, &resources
		return nil, nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}
