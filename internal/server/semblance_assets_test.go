package server

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSemblanceEmbeddedAssets(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	var files []string
	if err := fs.WalkDir(browserAssets, "web/assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, strings.TrimPrefix(path, "web/assets/"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) < 4 {
		t.Fatal("compiled UI and engine assets are missing")
	}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				r := httptest.NewRequest(method, "http://"+s.authority+"/assets/"+path, nil)
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatalf("%s %s: %d %v", method, path, w.Code, w.Header())
				}
				csp := w.Header().Get("Content-Security-Policy")
				if !strings.Contains(csp, "script-src 'self' 'wasm-unsafe-eval'") || !strings.Contains(csp, "style-src 'self';") || strings.Contains(csp, "'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") {
					t.Fatal("unexpected CSP", csp)
				}
				if strings.HasSuffix(path, ".mjs") && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
					t.Fatal("JavaScript MIME missing")
				}
				if strings.HasSuffix(path, ".wasm") && w.Header().Get("Content-Type") != "application/wasm" {
					t.Fatal("WASM MIME missing")
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatal("HEAD returned an asset body")
				}
				if method == http.MethodGet {
					original, _ := browserAssets.ReadFile("web/assets/" + path)
					if !bytes.Equal(original, w.Body.Bytes()) {
						t.Fatal("embedded asset bytes changed")
					}
				}
			}
		})
	}
	for _, path := range []string{"/assets/", "/assets/assets/", "/assets/missing.mjs", "/app.mjs", "/style.css", "/semblance/main.mjs"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "http://"+s.authority+path, nil))
		if w.Code != 404 {
			t.Fatalf("legacy path or directory %s returned %d", path, w.Code)
		}
	}
	for _, tc := range []struct {
		method, host string
		status       int
	}{{"POST", s.authority, 405}, {"GET", "evil.example", 403}} {
		r := httptest.NewRequest(tc.method, "http://"+s.authority+"/assets/arm.mjs", nil)
		r.Host = tc.host
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s/%s: %d", tc.method, tc.host, w.Code)
		}
	}
	notices, err := browserAssets.ReadFile("web/assets/LICENSES.txt")
	if err != nil || !bytes.Contains(notices, []byte("Semblance")) || !bytes.Contains(notices, []byte("Apache License")) {
		t.Fatal("embedded license notices missing")
	}
}
