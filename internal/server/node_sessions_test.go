package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

func TestNodeWindowTicketExactTargetNonceSingleUse(t *testing.T) {
	var b browserCredentials
	now := time.Now()
	bound := nodeSession{Host: strings.Repeat("a", 32), Source: strings.Repeat("b", 32), Pair: "pair", Pin: "pin", LocalRevision: 2, RemoteRevision: 3}
	ticket, _, e := b.issueNode(bound, strings.Repeat("c", 32), now)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, ok := b.exchangeNode(ticket, bound.Host, strings.Repeat("d", 32), now); ok {
		t.Fatal("wrong rendezvous accepted")
	}
	if _, _, _, ok := b.exchangeNode(ticket, bound.Source, strings.Repeat("c", 32), now); ok {
		t.Fatal("wrong host accepted")
	}
	if _, _, ok := b.exchange(ticket, now); ok {
		t.Fatal("scoped ticket became local authority")
	}
	token, _, got, ok := b.exchangeNode(ticket, bound.Host, strings.Repeat("c", 32), now)
	if !ok || len(token) != 64 || *got != bound {
		t.Fatal("bound exchange failed", got)
	}
	if _, _, _, ok = b.exchangeNode(ticket, bound.Host, strings.Repeat("c", 32), now); ok {
		t.Fatal("ticket replayed")
	}
	ticket, _, _ = b.issueNode(bound, "nonce", now)
	if _, _, _, ok = b.exchangeNode(ticket, bound.Host, "nonce", now.Add(time.Minute)); ok {
		t.Fatal("expired ticket accepted")
	}
}
func scopedRegistry(t *testing.T) (*Server, *Server, *mesh.Store, *mesh.Store, nodeSession) {
	t.Helper()
	summary := mesh.Grant{Permissions: []string{"observe-host"}}
	left, right, l, r := registryPair(t, summary, summary)
	host := r.Config().Identity.Host
	entry, e := left.nodeInspect(t.Context(), host)
	if e != nil || entry.Outbound == nil {
		t.Fatal(entry, e)
	}
	return left, right, l, r, nodeSession{Host: host, Source: l.Config().Identity.Host, Pair: entry.Trust.Pair, Pin: entry.Trust.Fingerprint, LocalRevision: entry.Trust.Revision, RemoteRevision: entry.Outbound.Revision}
}
func TestNodeWindowReadGatewayNoLocalFallbackOrAdministration(t *testing.T) {
	left, right, l, r, bound := scopedRegistry(t)
	if _, e := right.Manager.Acquire(model.Request{Host: bound.Host, Reason: "PRIVATE_TARGET_REASON", Resources: model.Resources{CPU: 0.125}, TTL: time.Minute}, nil); e != nil {
		t.Fatal(e)
	}
	ctx := context.WithValue(t.Context(), nodeSessionKey{}, bound)
	for _, op := range []string{"subscribe", "monitor_subscribe", "monitor_unsubscribe", "mesh_operator_watch", "node_list", "node_inspect", "mesh_dispatch", "node_open", "pool_list", "budget_list", "messages", "agents", "events", "storage_list", "lease_action", "message_send", "acquire", "run", "stop"} {
		_, e := left.dispatch(ctx, model.WireRequest{Operation: op, Params: json.RawMessage(`{}`)})
		if e == nil {
			t.Fatalf("remote context admitted local operation %s", op)
		}
	}
	value, e := left.dispatch(ctx, model.WireRequest{Operation: "status", Params: json.RawMessage(`{}`)})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(value)
	var status struct {
		Host   string `json:"host"`
		Leases []any  `json:"leases"`
	}
	if strings.Contains(string(raw), "PRIVATE_TARGET_REASON") || strings.Contains(string(raw), right.home) || json.Unmarshal(raw, &status) != nil || status.Host != bound.Host || len(status.Leases) != 0 {
		t.Fatal(string(raw))
	}

	changed := bound
	changed.RemoteRevision++
	if _, e = left.dispatch(context.WithValue(t.Context(), nodeSessionKey{}, changed), model.WireRequest{Operation: "status", Params: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("changed destination grant revision accepted")
	}
	if _, e = left.dispatch(ctx, model.WireRequest{Operation: "status", Params: json.RawMessage(`{"host":"localhost"}`)}); e == nil {
		t.Fatal("nested target selector accepted")
	}
	peer, _ := l.Resolve(bound.Host)
	if e = l.Revoke(bound.Host, "scope test"); e != nil {
		t.Fatal(e)
	}
	if _, e = left.dispatch(ctx, model.WireRequest{Operation: "status", Params: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("revoked session accepted")
	}
	if _, e = l.PairReviewed(r.Config().Identity, peer.Grant, strings.Repeat("b", 64), "new enrollment", true); e != nil {
		t.Fatal(e)
	}
	if _, e = left.validateNodeSession(bound); e == nil {
		t.Fatal("old context bound to replacement enrollment")
	}
}
func TestNodeWindowWebSocketScopesSpecialSubscriptionsAndRevocation(t *testing.T) {
	left, _, l, _, bound := scopedRegistry(t)
	ticket, _, e := left.browser.issueNode(bound, strings.Repeat("c", 32), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	token, _, _, ok := left.browser.exchangeNode(ticket, bound.Host, strings.Repeat("c", 32), time.Now())
	if !ok {
		t.Fatal("exchange failed")
	}
	h := httptest.NewServer(http.HandlerFunc(left.ws))
	defer h.Close()
	left.authority = strings.TrimPrefix(h.URL, "http://")
	ctx, stop := context.WithTimeout(t.Context(), 8*time.Second)
	defer stop()
	c, _, e := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{h.URL}}, Subprotocols: []string{"arm.v1", "arm.auth." + token}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.CloseNow()
	for _, op := range []string{"subscribe", "monitor_subscribe", "monitor_unsubscribe", "mesh_operator_watch"} {
		if e = wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: op, Operation: op, Params: json.RawMessage(`{}`)}); e != nil {
			t.Fatal(e)
		}
		var response model.WireResponse
		if e = wsjson.Read(ctx, c, &response); e != nil || response.ID != op || response.Error == nil || response.Event != nil || response.Telemetry != nil || response.Subscription != "" {
			t.Fatal("special path escaped scope", response, e)
		}
	}
	if e = l.Revoke(bound.Host, "scope close test"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = c.Read(ctx); e == nil || ctx.Err() != nil {
		t.Fatal("revoked node window stayed open", e)
	}
}

func TestNodeWindowAdmissionBindingSurvivesExpiryPruningWithoutLocalFallback(t *testing.T) {
	s := &Server{}
	now := time.Now()
	bound := nodeSession{Host: strings.Repeat("a", 32), Source: strings.Repeat("b", 32), expiresAt: now.Add(time.Second)}
	ticket, _, e := s.browser.issueNode(bound, "nonce", now)
	if e != nil {
		t.Fatal(e)
	}
	token, expiry, _, ok := s.browser.exchangeNode(ticket, bound.Host, "nonce", now)
	if !ok || !expiry.Equal(bound.expiresAt) {
		t.Fatal("child extended parent lifetime")
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/v1/ws", nil)
	request.Header.Set("Origin", "http://127.0.0.1:1")
	request.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token)
	admitted, ok := s.authenticateAdmission(request, now)
	if !ok || admitted.Node == nil || admitted.Node.Host != bound.Host {
		t.Fatal("scope not atomically captured")
	}
	// Deterministically advance pruning between authentication and WS admission.
	s.browser.Lock()
	s.browser.pruneNodeSessions(now.Add(time.Minute))
	pruneCredentials(s.browser.sessions, now.Add(time.Minute))
	s.browser.Unlock()
	if admitted.Node == nil || !remoteNodeContext(context.WithValue(t.Context(), nodeSessionKey{}, *admitted.Node)) {
		t.Fatal("captured scope downgraded after pruning")
	}
	if _, ok = s.authenticateAdmission(request, now.Add(time.Minute)); ok {
		t.Fatal("pruned scoped token became ordinary local authority")
	}
	if _, _, e = s.browser.issueNode(bound, "later", now.Add(time.Minute)); e == nil {
		t.Fatal("expired parent minted fresh child")
	}
}

func repairTargetGrant(t *testing.T, target *mesh.Store, source mesh.Identity, pair string, grant mesh.Grant) {
	t.Helper()
	if e := target.Revoke(source.Host, "fixture changes only destination grant"); e != nil {
		t.Fatal(e)
	}
	if _, e := target.PairReviewed(source, grant, pair, "fixture reviewed broader destination grant", true); e != nil {
		t.Fatal(e)
	}
	if e := target.Prove(source.Host, pair); e != nil {
		t.Fatal(e)
	}
	if e := target.Activate(source.Host, pair); e != nil {
		t.Fatal(e)
	}
}
func TestNodeWindowDestinationGuardRejectsBroadenBetweenScopeAndData(t *testing.T) {
	summary := mesh.Grant{Permissions: []string{"observe-host"}}
	own := mesh.Grant{Permissions: []string{"observe-host", "observe-own"}}
	left, right, l, r := registryPair(t, summary, own)
	bound, e := left.bindNodeContext(t.Context(), r.Config().Identity.Host)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = right.Manager.Acquire(model.Request{Host: bound.Host, Reason: "UNRELATED_SECRET_LEASE", Resources: model.Resources{CPU: 0.125}, TTL: time.Minute}, nil); e != nil {
		t.Fatal(e)
	}
	left.meshMu.Lock()
	service := left.mesh
	left.meshMu.Unlock()
	if _, e = scopedNodeCall(t.Context(), service, bound, "access_scope", json.RawMessage(`{}`)); e != nil {
		t.Fatal(e)
	}
	// Deterministically widen ONLY the target grant after the scope observation
	// and BEFORE its data request. TLS identity and source enrollment stay fixed.
	repairTargetGrant(t, r, l.Config().Identity, bound.Pair, mesh.Grant{Permissions: []string{"observe-host", "observe-all"}})
	if value, e := scopedNodeCall(t.Context(), service, bound, "status", json.RawMessage(`{}`)); e == nil || !strings.Contains(e.Error(), "node_context_changed") || len(value) != 0 {
		t.Fatal("stale scoped window received widened data", string(value), e)
	}
	// Ordinary current-grant callers retain their existing API behavior; this
	// proves the wider view really exists and would otherwise disclose the lease.
	value, e := l.Call(t.Context(), bound.Host, mesh.Request{Operation: "status", Params: json.RawMessage(`{}`)})
	if e != nil || !strings.Contains(string(value), "UNRELATED_SECRET_LEASE") {
		t.Fatal("fixture did not broaden", string(value), e)
	}
}
func TestNodeWindowIdleDestinationOnlyChangeClosesBrowser(t *testing.T) {
	for _, change := range []string{"revoked", "grant-revision"} {
		t.Run(change, func(t *testing.T) {
			left, _, l, r, bound := scopedRegistry(t)
			ticket, _, e := left.browser.issueNode(bound, strings.Repeat("c", 32), time.Now())
			if e != nil {
				t.Fatal(e)
			}
			token, _, _, ok := left.browser.exchangeNode(ticket, bound.Host, strings.Repeat("c", 32), time.Now())
			if !ok {
				t.Fatal("exchange failed")
			}
			h := httptest.NewServer(http.HandlerFunc(left.ws))
			defer h.Close()
			left.authority = strings.TrimPrefix(h.URL, "http://")
			ctx, stop := context.WithTimeout(t.Context(), 8*time.Second)
			defer stop()
			c, _, e := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{h.URL}}, Subprotocols: []string{"arm.v1", "arm.auth." + token}})
			if e != nil {
				t.Fatal(e)
			}
			defer c.CloseNow()
			if change == "revoked" {
				if e = r.Revoke(bound.Source, "fixture destination-only revocation"); e != nil {
					t.Fatal(e)
				}
			} else {
				repairTargetGrant(t, r, l.Config().Identity, bound.Pair, mesh.Grant{Permissions: []string{"observe-host", "observe-all"}})
			}
			if _, _, e = c.Read(ctx); e == nil || ctx.Err() != nil {
				t.Fatal("idle browser survived destination change", e)
			}
			if _, e = left.validateNodeSession(bound); e != nil {
				t.Fatal("fixture changed source trust; expected destination-only test", e)
			}
		})
	}
}

func TestNodeWindowChangedSourceEnrollmentClosesWithContextPolicy(t *testing.T) {
	left, _, source, _, bound := scopedRegistry(t)
	nonce := strings.Repeat("c", 32)
	ticket, _, err := left.browser.issueNode(bound, nonce, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, ok := left.browser.exchangeNode(ticket, bound.Host, nonce, time.Now())
	if !ok {
		t.Fatal("exchange failed")
	}
	if err = source.Revoke(bound.Host, "private policy-close regression"); err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(http.HandlerFunc(left.ws))
	defer h.Close()
	left.authority = strings.TrimPrefix(h.URL, "http://")
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	c, _, err := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{h.URL}}, Subprotocols: []string{"arm.v1", "arm.auth." + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(ctx)
	if err == nil || websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "node_context_changed") {
		t.Fatal("changed context did not carry explicit close policy", err)
	}
}
