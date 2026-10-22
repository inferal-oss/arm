package server

import (
	"embed"
	"net/http"
)

//go:embed web/brand-lockup.svg web/favicon.svg
var brandAssets embed.FS

func (s *Server) registerBrand(mux *http.ServeMux) {
	for path, asset := range map[string]string{"/brand-lockup.svg": "brand-lockup.svg", "/favicon.svg": "favicon.svg"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
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
			data, err := brandAssets.ReadFile("web/" + asset)
			if err != nil {
				http.Error(w, "asset unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "image/svg+xml")
			if r.Method != http.MethodHead {
				_, _ = w.Write(data)
			}
		})
	}
}
