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

//go:embed web/index.html web/app.mjs web/core.mjs web/json-view.mjs web/metric-help.mjs web/lease-actions.mjs web/lease-reviews.mjs web/lease-state.mjs web/io-devices.mjs web/io-selector.mjs web/route.mjs web/dashboard-shortcuts.mjs web/activity-feed.mjs web/operator-messaging.mjs web/pool-model.mjs web/pool-operations.mjs web/pool-console.mjs web/mesh-console.mjs web/mesh-model.mjs web/node-window.mjs web/remote-node.mjs web/connection-links.mjs web/style.css
var browserAssets embed.FS

type browserContextKey struct{}
type browserCredentials struct {
	sync.Mutex
	tickets, sessions map[[32]byte]time.Time
	nodeTickets       map[[32]byte]nodeTicket
	nodeSessions      map[[32]byte]nodeCredential
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
	b.pruneNodeSessions(now)
	key := sha256.Sum256([]byte(ticket))
	if _, ok := b.tickets[key]; !ok || len(ticket) != 64 || len(b.sessions)+len(b.nodeSessions) >= 64 {
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

type browserAdmission struct {
	Browser   bool
	Expires   time.Time
	Node      *nodeSession
	Principal [32]byte
}

func (s *Server) authenticate(r *http.Request) (bool, time.Time, bool) {
	a, ok := s.authenticateAdmission(r, time.Now())
	return a.Browser, a.Expires, ok
}
func (s *Server) authenticateAdmission(r *http.Request, now time.Time) (browserAdmission, bool) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") && s.token != "" && subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.token)) == 1 {
		return browserAdmission{}, true
	}
	if r.Header.Get("Origin") != "http://"+r.Host {
		return browserAdmission{}, false
	}
	token := browserToken(r)
	version := false
	for _, protocol := range strings.Split(strings.Join(r.Header.Values("Sec-WebSocket-Protocol"), ","), ",") {
		if strings.TrimSpace(protocol) == "arm.v1" {
			version = true
		}
	}
	if !version || len(token) != 64 {
		return browserAdmission{}, false
	}
	key := sha256.Sum256([]byte(token))
	s.browser.Lock()
	defer s.browser.Unlock()
	localExpiry, localOK := s.browser.sessions[key]
	node, nodeOK := s.browser.nodeSessions[key]
	// A scoped credential is NEVER inserted in the ordinary local map. Capture
	// kind, expiry and binding atomically; pruning cannot reclassify admission.
	if localOK && nodeOK {
		return browserAdmission{}, false
	}
	if nodeOK {
		if !now.Before(node.Expires) {
			return browserAdmission{}, false
		}
		bound := node.Context
		return browserAdmission{Browser: true, Expires: node.Expires, Node: &bound, Principal: key}, true
	}
	if !localOK || !now.Before(localExpiry) {
		return browserAdmission{}, false
	}
	return browserAdmission{Browser: true, Expires: localExpiry, Principal: key}, true
}

func browserAllowedOperation(op string) bool {
	switch op {
	case "lease_review_list", "lease_review_inspect", "lease_review_policy", "lease_review_configure":
		return true // Same local reviewed lease revision; replies require the exact agent polling workflow.
	case "mesh_operator_watch", "mesh_operator_stop_lease", "mesh_operator_stop_lookup", "mesh_operator_reconnect_context", "mesh_operator_prepare", "mesh_operator_get", "mesh_operator_list", "mesh_operator_decide", "mesh_operator_cancel", "mesh_operator_resume", "mesh_operator_handoff", "mesh_operator_connectors":
		return true
	case "node_open", "node_list", "node_inspect", "node_metadata", "status", "resources", "monitor", "inspect", "events", "messages", "agents", "incidents", "usage", "pool_list", "pool_inspect", "pool_children", "pool_consumers", "pool_history", "pool_history_page", "pool_lookup", "storage_list", "message_lookup", "message_send":
		return true
	case "pool_mutate":
		return true // Restricted to reviewed local pool operations in dispatch.
	case "lease_action":
		return true // Restricted to revision-guarded renew/release/cancel below.
	}
	return false
}

func (s *Server) registerBrowser(mux *http.ServeMux) {
	s.registerBrand(mux)
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
	for path, asset := range map[string]string{"/": "index.html", "/app.mjs": "app.mjs", "/core.mjs": "core.mjs", "/json-view.mjs": "json-view.mjs", "/metric-help.mjs": "metric-help.mjs", "/lease-actions.mjs": "lease-actions.mjs", "/lease-reviews.mjs": "lease-reviews.mjs", "/lease-state.mjs": "lease-state.mjs", "/io-devices.mjs": "io-devices.mjs", "/io-selector.mjs": "io-selector.mjs", "/route.mjs": "route.mjs", "/dashboard-shortcuts.mjs": "dashboard-shortcuts.mjs", "/activity-feed.mjs": "activity-feed.mjs", "/operator-messaging.mjs": "operator-messaging.mjs", "/connection-links.mjs": "connection-links.mjs", "/pool-model.mjs": "pool-model.mjs", "/pool-operations.mjs": "pool-operations.mjs", "/pool-console.mjs": "pool-console.mjs", "/mesh-console.mjs": "mesh-console.mjs", "/mesh-model.mjs": "mesh-model.mjs", "/node-window.mjs": "node-window.mjs", "/remote-node.mjs": "remote-node.mjs", "/style.css": "style.css"} {
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
		Host   string `json:"host,omitempty"`
		Nonce  string `json:"window_nonce,omitempty"`
	}
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var token string
	var expiry time.Time
	var bound *nodeSession
	var ok bool
	if input.Host != "" || input.Nonce != "" {
		token, expiry, bound, ok = s.browser.exchangeNode(input.Ticket, input.Host, input.Nonce, time.Now())
		if ok {
			if _, e := s.validateNodeSession(*bound); e != nil {
				s.browser.Lock()
				delete(s.browser.sessions, sha256.Sum256([]byte(token)))
				delete(s.browser.nodeSessions, sha256.Sum256([]byte(token)))
				s.browser.Unlock()
				ok = false
			}
		}
	} else {
		token, expiry, ok = s.browser.exchange(input.Ticket, time.Now())
	}
	if !ok {
		http.Error(w, "link expired, consumed or session limit reached; run arm dashboard again", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_at": expiry, "context": bound})
}
