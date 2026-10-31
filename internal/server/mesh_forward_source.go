package server

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/meshsshrelay"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

type forwardSourceRequest struct {
	Key           string        `json:"key"`
	Workspace     string        `json:"workspace"`
	Target        string        `json:"target"`
	Service       string        `json:"service"`
	GrantRevision int64         `json:"grant_revision"`
	Bind          string        `json:"bind"`
	TokenSHA256   string        `json:"token_sha256"`
	TTL           time.Duration `json:"ttl_ns"`
	Reason        string        `json:"reason"`
}

// The token digest is intentionally absent from every source status response.
type forwardSourceStatus struct {
	Key                   string     `json:"key"`
	Lease                 string     `json:"lease"`
	State                 string     `json:"state"`
	Outcome               string     `json:"outcome,omitempty"`
	Failure               string     `json:"failure,omitempty"`
	RecoveryReason        string     `json:"recovery_reason,omitempty"`
	Workspace             string     `json:"workspace"`
	Target                string     `json:"target"`
	Service               string     `json:"service"`
	GrantRevision         int64      `json:"grant_revision"`
	Bind                  string     `json:"bind"`
	Expires               *time.Time `json:"expires_at,omitempty"`
	Active                int        `json:"active_connections"`
	OriginalOwner         bool       `json:"original_owner"`
	LastOpenFailure       string     `json:"last_open_failure,omitempty"`
	LastConnectionFailure string     `json:"last_connection_failure,omitempty"`
}

type forwardSource struct {
	owner                 *meshService
	handle                *manager.ForwardListener
	key                   string
	binding               model.ForwardSourceBinding
	authority             forwardSourceAuthority
	localTLS              *tls.Config
	lastOpenFailure       string
	lastConnectionFailure string
	joined                chan struct{}
	done                  chan struct{}
	mu                    sync.Mutex
	active                map[net.Conn]struct{}
	closed                bool
	wg                    sync.WaitGroup
}

func forwardSourceView(l model.Lease, owner *forwardSource) forwardSourceStatus {
	b := l.Request.ForwardSource
	if b == nil {
		return forwardSourceStatus{}
	}
	v := forwardSourceStatus{Key: strings.TrimPrefix(l.Request.Key, "forward-listener:"), Lease: l.ID, State: l.State, Outcome: l.Outcome, Failure: l.Failure, RecoveryReason: l.RecoveryReason, Workspace: b.Workspace, Target: b.Target, Service: b.Service, GrantRevision: b.GrantRevision, Bind: b.Bind, Expires: l.Expires, OriginalOwner: owner != nil}
	if len(l.Ports) == 1 {
		v.Bind = net.JoinHostPort(l.Ports[0].Address, fmt.Sprint(l.Ports[0].Port))
	}
	if owner != nil {
		owner.mu.Lock()
		v.Active = len(owner.active)
		v.LastOpenFailure = owner.lastOpenFailure
		v.LastConnectionFailure = owner.lastConnectionFailure
		owner.mu.Unlock()
	}
	return v
}

func (m *meshService) forwardSourceLease(key string) (model.Lease, error) {
	if len(key) != 32 {
		return model.Lease{}, model.Err("invalid_request", "exact source listener key required")
	}
	status, err := m.owner.Manager.Status()
	if err != nil {
		return model.Lease{}, err
	}
	for _, l := range status.Leases {
		if l.Request.Kind == "forward-listener" && l.Request.Key == "forward-listener:"+key {
			return l, nil
		}
	}
	return model.Lease{}, model.Err("not_found", "source listener key not recorded")
}

