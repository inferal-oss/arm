package server

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScreenUpgradeRequiresExactBrowserAuthority(t *testing.T) {
	const authority = "127.0.0.1:1234"
	token := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := &Server{authority: authority, token: "native-secret"}
	s.browser.sessions = map[[32]byte]time.Time{sha256.Sum256([]byte(token)): time.Now().Add(time.Minute)}
	request := func(method, address, origin, protocol, bearer string) int {
		r := httptest.NewRequest(method, address, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if protocol != "" {
			r.Header.Set("Sec-WebSocket-Protocol", protocol)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.browserScreen(w, r)
		return w.Code
	}
	validOrigin := "http://" + authority
	validProtocol := "arm.v1, arm.auth." + token
	for _, tc := range []struct {
		name, method, address, origin, protocol, bearer string
		want                                            int
	}{
		{"wrong method", "POST", validOrigin + "/v1/screen", validOrigin, validProtocol, "", http.StatusForbidden},
		{"query", "GET", validOrigin + "/v1/screen?port=5901", validOrigin, validProtocol, "", http.StatusForbidden},
		{"wrong host", "GET", "http://127.0.0.1:9876/v1/screen", "http://127.0.0.1:9876", validProtocol, "", http.StatusForbidden},
		{"wrong origin", "GET", validOrigin + "/v1/screen", "http://127.0.0.1:9876", validProtocol, "", http.StatusUnauthorized},
		{"missing protocol", "GET", validOrigin + "/v1/screen", validOrigin, "arm.auth." + token, "", http.StatusUnauthorized},
		{"native bearer", "GET", validOrigin + "/v1/screen", "", "", "native-secret", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(tc.method, tc.address, tc.origin, tc.protocol, tc.bearer); got != tc.want {
				t.Fatalf("status %d, want %d", got, tc.want)
			}
		})
	}
}

func TestScreenAssetsPreserveComposedDashboardModules(t *testing.T) {
	s := &Server{authority: "127.0.0.1:1234"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, path := range []string{"/assets/arm.mjs", "/novnc/core/rfb.js", "/novnc/deps/pako/LICENSE", "/novnc/deps/pako/lib/zlib/inflate.js", "/novnc/deps/pako/lib/zlib/deflate.js", "/ard/ard-patch.js", "/ard/LICENSE.txt"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+s.authority+path, nil))
			if w.Code != http.StatusOK || w.Body.Len() == 0 || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
				t.Fatalf("composed browser asset unavailable or missing origin policy: %s HTTP%d", path, w.Code)
			}
			for _, method := range []string{http.MethodPost, http.MethodGet} {
				host := s.authority
				if method == http.MethodGet {
					host = "127.0.0.1:4321"
				}
				refused := httptest.NewRecorder()
				mux.ServeHTTP(refused, httptest.NewRequest(method, "http://"+host+path, nil))
				if refused.Code != http.StatusForbidden && refused.Code != http.StatusMethodNotAllowed {
					t.Fatalf("asset accepted foreign Host or mutation: %s %s HTTP%d", method, path, refused.Code)
				}
			}
		})
	}
}
