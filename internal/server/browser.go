package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

//go:embed web/index.html web/app.mjs web/core.mjs web/json-view.mjs web/metric-help.mjs web/lease-actions.mjs web/route.mjs web/style.css
var browserAssets embed.FS

type browserContextKey struct{}
type browserCredentials struct {
	sync.Mutex
	tickets, sessions map[[32]byte]time.Time
}

const browserLifetime = 8 * time.Hour

func pruneCredentials(m map[[32]byte]time.Time, now time.Time) {
	for key, expiry := range m {
		if !now.Before(expiry) {
			delete(m, key)
		}
	}
}

func (b *browserCredentials) issue(authority string, now time.Time) (model.BrowserLink, error) {
	b.Lock()
	defer b.Unlock()
	if b.tickets == nil {
		b.tickets = make(map[[32]byte]time.Time)
	}
	pruneCredentials(b.tickets, now)
	if len(b.tickets) >= 32 {
		return model.BrowserLink{}, model.Err("browser_busy", "32 unused dashboard links exist; wait two minutes before requesting another")
	}
	ticket := local.ID() + local.ID()
	expiry := now.Add(2 * time.Minute)
	b.tickets[sha256.Sum256([]byte(ticket))] = expiry
	return model.BrowserLink{URL: "http://" + authority + "/#connect=" + ticket, ExpiresAt: expiry}, nil
}

func (b *browserCredentials) exchange(ticket string, now time.Time) (string, time.Time, bool) {
	b.Lock()
	defer b.Unlock()
	pruneCredentials(b.tickets, now)
	pruneCredentials(b.sessions, now)
	key := sha256.Sum256([]byte(ticket))
	if _, ok := b.tickets[key]; !ok || len(ticket) != 64 || len(b.sessions) >= 64 {
		return "", time.Time{}, false
	}
	delete(b.tickets, key)
	if b.sessions == nil {
		b.sessions = make(map[[32]byte]time.Time)
	}
	token := local.ID() + local.ID()
	expiry := now.Add(browserLifetime)
	b.sessions[sha256.Sum256([]byte(token))] = expiry
	return token, expiry, true
}

func (s *Server) validHost(host string) bool {
	name, _, err := net.SplitHostPort(host)
	ip := net.ParseIP(name)
	return err == nil && ip != nil && ip.IsLoopback() && (s.authority == "" || host == s.authority)
}

func (s *Server) authenticate(r *http.Request) (browser bool, expiry time.Time, ok bool) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") && s.token != "" &&
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.token)) == 1 {
		return false, time.Time{}, true
	}
	if r.Header.Get("Origin") != "http://"+r.Host {
		return false, time.Time{}, false
	}
	var token string
	version := false
	for _, protocol := range strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",") {
		protocol = strings.TrimSpace(protocol)
		if protocol == "arm.v1" {
			version = true
		} else if strings.HasPrefix(protocol, "arm.auth.") {
			if token != "" { // Ambiguous credentials are never accepted.
				return false, time.Time{}, false
			}
			token = strings.TrimPrefix(protocol, "arm.auth.")
		}
	}
	if !version || len(token) != 64 {
		return false, time.Time{}, false
	}
	s.browser.Lock()
	defer s.browser.Unlock()
	expiry, ok = s.browser.sessions[sha256.Sum256([]byte(token))]
	return true, expiry, ok && time.Now().Before(expiry)
}

func browserAllowedOperation(op string) bool {
	switch op {
	case "status", "resources", "monitor", "inspect", "events", "messages", "agents", "incidents", "usage", "pool_list", "pool_inspect", "pool_history", "storage_list":
		return true
	case "lease_action":
		return true // Restricted to revision-guarded renew/release/cancel below.
	}
	return false
}

func (s *Server) registerBrowser(mux *http.ServeMux) {
	mux.HandleFunc("/v1/browser/session", s.browserSession)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		s.browserHeaders(w)
		if !s.validHost(r.Host) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	for path, asset := range map[string]string{"/": "index.html", "/app.mjs": "app.mjs", "/core.mjs": "core.mjs", "/json-view.mjs": "json-view.mjs", "/metric-help.mjs": "metric-help.mjs", "/lease-actions.mjs": "lease-actions.mjs", "/route.mjs": "route.mjs", "/style.css": "style.css"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			s.browserHeaders(w)
			if !s.validHost(r.Host) {
				http.Error(w, "invalid host", http.StatusForbidden)
				return
			}
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			data, err := browserAssets.ReadFile("web/" + asset)
			if err != nil {
				http.Error(w, "asset unavailable", http.StatusInternalServerError)
				return
			}
			content := "text/html; charset=utf-8"
			if strings.HasSuffix(asset, ".mjs") {
				content = "text/javascript; charset=utf-8"
			} else if strings.HasSuffix(asset, ".css") {
				content = "text/css; charset=utf-8"
			}
			w.Header().Set("Content-Type", content)
			if r.Method != http.MethodHead {
				_, _ = w.Write(data)
			}
		})
	}
}

func (s *Server) browserHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self' ws://"+s.authority+"; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
}

func (s *Server) browserSession(w http.ResponseWriter, r *http.Request) {
	s.browserHeaders(w)
	if !s.validHost(r.Host) || r.Header.Get("Origin") != "http://"+r.Host {
		http.Error(w, "invalid origin or host", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input struct {
		Ticket string `json:"ticket"`
	}
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	token, expiry, ok := s.browser.exchange(input.Ticket, time.Now())
	if !ok {
		http.Error(w, "link expired, consumed or session limit reached; run arm dashboard again", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_at": expiry})
}