func (m *meshService) forwardSourceOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if operation == "mesh_forward_recover" {
		return m.owner.forwardSourceHistoryOperation(operation, raw)
	}
	if operation == "mesh_forward_list" {
		var q struct{}
		if err := mesh.Decode(raw, &q); err != nil {
			return nil, err
		}
		status, err := m.owner.Manager.Status()
		if err != nil {
			return nil, err
		}
		m.forwardSourcesMu.Lock()
		defer m.forwardSourcesMu.Unlock()
		out := make([]forwardSourceStatus, 0)
		for _, l := range status.Leases {
			if l.Request.Kind == "forward-listener" && l.Request.ForwardSource != nil {
				out = append(out, forwardSourceView(l, m.forwardSources[strings.TrimPrefix(l.Request.Key, "forward-listener:")]))
			}
		}
		return out, nil
	}
	if operation == "mesh_forward_start" {
		var q forwardSourceRequest
		if err := mesh.Decode(raw, &q); err != nil {
			return nil, err
		}
		return m.startForwardSource(ctx, q)
	}
	var q struct {
		Key string `json:"key"`
	}
	if err := mesh.Decode(raw, &q); err != nil {
		return nil, err
	}
	l, err := m.forwardSourceLease(q.Key)
	if err != nil {
		return nil, err
	}
	if operation == "mesh_forward_status" {
		m.forwardSourcesMu.Lock()
		owner := m.forwardSources[q.Key]
		m.forwardSourcesMu.Unlock()
		return forwardSourceView(l, owner), nil
	}
	if operation != "mesh_forward_stop" {
		return nil, model.Err("invalid_request", "unknown TCP forward source operation")
	}
	m.forwardSourcesMu.Lock()
	owner := m.forwardSources[q.Key]
	m.forwardSourcesMu.Unlock()
	if owner == nil {
		if l.State == "terminal" {
			return forwardSourceView(l, nil), nil
		}
		return forwardSourceView(l, nil), model.Err("needs_attention", "original source listener owner unavailable; charge retained")
	}
	if _, err = m.owner.Manager.End(l.ID, "stopped"); err != nil {
		owner.handle.CloseOriginal()
		return nil, err
	}
	<-owner.done
	l, err = m.owner.Manager.Inspect(l.ID)
	return forwardSourceView(l, nil), err
}

func validForwardHex(s string, bytes int) bool {
	if len(s) != 2*bytes || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (m *meshService) startForwardSource(ctx context.Context, q forwardSourceRequest) (any, error) {
	if !meshsshrelay.ForwardReviewEnabled {
		return nil, model.Err("feature_unavailable", "TCP forwarding is disabled while acceptance gates remain incomplete")
	}
	if !validForwardHex(q.Key, 16) || q.Workspace == "" || q.Target == "" || q.Service == "" || q.GrantRevision < 1 || !validForwardHex(q.TokenSHA256, 32) || q.Reason == "" || q.TTL <= 0 || q.TTL > meshsshrelay.MaxLifetime {
		return nil, model.Err("invalid_request", "exact key, route, service revision, digest, reason and bounded TTL required")
	}
	if err := (meshforward.Endpoint{Name: q.Service, Network: "tcp", Address: "127.0.0.1:1"}).Validate(); err != nil {
		return nil, model.Err("invalid_request", err.Error())
	}
	localHost := m.store.Config().Identity.Host
	a, err := m.owner.Manager.WorkspaceRouteAccess(ctx, q.Workspace, localHost, q.Target, workspace.RouteForward)
	if err != nil {
		return nil, err
	}
	if a.Local.Host != localHost || a.Origin.Host != localHost || a.Destination.Host != q.Target {
		return nil, model.Err("forbidden", "original TCP source membership changed")
	}
	// Authenticate the current private Store identity before acquiring any
	// source port/resource lease. The original listener owns this TLS snapshot.
	localTLS, err := m.store.LocalForwardServerTLS()
	if err != nil {
		return nil, err
	}
	binding := model.ForwardSourceBinding{Workspace: q.Workspace, Target: q.Target, Service: q.Service, GrantRevision: q.GrantRevision, Bind: q.Bind, TokenSHA256: q.TokenSHA256}
	r := &forwardSource{owner: m, key: q.Key, binding: binding, authority: sourceAuthority(a), localTLS: localTLS, joined: make(chan struct{}), done: make(chan struct{}), active: map[net.Conn]struct{}{}}
	m.forwardSourcesMu.Lock()
	defer m.forwardSourcesMu.Unlock()
	if m.forwardSourcesClosed {
		return nil, model.Err("backend_stopping", "TCP forward listener admission closed")
	}
	if m.forwardSources[q.Key] != nil {
		return nil, model.Err("idempotency_conflict", "original source listener key already active; inspect status")
	}
	// The RPC context ends after this call. The Manager-owned context persists
	// until the listener expires, is stopped, or the backend closes.
	h, err := m.owner.Manager.AcquireForwardListener(context.Background(), q.Key, q.Reason, binding, q.TTL, model.Resources{CPU: .0001, Memory: 4 << 20}, r.joined)
	if err != nil {
		return nil, err
	}
	r.handle = h
	if m.forwardSources == nil {
		m.forwardSources = map[string]*forwardSource{}
	}
	m.forwardSources[q.Key] = r
	go r.run()
	l, err := h.Lease()
	if err != nil {
		_, _ = m.owner.Manager.End(h.LeaseID(), "start_failed")
		return nil, err
	}
	return forwardSourceView(l, r), nil
}

func (r *forwardSource) closeActive() {
	r.mu.Lock()
	r.closed = true
	for c := range r.active {
		_ = c.Close()
	}
	r.mu.Unlock()
}

func (r *forwardSource) run() {
	defer close(r.done)
	h := r.handle
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); <-h.Context().Done(); _ = h.Listener().Close(); r.closeActive() }()
	for {
		c, err := h.Listener().Accept()
		if err != nil {
			break
		}
		if _, err := h.Remaining(h.Context()); err != nil {
			_ = c.Close()
			break
		}
		r.mu.Lock()
		if r.closed || len(r.active) >= meshforward.MaxConnections {
			r.mu.Unlock()
			_ = c.Close()
			continue
		}
		r.active[c] = struct{}{}
		r.wg.Add(1)
		r.mu.Unlock()
		go func(conn net.Conn) {
			defer r.wg.Done()
			defer func() { r.mu.Lock(); delete(r.active, conn); r.mu.Unlock() }()
			openFailure, connectionFailure := serveForwardLocalTLS(h.Context(), conn, r.localTLS, r.binding.TokenSHA256, func(ctx context.Context) (net.Conn, error) { return r.open(ctx) })
			r.mu.Lock()
			r.lastOpenFailure = openFailure
			r.lastConnectionFailure = connectionFailure
			r.mu.Unlock()
		}(c)
	}
	h.CloseOriginal()
	_, _ = r.owner.owner.Manager.End(h.LeaseID(), "listener_closed")
	r.closeActive()
	r.wg.Wait()
	<-watchDone
	close(r.joined)
	_, _ = h.FinishAfterJoin()
	r.owner.forwardSourcesMu.Lock()
	delete(r.owner.forwardSources, r.key)
	r.owner.forwardSourcesMu.Unlock()
}

