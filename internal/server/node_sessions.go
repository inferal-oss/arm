package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

type nodeSessionKey struct{}
type browserExpiryKey struct{}
type browserPrincipalKey struct{}

// nodeSession is an immutable authority binding. Tokens never travel to peers.
// Local and destination revisions are independent directional enrollment facts.
type nodeSession struct {
	expiresAt      time.Time
	principal      [32]byte
	Host           string `json:"host"`
	Source         string `json:"source_host"`
	Local          bool   `json:"local"`
	Pair           string `json:"pair,omitempty"`
	Pin            string `json:"certificate_fingerprint,omitempty"`
	LocalRevision  int    `json:"local_revision,omitempty"`
	RemoteRevision int    `json:"remote_revision,omitempty"`
}
type nodeTicket struct {
	Context nodeSession
	Nonce   string
	Expires time.Time
}
type nodeCredential struct {
	Context nodeSession
	Expires time.Time
}

func (b *browserCredentials) issueNode(context nodeSession, nonce string, now time.Time) (string, time.Time, error) {
	b.Lock()
	defer b.Unlock()
	if b.nodeTickets == nil {
		b.nodeTickets = map[[32]byte]nodeTicket{}
	}
	for key, t := range b.nodeTickets {
		if !now.Before(t.Expires) {
			delete(b.nodeTickets, key)
		}
	}
	if len(b.nodeTickets) >= 32 {
		return "", time.Time{}, model.Err("browser_busy", "too many pending node windows")
	}
	ticket := local.ID() + local.ID()
	expiry := now.Add(time.Minute)
	if !context.expiresAt.IsZero() {
		if !now.Before(context.expiresAt) {
			return "", time.Time{}, model.Err("browser_expired", "parent operator session expired")
		}
		if context.expiresAt.Before(expiry) {
			expiry = context.expiresAt
		}
	}
	b.nodeTickets[sha256.Sum256([]byte(ticket))] = nodeTicket{context, nonce, expiry}
	return ticket, expiry, nil
}
func (b *browserCredentials) exchangeNode(ticket, host, nonce string, now time.Time) (string, time.Time, *nodeSession, bool) {
	b.Lock()
	defer b.Unlock()
	key := sha256.Sum256([]byte(ticket))
	t, ok := b.nodeTickets[key]
	if !ok || len(ticket) != 64 || !now.Before(t.Expires) || t.Context.Host != host || t.Nonce != nonce {
		return "", time.Time{}, nil, false
	}
	pruneCredentials(b.sessions, now)
	b.pruneNodeSessions(now)
	if len(b.sessions)+len(b.nodeSessions) >= 64 {
		return "", time.Time{}, nil, false
	}
	delete(b.nodeTickets, key)
	if b.sessions == nil {
		b.sessions = map[[32]byte]time.Time{}
	}
	if b.nodeSessions == nil {
		b.nodeSessions = map[[32]byte]nodeCredential{}
	}
	token := local.ID() + local.ID()
	expiry := now.Add(browserLifetime)
	if !t.Context.expiresAt.IsZero() && t.Context.expiresAt.Before(expiry) {
		expiry = t.Context.expiresAt
	}
	digest := sha256.Sum256([]byte(token))
	b.nodeSessions[digest] = nodeCredential{t.Context, expiry}
	bound := t.Context
	return token, expiry, &bound, true
}
func browserToken(r *http.Request) string {
	var token string
	for _, p := range strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "arm.auth.") {
			if token != "" {
				return ""
			}
			token = strings.TrimPrefix(p, "arm.auth.")
		}
	}
	return token
}
func (b *browserCredentials) pruneNodeSessions(now time.Time) {
	for key, value := range b.nodeSessions {
		if !now.Before(value.Expires) {
			delete(b.nodeSessions, key)
		}
	}
}
func (s *Server) validateNodeSession(bound nodeSession) (*meshService, error) {
	if !bound.expiresAt.IsZero() && !time.Now().Before(bound.expiresAt) {
		return nil, model.Err("node_context_changed", "node session expired")
	}
	status, e := s.Manager.Status()
	if e != nil {
		return nil, e
	}
	if status.Host != bound.Source {
		return nil, model.Err("node_context_changed", "source authority changed; open this node again from Mesh")
	}
	if bound.Local {
		if bound.Host != status.Host {
			return nil, model.Err("wrong_host", "local node context differs")
		}
		return nil, nil
	}
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	if service == nil {
		return nil, model.Err("node_context_changed", "local mesh is unavailable; open this node again when restored")
	}
	peer, e := service.store.Resolve(bound.Host)
	if e != nil || peer.State != "active" || peer.Pair != bound.Pair || peer.Revision != bound.LocalRevision || peer.Identity.Fingerprint != bound.Pin {
		return nil, model.Err("node_context_changed", "enrollment or trust changed; open this node again from Mesh")
	}
	if _, e = mesh.ValidateIdentity(peer.Identity); e != nil {
		return nil, model.Err("node_context_changed", "peer certificate unavailable or expired")
	}
	return service, nil
}
func (s *Server) openNode(ctx context.Context, raw json.RawMessage) (any, error) {
	var q struct {
		Host  string `json:"host"`
		Nonce string `json:"window_nonce"`
	}
	if e := mesh.Decode(raw, &q); e != nil {
		return nil, e
	}
	if !exactNodeID(q.Host) || len(q.Nonce) != 32 || !exactNodeID(q.Nonce) {
		return nil, model.Err("invalid_request", "exact host and random 32-hex window nonce required")
	}
	bound, e := s.bindNodeContext(ctx, q.Host)
	if e != nil {
		return nil, e
	}
	ticket, expiry, e := s.browser.issueNode(bound, q.Nonce, time.Now())
	if e != nil {
		return nil, e
	}
	return map[string]any{"ticket": ticket, "expires_at": expiry, "context": bound}, nil
}

