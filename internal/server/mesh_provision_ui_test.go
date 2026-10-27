package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
)

func TestProvisionDashboardRoutesExposeOnlyClosedInstallationInput(t *testing.T) {
	s := meshFixture(t)
	t.Cleanup(s.closeMeshOperator)
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	key := meshops.ProvisionKey("ui-original")
	for _, field := range []string{"remote_binary", "remote_home", "install_root", "artifact", "startup", "ssh_identity", "shell", "seed_catalog", "allow_public_listen"} {
		raw := `{"key":"` + key + `","provision":{"connection_key":"ui-original","ssh_destination":"fixture","reason":"install fixture","` + field + `":null}}`
		if _, e := s.dispatch(ctx, model.WireRequest{Operation: "mesh_operator_provision_prepare", Params: json.RawMessage(raw)}); e == nil {
			t.Fatal("browser execution override accepted", field)
		}
	}
	for _, raw := range []string{`{"key":"` + key + `","provision":{"connection_key":"wrong-linked","ssh_destination":"fixture","reason":"install fixture"}}`, `{"key":"` + key + `","provision":{"connection_key":"ui-original","ssh_destination":"-F","reason":"install fixture"}}`, `{"key":"` + key + `","provision":{"connection_key":"ui-original","connector":"saved","ssh_destination":"fixture","reason":"install fixture"}}`} {
		if _, e := s.dispatch(ctx, model.WireRequest{Operation: "mesh_operator_provision_prepare", Params: json.RawMessage(raw)}); e == nil {
			t.Fatal("invalid closed draft accepted", raw)
		}
	}
	service, e := s.meshOperatorService()
	if e != nil {
		t.Fatal(e)
	}
	ops, e := service.List()
	if e != nil || len(ops) != 0 {
		t.Fatal("refused drafts allocated durable work", ops, e)
	}
	for _, name := range []string{"mesh_operator_provision_prepare", "mesh_operator_provision_lookup"} {
		if !browserAllowedOperation(name) {
			t.Fatal("closed API not available", name)
		}
	}
	for _, name := range []string{"mesh_provision_upload", "mesh_provision_start", "provision_start", "mesh_dispatch", "mesh_bootstrap"} {
		if browserAllowedOperation(name) {
			t.Fatal("raw native execution exposed", name)
		}
	}
}
func TestProvisionModuleUsesClosedExactAssetRoute(t *testing.T) {
	s := &Server{authority: "127.0.0.1:12345"}
	mux := http.NewServeMux()
	s.registerBrowser(mux)
	for _, c := range []struct {
		method, host, path string
		status             int
	}{{"GET", s.authority, "/mesh-provision.mjs", 200}, {"HEAD", s.authority, "/mesh-provision.mjs", 200}, {"POST", s.authority, "/mesh-provision.mjs", 405}, {"GET", "foreign.invalid", "/mesh-provision.mjs", 403}, {"GET", s.authority, "/mesh-provision.mjs/extra", 404}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(c.method, "http://"+c.host+c.path, nil))
		if w.Code != c.status {
			t.Fatal(c, w.Code)
		}
		if c.method == "GET" && c.status == 200 && !strings.Contains(w.Body.String(), "provision") {
			t.Fatal("missing module")
		}
	}
}
