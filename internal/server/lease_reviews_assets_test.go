package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLeaseReviewEmbeddedOperatorModuleHasClosedAssetRoutes(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, tc := range []struct {
		method, path, host string
		code               int
	}{{"GET", "/assets/arm.mjs", s.authority, 200}, {"HEAD", "/assets/arm.mjs", s.authority, 200}, {"POST", "/assets/arm.mjs", s.authority, 405}, {"GET", "/assets/arm.mjs", "external.invalid", 403}, {"GET", "/assets/arm.mjs/extra", s.authority, 404}} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.method == "GET" && tc.code == 200 {
			if !strings.Contains(w.Body.String(), "lease_review_configure") || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
				t.Fatal("embedded module not served", w.Header())
			}
		}
	}
	if !browserAllowedOperation("lease_review_policy") || !browserAllowedOperation("lease_review_configure") || browserAllowedOperation("lease_review_reply") {
		t.Fatal("operator UI acquired exact-session reply authority")
	}
}
