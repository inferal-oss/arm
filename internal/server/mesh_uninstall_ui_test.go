package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUninstallEmbeddedAssetAndNarrowOperatorRoutes(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, c := range []struct {
		method, host, path string
		want               int
	}{{"GET", s.authority, "/assets/arm.mjs", 200}, {"HEAD", s.authority, "/assets/arm.mjs", 200}, {"POST", s.authority, "/assets/arm.mjs", 405}, {"GET", "foreign.invalid", "/assets/arm.mjs", 403}, {"GET", s.authority, "/assets/arm.mjs/extra", 404}} {
		r := httptest.NewRequest(c.method, "http://"+c.host+c.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Fatal(c, w.Code)
		}
		if c.method == "GET" && c.want == 200 && (!strings.Contains(w.Body.String(), "mesh_operator_uninstall_finish") || !strings.Contains(w.Header().Get("Content-Type"), "javascript")) {
			t.Fatal("missing embedded uninstall module")
		}
	}
	for _, name := range []string{"mesh_operator_uninstall_context", "mesh_operator_uninstall_lookup", "mesh_operator_uninstall_finish"} {
		if !browserAllowedOperation(name) {
			t.Fatal("narrow route absent", name)
		}
	}
	for _, name := range []string{"node_uninstall_apply", "node_uninstall_plan", "node_uninstall_finish", "mesh_dispatch", "mesh_bootstrap"} {
		if browserAllowedOperation(name) {
			t.Fatal("raw native admin exposed", name)
		}
	}
}
