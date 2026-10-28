package manager

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
)

const forwardListenerKind = "forward-listener"

// ForwardListener owns both the Manager's held TCP port and its admitted CPU
// and memory. The caller must close joined only after its original accept
// loop, connections, copy workers and authority watcher have all returned.
type ForwardListener struct {
	manager    *Manager
	id, key    string
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	joined     <-chan struct{}
	workerDone chan struct{}
	request    model.Request
	result     model.Lease // protected by manager.mu
	settled    bool
	unixChild  *ForwardUnixChild // one original child, protected by manager.mu
	unixIssued bool              // never reuse a child under this source
	unixFault  string            // sticky native cleanup uncertainty
}

func (h *ForwardListener) Context() context.Context { return h.ctx }
func (h *ForwardListener) Listener() net.Listener   { return h.listener }
func (h *ForwardListener) LeaseID() string          { return h.id }

// CloseOriginal interrupts native admission even if the ledger is temporarily
// unavailable. It never settles the durable resource charge.
func (h *ForwardListener) CloseOriginal() {
	if h != nil {
		h.cancel()
		_ = h.listener.Close()
	}
}
func (h *ForwardListener) Lease() (model.Lease, error) {
	if h == nil || h.manager == nil {
		return model.Lease{}, model.Err("invalid_request", "original forward listener required")
	}
	return h.manager.Inspect(h.id)
}