func (r *forwardSource) open(ctx context.Context) (*tls.Conn, error) {
	admitted, err := r.openAdmitted(ctx)
	if err != nil {
		return nil, err
	}
	return admitted.Conn(), nil
}

// openAdmitted gives a future Unix worker a typed original after pinned inner
// TLS and the destination's exact request acknowledgement. It receives no
// local accepted connection or application bytes.
func (r *forwardSource) openAdmitted(ctx context.Context) (*meshforward.AdmittedRoute, error) {
	if _, err := r.handle.Remaining(ctx); err != nil {
		return nil, err
	}
	remote, o, err := r.owner.forwardSourceDial(ctx, r.binding.Workspace, r.binding.Target, r.binding.Service, r.authority, func(check context.Context) error { _, err := r.handle.Remaining(check); return err })
	if err != nil {
		return nil, err
	}
	remaining, err := r.handle.Remaining(ctx)
	if err != nil {
		return nil, closeForwardOriginalAfterRefusal(remote, err)
	}
	binding, err := o.BindingDigest(time.Now())
	if err != nil {
		return nil, closeForwardOriginalAfterRefusal(remote, err)
	}
	ttl := time.Until(o.Expires)
	if remaining < ttl {
		ttl = remaining
	}
	if ttl > meshforward.MaxLifetime {
		ttl = meshforward.MaxLifetime
	}
	if ttl <= 0 {
		return nil, closeForwardOriginalAfterRefusal(remote, errors.New("TCP route expired"))
	}
	q := meshforward.Request{Version: 1, Workspace: o.Workspace, Generation: o.Generation, Source: o.Source, Target: o.Target, SourcePin: o.SourcePin, TargetPin: o.TargetPin, Service: o.Service, GrantRevision: r.binding.GrantRevision, ConnectionID: local.ID(), TTL: ttl, RouteBindingHash: binding}
	_ = remote.SetDeadline(time.Now().Add(meshsshrelay.RequestDeadline))
	var admitted *meshforward.AdmittedRoute
	if err = meshforward.WriteRequest(remote, q); err == nil {
		admitted, err = meshforward.ReadAdmittedRoute(remote, q)
	}
	if err != nil {
		return nil, closeForwardOriginalAfterRefusal(remote, err)
	}
	if _, err = r.handle.Remaining(ctx); err != nil {
		return nil, closeForwardOriginalAfterRefusal(remote, err)
	}
	if err := r.currentForwardAuthority(ctx); err != nil {
		return nil, closeForwardOriginalAfterRefusal(remote, err)
	}
	_ = remote.SetDeadline(time.Time{})
	return admitted, nil
}

func closeForwardOriginalAfterRefusal(remote *tls.Conn, reason error) error {
	applicationErr, nativeErr := meshsshrelay.CloseOriginalTLS(remote)
	return errors.Join(reason, applicationErr, nativeErr)
}

