package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMeshForwardSourceBrowserBoundary(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, tc := range []struct {
		method, path, host string
		code               int
	}{{"GET", "/mesh-forward-console.mjs", s.authority, 200}, {"HEAD", "/mesh-forward-console.mjs", s.authority, 200}, {"POST", "/mesh-forward-console.mjs", s.authority, 405}, {"GET", "/mesh-forward-console.mjs", "external.invalid", 403}, {"GET", "/mesh-forward-console.mjs/extra", s.authority, 404}} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.method == "GET" && tc.code == 200 && (!strings.Contains(w.Body.String(), "prepareForwardSource") || !strings.Contains(w.Header().Get("Content-Type"), "javascript")) {
			t.Fatal("embedded source asset missing", w.Header())
		}
	}
	for _, op := range []string{"mesh_forward_start", "mesh_forward_status", "mesh_forward_list", "mesh_forward_stop", "mesh_forward_recover"} {
		if !browserAllowedOperation(op) {
			t.Fatal("local source control unavailable", op)
		}
	}
	for _, op := range []string{"mesh_forward_grant", "mesh_forward_revoke", "mesh_forward_proxy", "mesh_forward_route", "mesh_dispatch"} {
		if browserAllowedOperation(op) {
			t.Fatal("destination grant or raw routed control exposed", op)
		}
	}
}