// Remaining fences new work with the exact current owner and native deadline.
// Context cancellation alone is not a suspend-inclusive entitlement check.
func (h *ForwardListener) Remaining(ctx context.Context) (time.Duration, error) {
	if h == nil || h.manager == nil || ctx == nil || ctx.Err() != nil {
		return 0, model.Err("lease_inactive", "live original listener required")
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	return h.remainingLocked(ctx)
}

// Caller holds Manager.mu. An original child uses this same exact-owner and
// suspend-inclusive clock check; a database row never mints a new handle.
func (h *ForwardListener) remainingLocked(ctx context.Context) (time.Duration, error) {
	m := h.manager
	l, err := m.get(h.id)
	if ctx == nil || ctx.Err() != nil || err != nil || m.closing || h.ctx.Err() != nil || h.settled || h.unixFault != "" || m.forwardListeners[h.id] != h || l.ID != h.id || l.PID != 0 || l.Process != nil || l.State != "active" || l.Revision != h.result.Revision || !reflect.DeepEqual(l.Clock, h.result.Clock) || !reflect.DeepEqual(l.Expires, h.result.Expires) || l.Request.Key != h.key || l.Host != h.result.Host || l.Owner != h.result.Owner || !reflect.DeepEqual(l.Request, h.request) {
		return 0, model.Err("lease_inactive", "original listener allocation changed")
	}
	remaining, err := m.leaseRemaining(l)
	if err != nil || remaining <= 0 {
		return 0, model.Err("lease_inactive", "elapsed listener entitlement unavailable")
	}
	return remaining, nil
}

func canonicalForwardHex(s string, bytes int) bool {
	if len(s) != 2*bytes || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func canonicalForwardService(s string) bool {
	if len(s) < 1 || len(s) > 64 || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if c != '-' && c != '_' && c != '.' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func (m *Manager) AcquireForwardListener(ctx context.Context, key, reason string, binding model.ForwardSourceBinding, ttl time.Duration, resources model.Resources, joined <-chan struct{}) (*ForwardListener, error) {
	if ctx == nil || joined == nil || !canonicalForwardHex(key, 16) || strings.TrimSpace(reason) == "" || len(reason) > 4096 || ttl <= 0 || ttl > 15*time.Minute || resources.CPU <= 0 || resources.Memory < 1<<20 || resources.Disk != 0 {
		return nil, model.Err("invalid_request", "bounded original source listener and resource promise required")
	}
	select {
	case <-joined:
		return nil, model.Err("invalid_request", "forward listener join already closed")
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !canonicalForwardHex(binding.Workspace, 16) || !canonicalForwardHex(binding.Target, 16) || !canonicalForwardService(binding.Service) || binding.GrantRevision < 1 || !canonicalForwardHex(binding.TokenSHA256, 32) {
		return nil, model.Err("invalid_request", "exact forward source binding required")
	}
	host, portText, err := net.SplitHostPort(binding.Bind)
	if err != nil {
		return nil, model.Err("invalid_request", "literal loopback source bind required")
	}
	ip := net.ParseIP(host)
	port, e := strconv.Atoi(portText)
	if e != nil || ip == nil || !ip.IsLoopback() || port < 0 || port > 65535 || strconv.Itoa(port) != portText {
		return nil, model.Err("invalid_request", "canonical literal loopback source port required")
	}
	network := "tcp4"
	if ip.To4() == nil {
		network = "tcp6"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.draining {
		return nil, model.Err("backend_stopping", "source listener admission closed")
	}
	var prior string
	err = m.db.QueryRow(`SELECT id FROM leases WHERE request_key=?`, "forward-listener:"+key).Scan(&prior)
	if err == nil {
		return nil, model.Err("idempotency_conflict", "original source listener key already recorded; inspect status")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	request := model.Request{Key: "forward-listener:" + key, Kind: forwardListenerKind, Reason: reason, Project: "mesh-forward", Resources: resources, TTL: ttl, ForwardSource: &binding,
		Ports: []model.PortClaim{{Name: "forward", Network: network, Address: host, Port: port, Mode: "hold"}}}
	lease, err := m.acquireWithRelay(request, nil, true)
	if err != nil {
		return nil, err
	}
	sockets := m.portSockets[lease.ID]
	if lease.State != "active" || len(sockets) != 1 || sockets[0].File == nil {
		_, _ = m.endPortParent(lease, "listener_admission_failed")
		return nil, model.Err("port_unavailable", "Manager did not retain one held source listener socket")
	}
	listener, err := net.FileListener(sockets[0].File)
	if err != nil {
		_, _ = m.endPortParent(lease, "listener_admission_failed")
		return nil, err
	}
	remaining, clockErr := m.leaseRemaining(lease)
	if clockErr != nil || remaining <= 0 {
		listener.Close()
		_, _ = m.endPortParent(lease, "listener_admission_failed")
		return nil, model.Err("lease_inactive", "elapsed entitlement unavailable")
	}
	life, cancel := context.WithTimeout(ctx, remaining)
	h := &ForwardListener{manager: m, id: lease.ID, key: request.Key, listener: listener, ctx: life, cancel: cancel, joined: joined, workerDone: make(chan struct{}), request: lease.Request, result: lease}
	if m.forwardListeners == nil {
		m.forwardListeners = map[string]*ForwardListener{}
	}
	m.forwardListeners[lease.ID] = h
	m.wg.Go(func() { defer close(h.workerDone); <-joined; _, _ = h.FinishAfterJoin() })
	return h, nil
}

// endForwardListener runs under Manager.mu. Cancellation and socket close do
// not certify reclamation; only FinishAfterJoin can settle the durable charge.
func (m *Manager) endForwardListener(l model.Lease, outcome string) (model.Lease, error) {
	h := m.forwardListeners[l.ID]
	if h == nil || h.key != l.Request.Key || l.State == "needs_attention" {
		return l, model.Err("needs_attention", "original listener owner and join proof unavailable; port and resources stay charged")
	}
	defer func() { h.cancel(); _ = h.listener.Close() }()
	l.State = "ending"
	l.Outcome = outcome
	l.Revision++
	if err := m.put(l, "forward.listener_ending"); err != nil {
		return l, err
	}
	h.result = l
	return l, nil
}

func (h *ForwardListener) FinishAfterJoin() (model.Lease, error) {
	if h == nil || h.manager == nil {
		return model.Lease{}, model.Err("invalid_request", "original listener owner required")
	}
	select {
	case <-h.joined:
	default:
		return model.Lease{}, model.Err("listener_not_joined", "original source listener and connections remain owned")
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.settled {
		return h.result, nil
	}
	if m.forwardListeners[h.id] != h {
		return model.Lease{}, model.Err("listener_owner_changed", "original listener owner changed; charge retained")
	}
	l, err := m.get(h.id)
	if err != nil {
		return l, err
	}
	if l.Request.Kind != forwardListenerKind || l.Request.Key != h.key || l.Host != h.result.Host || l.Owner != h.result.Owner || !consuming(l.State) || !reflect.DeepEqual(l.Request, h.request) {
		return l, model.Err("listener_owner_changed", "durable forward listener binding differs; charge retained")
	}
	if h.unixFault != "" {
		l.State, l.Ended = "needs_attention", nil
		l.Outcome, l.Failure = "forward_unix_cleanup_uncertain", h.unixFault
		l.Revision++
		if err = m.put(l, "forward.unix_cleanup_uncertain"); err != nil {
			return l, err
		}
		h.result = l
		return l, model.Err("needs_attention", "original Unix child cleanup remains uncertain; charge retained")
	}
	if h.unixChild != nil {
		return l, model.Err("listener_not_joined", "original Unix child has not proved cleanup")
	}
	h.cancel()
	_ = h.listener.Close()
	now := time.Now().UTC()
	l.State = "terminal"
	l.Ended = &now
	if l.Outcome == "" {
		l.Outcome = "forward_listener_closed"
	}
	m.finishPorts(&l)
	l.Revision++
	if err = m.put(l, "forward.listener_joined"); err != nil {
		return l, err
	}
	h.result, h.settled = l, true
	delete(m.forwardListeners, h.id)
	return l, nil
}

func (m *Manager) closeForwardListeners() error {
	m.mu.Lock()
	owners := make([]*ForwardListener, 0, len(m.forwardListeners))
	children := make([]*ForwardUnixChild, 0, len(m.forwardListeners))
	var result error
	for id, h := range m.forwardListeners {
		owners = append(owners, h)
		if h.unixChild != nil {
			children = append(children, h.unixChild)
		}
		_, err := m.end(id, "cancelled")
		result = errors.Join(result, err)
		h.CloseOriginal()
	}
	m.mu.Unlock()
	for _, c := range children {
		<-c.workerDone
	}
	for _, h := range owners {
		<-h.workerDone
		_, err := h.FinishAfterJoin()
		result = errors.Join(result, err)
	}
	return result
}

// RecoverForwardListener settles only a lost in-process source port/resource
// reservation after the backend has reacquired its exclusive daemon lock.
// It cannot certify or replay any application bytes that crossed the route.
// Callers must establish that process-lifetime fence before invoking it.
func (m *Manager) RecoverForwardListener(id, reason string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.get(id)
	if err != nil {
		return l, err
	}
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 || l.Request.Kind != forwardListenerKind || l.Request.ForwardSource == nil {
		return l, model.Err("invalid_request", "exact recovered source listener required")
	}
	if l.State == "terminal" {
		return l, nil
	}
	if l.State != "needs_attention" || m.forwardListeners[id] != nil || len(m.portSockets[id]) != 0 || l.PID != 0 || len(l.Ports) != 1 {
		return l, model.Err("needs_attention", "original listener may still own a socket or worker; charge retained")
	}
	if l.Outcome != "forward_listener_supervision_lost" && !strings.HasPrefix(l.Failure, "Port reclamation unresolved:") {
		return l, model.Err("needs_attention", "no exact recovered-listener cleanup evidence")
	}
	priorOutcome := l.Outcome
	l.State = "terminal"
	now := time.Now().UTC()
	l.Ended = &now
	l.Outcome = "reconciled"
	l.Failure = ""
	l.RecoveryReason = reason
	m.finishPorts(&l)
	if l.State != "terminal" {
		l.Outcome = priorOutcome
	}
	l.Revision++
	if err = m.put(l, "forward.listener_recovered"); err != nil {
		return l, err
	}
	if l.State != "terminal" {
		return l, model.Err("needs_attention", "exact source port remains occupied or unavailable; charge retained")
	}
	return l, nil
}