func (r *forwardSource) currentForwardAuthority(ctx context.Context) error {
	if _, err := r.handle.Remaining(ctx); err != nil {
		return err
	}
	current, err := r.owner.owner.Manager.WorkspaceRouteAccess(ctx, r.binding.Workspace, r.authority.Origin.Host, r.binding.Target, workspace.RouteForward)
	if err != nil || sourceAuthority(current) != r.authority {
		return model.Err("forbidden", "original forwarding membership changed after destination admission")
	}
	if _, err := r.handle.Remaining(ctx); err != nil {
		return err
	}
	return nil
}

func (m *meshService) stopForwardSources() {
	m.forwardSourcesMu.Lock()
	m.forwardSourcesClosed = true
	owners := make([]*forwardSource, 0, len(m.forwardSources))
	for _, r := range m.forwardSources {
		owners = append(owners, r)
	}
	m.forwardSourcesMu.Unlock()
	for _, r := range owners {
		l, err := r.handle.Lease()
		if err == nil {
			_, _ = m.owner.Manager.End(l.ID, "mesh_closed")
		}
		r.handle.CloseOriginal()
	}
	for _, r := range owners {
		<-r.done
	}
}

// Historical reads and explicit recovery remain available when the mesh
// listener is disabled. The live source is never adopted by a replacement
// mesh service; only the exact port/resource reservation can be reconciled.
func (s *Server) forwardSourceHistoryOperation(operation string, raw json.RawMessage) (any, error) {
	if operation == "mesh_forward_list" {
		var q struct{}
		if err := mesh.Decode(raw, &q); err != nil {
			return nil, err
		}
		status, err := s.Manager.Status()
		if err != nil {
			return nil, err
		}
		out := make([]forwardSourceStatus, 0)
		for _, l := range status.Leases {
			if l.Request.Kind == "forward-listener" && l.Request.ForwardSource != nil {
				out = append(out, forwardSourceView(l, nil))
			}
		}
		return out, nil
	}
	var q struct {
		Key    string `json:"key"`
		Reason string `json:"reason,omitempty"`
	}
	if err := mesh.Decode(raw, &q); err != nil {
		return nil, err
	}
	if !validForwardHex(q.Key, 16) {
		return nil, model.Err("invalid_request", "exact source listener key required")
	}
	status, err := s.Manager.Status()
	if err != nil {
		return nil, err
	}
	var found model.Lease
	for _, l := range status.Leases {
		if l.Request.Kind == "forward-listener" && l.Request.Key == "forward-listener:"+q.Key {
			found = l
			break
		}
	}
	if found.ID == "" {
		return nil, model.Err("not_found", "source listener key not recorded")
	}
	if operation == "mesh_forward_status" || operation == "mesh_forward_stop" && found.State == "terminal" {
		return forwardSourceView(found, nil), nil
	}
	if operation == "mesh_forward_recover" {
		if s.nodeStopLock == nil {
			return nil, model.Err("needs_attention", "exclusive original backend process fence unavailable")
		}
		l, err := s.Manager.RecoverForwardListener(found.ID, q.Reason)
		return forwardSourceView(l, nil), err
	}
	if operation == "mesh_forward_stop" {
		return forwardSourceView(found, nil), model.Err("needs_attention", "original listener owner unavailable; explicit recovery requires the exact key and port proof")
	}
	return nil, model.Err("invalid_request", "unknown TCP source history operation")
}

// The private source status retains one bounded original attempt, never the
// local token or application bytes. In particular an EOF is not evidence of
// a particular admission failure; preserve the actual opener rejection.
func forwardFailureText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 1024 {
		text = text[:1024]
	}
	return text
}

func serveForwardLocal(ctx context.Context, conn net.Conn, digest string, open func(context.Context) (net.Conn, error)) (openFailure, connectionFailure string) {
	return serveForwardLocalUsing(ctx, conn, digest, open, meshforward.ServeLocal)
}

func serveForwardLocalTLS(ctx context.Context, conn net.Conn, serverTLS *tls.Config, digest string, open func(context.Context) (net.Conn, error)) (openFailure, connectionFailure string) {
	return serveForwardLocalUsing(ctx, conn, digest, open, func(ctx context.Context, conn net.Conn, digest string, opener func(context.Context) (net.Conn, error)) error {
		return meshforward.ServeLocalCompatibleTLS(ctx, conn, serverTLS, digest, opener)
	})
}

func serveForwardLocalUsing(ctx context.Context, conn net.Conn, digest string, open func(context.Context) (net.Conn, error), serve func(context.Context, net.Conn, string, func(context.Context) (net.Conn, error)) error) (openFailure, connectionFailure string) {
	var openError error
	err := serve(ctx, conn, digest, func(ctx context.Context) (net.Conn, error) {
		remote, err := open(ctx)
		openError = err
		return remote, err
	})
	if err != nil {
		connectionFailure = "connection_failed"
	}
	return forwardFailureText(openError), connectionFailure
}
