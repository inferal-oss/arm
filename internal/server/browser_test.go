package server

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
	"github.com/coder/websocket"
)

func TestBrowserTicketConcurrentExchangeHasOneWinner(t *testing.T) {
	var b browserCredentials
	now := time.Now()
	link, _ := b.issue("127.0.0.1:1234", now)
	ticket := strings.Split(link.URL, "#connect=")[1]
	results := make(chan bool, 32)
	for range 32 {
		go func() { _, _, ok := b.exchange(ticket, now); results <- ok }()
	}
	winners := 0
	for range 32 {
		if <-results {
			winners++
		}
	}
	if winners != 1 || len(b.sessions) != 1 {
		t.Fatalf("ticket had %d successful exchanges", winners)
	}
}

func TestBrowserSessionExpiryClosesExistingWebSocket(t *testing.T) {
	s := &Server{}
	token := local.ID() + local.ID()
	s.browser.sessions = map[[32]byte]time.Time{sha256.Sum256([]byte(token)): time.Now().Add(250 * time.Millisecond)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.ws)
	h := httptest.NewServer(mux)
	defer h.Close()
	s.authority = strings.TrimPrefix(h.URL, "http://")
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	c, _, err := websocket.Dial(ctx, strings.Replace(h.URL, "http://", "ws://", 1)+"/v1/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{h.URL}}, Subprotocols: []string{"arm.v1", "arm.auth." + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_, _, err = c.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("live session outlasted expiry: %v", err)
	}
}

func TestBrowserCredentialsAreOneUseBoundedAndExpiring(t *testing.T) {
	var b browserCredentials
	now := time.Now()
	link, err := b.issue("127.0.0.1:1234", now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link.URL)
	if err != nil || u.RawQuery != "" || !strings.HasPrefix(u.Fragment, "connect=") {
		t.Fatalf("bad link: %v %s", err, link.URL)
	}
	ticket := strings.TrimPrefix(u.Fragment, "connect=")
	token, expiry, ok := b.exchange(ticket, now.Add(time.Second))
	if !ok || len(token) != 64 || expiry.Sub(now.Add(time.Second)) != browserLifetime {
		t.Fatal("valid ticket rejected")
	}
	if _, _, ok := b.exchange(ticket, now); ok {
		t.Fatal("replayed ticket")
	}
	for range 32 {
		if _, err := b.issue("127.0.0.1:1234", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.issue("127.0.0.1:1234", now); err == nil {
		t.Fatal("unbounded tickets")
	}
	if _, err := b.issue("127.0.0.1:1234", now.Add(2*time.Minute)); err != nil {
		t.Fatal("expired tickets not pruned")
	}
	link, _ = b.issue("127.0.0.1:1234", now.Add(2*time.Minute))
	ticket = strings.TrimPrefix(strings.Split(link.URL, "#")[1], "connect=")
	if _, _, ok := b.exchange(ticket, now.Add(4*time.Minute)); ok {
		t.Fatal("expired ticket accepted")
	}
	for len(b.sessions) < 64 {
		link, err = b.issue("127.0.0.1:1234", now)
		if err != nil {
			t.Fatal(err)
		}
		_, _, ok = b.exchange(strings.Split(link.URL, "#connect=")[1], now)
		if !ok {
			t.Fatal("valid exchange rejected")
		}
	}
	link, _ = b.issue("127.0.0.1:1234", now)
	ticket = strings.Split(link.URL, "#connect=")[1]
	if _, _, ok := b.exchange(ticket, now); ok {
		t.Fatal("unbounded sessions")
	}
	if len(b.tickets) != 1 {
		t.Fatal("session exhaustion consumed ticket")
	}
	if _, err := b.issue("127.0.0.1:1234", now.Add(browserLifetime)); err != nil {
		t.Fatal(err)
	}
	link, _ = b.issue("127.0.0.1:1234", now.Add(browserLifetime))
	if _, _, ok := b.exchange(strings.Split(link.URL, "#connect=")[1], now.Add(browserLifetime)); !ok {
		t.Fatal("expired sessions not pruned")
	}
}

func TestBrowserAuthenticationAndHostScope(t *testing.T) {
	s := &Server{authority: "127.0.0.1:1234", token: "primary-secret"}
	link, _ := s.browser.issue(s.authority, time.Now())
	token, _, _ := s.browser.exchange(strings.Split(link.URL, "#connect=")[1], time.Now())
	r := httptest.NewRequest("GET", "http://127.0.0.1:1234/v1/ws", nil)
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	r.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token)
	if browser, _, ok := s.authenticate(r); !ok || !browser {
		t.Fatal("valid browser credential rejected")
	}
	for _, origin := range []string{"", "null", "http://127.0.0.1:5678", "http://localhost:1234", "https://external.invalid"} {
		r.Header.Set("Origin", origin)
		if _, _, ok := s.authenticate(r); ok {
			t.Fatalf("accepted origin %q", origin)
		}
	}
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	r.Header.Set("Sec-WebSocket-Protocol", "arm.v1, arm.auth."+token+", arm.auth."+token)
	if _, _, ok := s.authenticate(r); ok {
		t.Fatal("ambiguous browser credential accepted")
	}
	r.Header.Set("Sec-WebSocket-Protocol", "arm.auth."+token)
	if _, _, ok := s.authenticate(r); ok {
		t.Fatal("missing protocol accepted")
	}
	r.Header.Set("Authorization", "Bearer primary-secret")
	if browser, _, ok := s.authenticate(r); !ok || browser {
		t.Fatal("native credential rejected")
	}
	for _, host := range []string{"localhost:1234", "127.0.0.1:9999", "127.0.0.2:1234", "evil.invalid:1234", "[::1]:1234"} {
		if s.validHost(host) {
			t.Fatalf("accepted wrong authority %q", host)
		}
	}
	if !s.validHost(s.authority) {
		t.Fatal("actual authority rejected")
	}
}
