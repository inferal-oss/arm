package server

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

//go:embed web/assets web/index.html web/novnc web/ard
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

// dashboardLoginLink is an explicitly requested local operator action. It
// issues a fresh short-lived ticket; the caller's session and primary bearer
// are never reflected in the link. Scoped node credentials are refused before
// local dispatch can reach this handler.
func (s *Server) dashboardLoginLink(raw json.RawMessage) (model.BrowserLink, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) {
		if trimmed[0] != '{' {
			return model.BrowserLink{}, model.Err("invalid_request", "dashboard login links accept only an empty parameter object")
		}
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		var request struct{}
		if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return model.BrowserLink{}, model.Err("invalid_request", "dashboard login links accept no selectors or other fields")
		}
	}
	return s.browser.issue(s.authority, time.Now())
}

func browserAllowedOperation(op string) bool {
	switch op {
	case "dashboard_login_link":
		return true // Local operator action; scoped node sessions are refused by dispatch.
	case "lease_review_list", "lease_review_inspect", "lease_review_policy", "lease_review_configure":
		return true // Same local reviewed lease revision; replies require the exact agent polling workflow.
	case "mesh_operator_provision_prepare", "mesh_operator_provision_lookup", "mesh_operator_uninstall_context", "mesh_operator_uninstall_lookup", "mesh_operator_uninstall_finish", "mesh_operator_watch", "mesh_operator_stop_lease", "mesh_operator_stop_lookup", "mesh_operator_reconnect_context", "mesh_operator_prepare", "mesh_operator_get", "mesh_operator_list", "mesh_operator_decide", "mesh_operator_cancel", "mesh_operator_resume", "mesh_operator_handoff", "mesh_operator_connectors":
		return true
	case "mesh_forward_start", "mesh_forward_status", "mesh_forward_list", "mesh_forward_stop", "mesh_forward_recover":
		return true // Local source controls; no forwarded-service browser access.
	case "kube_contexts", "kube_nodes", "peers_status", "node_open", "node_list", "node_inspect", "node_metadata", "status", "resources", "monitor", "inspect", "events", "messages", "agents", "incidents", "usage", "pool_list", "pool_inspect", "pool_children", "pool_consumers", "pool_history", "pool_history_page", "pool_lookup", "storage_list", "message_lookup", "message_send":
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
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
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
		name := strings.TrimPrefix(r.URL.Path, "/assets/")
		if !fs.ValidPath(name) || name == "." {
			http.NotFound(w, r)
			return
		}
		data, err := browserAssets.ReadFile("web/assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		content := "application/octet-stream"
		if strings.HasSuffix(name, ".mjs") || strings.HasSuffix(name, ".js") {
			content = "text/javascript; charset=utf-8"
		} else if strings.HasSuffix(name, ".wasm") {
			content = "application/wasm"
		} else if strings.HasSuffix(name, ".css") {
			content = "text/css; charset=utf-8"
		} else if strings.HasSuffix(name, ".txt") {
			content = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", content)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
	mux.HandleFunc("/v1/browser/session", s.browserSession)
	novnc, _ := fs.Sub(browserAssets, "web/novnc")
	novncFiles := http.StripPrefix("/novnc/", http.FileServer(http.FS(novnc)))
	mux.HandleFunc("/novnc/", func(w http.ResponseWriter, r *http.Request) {
		s.browserHeaders(w)
		if !s.validHost(r.Host) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		novncFiles.ServeHTTP(w, r)
	})
	ard, _ := fs.Sub(browserAssets, "web/ard")
	ardFiles := http.StripPrefix("/ard/", http.FileServer(http.FS(ard)))
	mux.HandleFunc("/ard/", func(w http.ResponseWriter, r *http.Request) {
		s.browserHeaders(w)
		if !s.validHost(r.Host) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ardFiles.ServeHTTP(w, r)
	})
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
	for path, asset := range map[string]string{"/": "index.html"} {
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; connect-src 'self' ws://"+s.authority+"; img-src 'self' data: blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
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
