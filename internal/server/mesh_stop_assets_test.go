package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshStopEmbeddedReviewAndExactLookupRoutes(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, tc := range []struct {
		method, path, host string
		code               int
	}{{"GET", "/mesh-stop.mjs", s.authority, 200}, {"HEAD", "/mesh-stop.mjs", s.authority, 200}, {"POST", "/mesh-stop.mjs", s.authority, 405}, {"GET", "/mesh-stop.mjs", "external.invalid", 403}, {"GET", "/mesh-stop.mjs/extra", s.authority, 404}} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.method == "GET" && tc.code == 200 && (!strings.Contains(w.Body.String(), "stopReviewView") || !strings.Contains(w.Header().Get("Content-Type"), "javascript")) {
			t.Fatal("embedded review asset missing", w.Header())
		}
	}
	if !browserAllowedOperation("mesh_operator_stop_lookup") {
		t.Fatal("explicit coordinator lookup unavailable")
	}
	for _, raw := range []string{"node_stop_apply", "mesh_node_stop_apply", "node_stop_restart", "mesh_setup_apply"} {
		if browserAllowedOperation(raw) {
			t.Fatal("raw destination administration exposed to browser", raw)
		}
	}
}