func (s *Server) bindNodeContext(ctx context.Context, host string) (nodeSession, error) {
	if !exactNodeID(host) {
		return nodeSession{}, model.Err("invalid_request", "exact stable node ID required")
	}
	entry, e := s.nodeInspect(ctx, host)
	if e != nil {
		return nodeSession{}, e
	}
	status, e := s.Manager.Status()
	if e != nil {
		return nodeSession{}, e
	}
	bound := nodeSession{Host: entry.Host, Source: status.Host, Local: entry.Local}
	if !entry.Local {
		if entry.Trust.State != "active" || entry.Outbound == nil || entry.ScopeFreshness.Stale || entry.Outbound.Source != status.Host || entry.Outbound.Target != entry.Host || entry.Outbound.Pair != entry.Trust.Pair {
			return nodeSession{}, model.Err("node_unavailable", "a fresh active enrollment and destination scope are required")
		}
		bound.Pair = entry.Trust.Pair
		bound.Pin = entry.Trust.Fingerprint
		bound.LocalRevision = entry.Trust.Revision
		bound.RemoteRevision = entry.Outbound.Revision
	}
	if _, e = s.validateNodeSession(bound); e != nil {
		return nodeSession{}, e
	}

	if expiry, ok := ctx.Value(browserExpiryKey{}).(time.Time); ok {
		bound.expiresAt = expiry
	}
	if principal, ok := ctx.Value(browserPrincipalKey{}).([32]byte); ok {
		bound.principal = principal
	}
	return bound, nil
}

func remoteNodeContext(ctx context.Context) bool {
	bound, ok := ctx.Value(nodeSessionKey{}).(nodeSession)
	return ok && !bound.Local
}

func (s *Server) watchNodeSession(ctx context.Context, cancel context.CancelFunc, bound nodeSession) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var err error
			if bound.Local {
				_, err = s.validateNodeSession(bound)
			} else {
				_, err = s.nodeSessionOperation(ctx, bound, model.WireRequest{Operation: "node_context", Params: json.RawMessage(`{}`)})
			}
			if err != nil {
				cancel()
				return
			}
		}
	}
}
