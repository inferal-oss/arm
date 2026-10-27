package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/model"
)

func loginLinkServer(t *testing.T, s *Server) (*httptest.Server, context.Context) {
	t.Helper()
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	mux.HandleFunc("/v1/ws", s.ws)
	h := httptest.NewServer(mux)
	t.Cleanup(h.Close)
	s.authority = strings.TrimPrefix(h.URL, "http://")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return h, ctx
}

func loginLinkDial(t *testing.T, ctx context.Context, base, token string) *websocket.Conn {
	t.Helper()
	c, response, err := websocket.Dial(ctx, strings.Replace(base, "http://", "ws://", 1)+"/v1/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{base}}, Subprotocols: []string{"arm.v1", "arm.auth." + token},
	})
	if err != nil {
		t.Fatal("authenticated browser connection failed", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	if c.Subprotocol() != "arm.v1" || strings.Contains(response.Header.Get("Sec-WebSocket-Protocol"), token) {
		t.Fatal("browser credential was reflected in the negotiated protocol")
	}
	return c
}

func loginLinkCall(t *testing.T, ctx context.Context, c *websocket.Conn, operation string, params json.RawMessage) model.WireResponse {
	t.Helper()
	if err := wsjson.Write(ctx, c, model.WireRequest{Version: 1, ID: operation, Operation: operation, Params: params}); err != nil {
		t.Fatal("write dashboard operation", err)
	}
	var response model.WireResponse
	if err := wsjson.Read(ctx, c, &response); err != nil {
		t.Fatal("read dashboard operation", err)
	}
	if response.ID != operation || response.Version != 1 {
		t.Fatal("response did not identify the requested operation")
	}
	return response
}

func loginLinkResult(t *testing.T, response model.WireResponse) model.BrowserLink {
	t.Helper()
	if response.Error != nil {
		t.Fatal("dashboard login link failed", response.Error)
	}
	raw, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result model.BrowserLink
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func loginLinkExchange(t *testing.T, ctx context.Context, base, ticket string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/browser/session", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", base)
	r.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal("exchange dashboard link", err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Set-Cookie") != "" {
		t.Fatal("dashboard exchange unexpectedly cached credentials or set a cookie")
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode, ""
	}
	var result struct {
		Token string `json:"token"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil || len(result.Token) != 64 {
		t.Fatal("exchange returned no valid session")
	}
	return response.StatusCode, result.Token
}

func loginLinkTicket(t *testing.T, link model.BrowserLink, base string) string {
	t.Helper()
	parsed, err := url.Parse(link.URL)
	if err != nil || parsed.Scheme+"://"+parsed.Host != base || parsed.Path != "/" || parsed.RawQuery != "" || !strings.HasPrefix(parsed.Fragment, "connect=") {
		t.Fatal("login link did not use the bound local authority and a fragment-only ticket")
	}
	ticket := strings.TrimPrefix(parsed.Fragment, "connect=")
	if len(ticket) != 64 {
		t.Fatal("invalid dashboard ticket length")
	}
	return ticket
}

func TestDashboardLoginLinkAuthenticatedRoundTrip(t *testing.T) {
	s := &Server{token: strings.Repeat("f", 64)}
	h, ctx := loginLinkServer(t, s)
	// Bootstrap one ordinary local browser session through the existing ticket exchange.
	initial, err := s.browser.issue(s.authority, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, original := loginLinkExchange(t, ctx, h.URL, loginLinkTicket(t, initial, h.URL))
	if code != http.StatusOK {
		t.Fatal("bootstrap link exchange failed")
	}
	browser := loginLinkDial(t, ctx, h.URL, original)
	before := time.Now()
	link := loginLinkResult(t, loginLinkCall(t, ctx, browser, "dashboard_login_link", json.RawMessage(`{}`)))
	after := time.Now()
	if link.ExpiresAt.Before(before.Add(2*time.Minute)) || link.ExpiresAt.After(after.Add(2*time.Minute)) {
		t.Fatal("new link did not retain the existing two-minute lifetime")
	}
	if strings.Contains(link.URL, original) || strings.Contains(link.URL, s.token) {
		t.Fatal("new link reflected an existing browser session or primary bearer")
	}
	ticket := loginLinkTicket(t, link, h.URL)
	code, fresh := loginLinkExchange(t, ctx, h.URL, ticket)
	if code != http.StatusOK || fresh == original || fresh == s.token {
		t.Fatal("fresh login link did not establish an independent ordinary session")
	}
	if code, _ = loginLinkExchange(t, ctx, h.URL, ticket); code != http.StatusUnauthorized {
		t.Fatal("new login ticket was reusable")
	}
	freshBrowser := loginLinkDial(t, ctx, h.URL, fresh)
	again := loginLinkResult(t, loginLinkCall(t, ctx, freshBrowser, "dashboard_login_link", json.RawMessage(`null`)))
	if again.URL == link.URL {
		t.Fatal("new session did not issue a fresh independently bounded login link")
	}
	if denied := loginLinkCall(t, ctx, freshBrowser, "browser_ticket", json.RawMessage(`{}`)); denied.Error == nil || denied.Error.Code != "forbidden" {
		t.Fatal("native browser_ticket became browser-authorized")
	}
	// Expiry is exercised through the actual HTTP exchange without a two-minute sleep.
	expiring := loginLinkTicket(t, again, h.URL)
	s.browser.Lock()
	s.browser.tickets[sha256.Sum256([]byte(expiring))] = time.Now().Add(-time.Second)
	s.browser.Unlock()
	if code, _ = loginLinkExchange(t, ctx, h.URL, expiring); code != http.StatusUnauthorized {
		t.Fatal("expired login ticket exchanged successfully")
	}
}

func TestDashboardLoginLinkAuthenticationAndClosedParameters(t *testing.T) {
	s := &Server{token: strings.Repeat("f", 64)}
	h, ctx := loginLinkServer(t, s)
	initial, _ := s.browser.issue(s.authority, time.Now())
	_, token := loginLinkExchange(t, ctx, h.URL, loginLinkTicket(t, initial, h.URL))
	for _, options := range []*websocket.DialOptions{
		nil,
		{HTTPHeader: http.Header{"Origin": []string{h.URL}}, Subprotocols: []string{"arm.v1", "arm.auth." + strings.Repeat("0", 64)}},
		{HTTPHeader: http.Header{"Origin": []string{"http://unrelated.invalid"}}, Subprotocols: []string{"arm.v1", "arm.auth." + token}},
		{HTTPHeader: http.Header{"Authorization": []string{"Bearer wrong"}}},
	} {
		c, response, err := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1)+"/v1/ws", options)
		if c != nil {
			_ = c.CloseNow()
		}
		if err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
			t.Fatal("unauthenticated or wrong-origin login-link transport was admitted")
		}
	}
	browser := loginLinkDial(t, ctx, h.URL, token)
	for _, params := range []json.RawMessage{json.RawMessage(`{"host":"other"}`), json.RawMessage(`{"token":"replacement"}`), json.RawMessage(`[]`)} {
		response := loginLinkCall(t, ctx, browser, "dashboard_login_link", params)
		if response.Error == nil || response.Error.Code != "invalid_request" {
			t.Fatal("login link accepted selector or credential parameters")
		}
	}
	s.browser.Lock()
	count := len(s.browser.tickets)
	s.browser.Unlock()
	if count != 0 {
		t.Fatal("rejected authentication or parameters issued an unused ticket")
	}
	// The native bearer remains a supported caller of the explicit operation.
	native, _, err := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1)+"/v1/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + s.token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer native.CloseNow()
	_ = loginLinkTicket(t, loginLinkResult(t, loginLinkCall(t, ctx, native, "dashboard_login_link", nil)), h.URL)
}

func TestDashboardLoginLinkPreservesCredentialCaps(t *testing.T) {
	s := &Server{}
	h, ctx := loginLinkServer(t, s)
	initial, _ := s.browser.issue(s.authority, time.Now())
	_, token := loginLinkExchange(t, ctx, h.URL, loginLinkTicket(t, initial, h.URL))
	browser := loginLinkDial(t, ctx, h.URL, token)
	s.browser.Lock()
	for i := range 32 {
		s.browser.tickets[sha256.Sum256([]byte(fmt.Sprintf("unused-ticket-%d", i)))] = time.Now().Add(time.Minute)
	}
	s.browser.Unlock()
	if response := loginLinkCall(t, ctx, browser, "dashboard_login_link", nil); response.Error == nil || response.Error.Code != "browser_busy" {
		t.Fatal("new operation bypassed the 32 unused-ticket cap")
	}
	s.browser.Lock()
	clear(s.browser.tickets)
	for i := 0; len(s.browser.sessions) < 64; i++ {
		s.browser.sessions[sha256.Sum256([]byte(fmt.Sprintf("existing-session-%d", i)))] = time.Now().Add(time.Hour)
	}
	s.browser.Unlock()
	link := loginLinkResult(t, loginLinkCall(t, ctx, browser, "dashboard_login_link", nil))
	ticket := loginLinkTicket(t, link, h.URL)
	if code, _ := loginLinkExchange(t, ctx, h.URL, ticket); code != http.StatusUnauthorized {
		t.Fatal("new operation bypassed the 64 total-session cap")
	}
	s.browser.Lock()
	_, retained := s.browser.tickets[sha256.Sum256([]byte(ticket))]
	delete(s.browser.sessions, sha256.Sum256([]byte("existing-session-0")))
	s.browser.Unlock()
	if !retained {
		t.Fatal("session exhaustion consumed the unexchanged ticket")
	}
	if code, _ := loginLinkExchange(t, ctx, h.URL, ticket); code != http.StatusOK {
		t.Fatal("retained ticket failed after one session slot became available")
	}
}

func TestDashboardLoginLinkScopedSessionsCannotLiftAuthority(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint("local=", local), func(t *testing.T) {
			var s *Server
			var bound nodeSession
			if local {
				s = meshFixture(t)
				status, err := s.Manager.Status()
				if err != nil {
					t.Fatal(err)
				}
				bound = nodeSession{Host: status.Host, Source: status.Host, Local: true}
			} else {
				s, _, _, _, bound = scopedRegistry(t)
			}
			h, ctx := loginLinkServer(t, s)
			nonce := strings.Repeat("c", 32)
			ticket, _, err := s.browser.issueNode(bound, nonce, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			token, _, _, ok := s.browser.exchangeNode(ticket, bound.Host, nonce, time.Now())
			if !ok {
				t.Fatal("scoped fixture exchange failed")
			}
			browser := loginLinkDial(t, ctx, h.URL, token)
			response := loginLinkCall(t, ctx, browser, "dashboard_login_link", nil)
			if response.Error == nil || response.Error.Code != "forbidden" {
				t.Fatal("scoped browser credential was lifted into ordinary local authority")
			}
			s.browser.Lock()
			issued := len(s.browser.tickets)
			s.browser.Unlock()
			if issued != 0 {
				t.Fatal("denied scoped call issued an ordinary login ticket")
			}
		})
	}
}
