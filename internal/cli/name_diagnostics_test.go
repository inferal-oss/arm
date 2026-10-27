package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestCustomNameCLIWireDiagnosticKeepsJSONAndMatchingResult(t *testing.T) {
	for _, mode := range []string{"success", "mismatched-id", "unsupported-version", "failed-operation"} {
		t.Run(mode, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer connection.CloseNow()
				var request model.WireRequest
				if wsjson.Read(r.Context(), connection, &request) != nil {
					return
				}
				response := model.WireResponse{Version: 1, ID: request.ID, Result: model.Lease{ID: nameLease, Host: nameHost}, NamingDiagnostic: &model.Error{Code: "name_metadata_invalid", Message: "private raw document secret"}}
				if mode == "mismatched-id" {
					response.ID = "different-request"
				}
				if mode == "unsupported-version" {
					response.Version = 99
				}
				if mode == "failed-operation" {
					response.Error = &model.Error{Code: "forbidden"}
					response.Result = nil
				}
				_ = wsjson.Write(r.Context(), connection, response)
			}))
			t.Cleanup(backend.Close)
			home := t.TempDir()
			if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(backend.URL, "http"), Token: "test-secret"}); err != nil {
				t.Fatal(err)
			}
			var out, diagnostic bytes.Buffer
			code := Main([]string{"--home", home, "release", nameLease}, strings.NewReader(""), &out, &diagnostic)
			if strings.Contains(out.String()+diagnostic.String(), "private raw") {
				t.Fatal("diagnostic echoed raw metadata", out.String(), diagnostic.String())
			}
			if mode == "success" {
				var lease model.Lease
				if err := json.Unmarshal(out.Bytes(), &lease); err != nil || lease.ID != nameLease || code != 0 || strings.Count(diagnostic.String(), "Warning:") != 1 || !strings.Contains(diagnostic.String(), "name_metadata_invalid") {
					t.Fatal("successful cleanup result or warning lost", code, out.String(), diagnostic.String(), err)
				}
				a := customNameApp(home)
				var result model.Lease
				if err := a.call("inspect", nil, &result); err != nil {
					t.Fatal(err)
				}
				if err := a.call("inspect", nil, &result); err != nil {
					t.Fatal(err)
				}
				if strings.Count(a.err.(*bytes.Buffer).String(), "Warning:") != 1 {
					t.Fatal("one invocation repeated cosmetic warnings", a.err)
				}
			} else if code == 0 || strings.Contains(diagnostic.String(), "Warning:") || out.Len() != 0 {
				t.Fatal("unmatched/failed diagnostic accepted", code, out.String(), diagnostic.String())
			}
		})
	}
}

func TestCustomNameCLIWireDiagnosticRefusesCustomSelector(t *testing.T) {
	// The result envelope may remain structurally valid while the matched wire
	// warning says the optional metadata cannot establish a custom selector.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var request model.WireRequest
		if wsjson.Read(r.Context(), connection, &request) != nil {
			return
		}
		response := model.WireResponse{Version: 1, ID: request.ID}
		if request.Operation == "status" {
			response.Result = model.Status{Host: nameHost, Leases: []model.Lease{{ID: nameLease}}}
		} else if request.Operation == "name_list" {
			response.Result = nameSnapshot{Document: nameDocument(nameEntry("lease", nameLease, "Build"))}
			response.NamingDiagnostic = &model.Error{Code: "name_metadata_invalid"}
		} else {
			t.Error("custom selector dispatched operation", request.Operation)
			response.Error = &model.Error{Code: "forbidden"}
		}
		_ = wsjson.Write(r.Context(), connection, response)
	}))
	t.Cleanup(backend.Close)
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(backend.URL, "http"), Token: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	a := customNameApp(home)
	a.ctx = context.Background()
	code, err := a.command([]string{"release", "Build"})
	if code == 0 || customNameCode(err) != "name_metadata_invalid" {
		t.Fatal("wire diagnostic did not fence custom selector", code, err)
	}
	out, diagnostic, completionCode := completionCLI(t, home, "__complete", "inspect", "")
	if completionCode != 0 || strings.Contains(out, "Build") || strings.Contains(diagnostic, "Warning:") {
		t.Fatal("completion accepted cosmetic wire diagnostic or corrupted shell protocol", completionCode, out, diagnostic)
	}
}
