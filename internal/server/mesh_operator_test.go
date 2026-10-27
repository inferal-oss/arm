package server

import (
	"context"
	"encoding/json"
	"getarm.org/internal/model"
	"testing"
)

func TestBrowserMeshWorkflowHasClosedTypedExecutionInputs(t *testing.T) {
	s := meshFixture(t)
	t.Cleanup(s.closeMeshOperator)
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	for _, raw := range []string{
		`{"key":"bad","kind":"connect","connect":{"ssh_destination":"fixture","remote_binary":"/bad/command","reason":"test"}}`,
		`{"key":"bad","kind":"reconnect","reconnect":{"host":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","remote_binary":"/bad/command","reason":"test"}}`,
		`{"key":"bad","kind":"reconnect","reconnect":{"host":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","remote_home":"/private/other","reason":"test"}}`,
		`{"key":"bad","kind":"reconnect","reconnect":{"host":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","binding":{"selected_helper":{"remote_binary":"/bad/command"}},"reason":"test"}}`,
		`{"key":"bad","kind":"connect","connect":{"ssh_destination":"fixture","remote_home":"/private/other","reason":"test"}}`,
		`{"key":"bad","kind":"connect","connect":{"connector":"unknown","ssh_destination":"fixture","reason":"test"}}`,
		`{"key":"bad","kind":"connect","connect":{"connector":"unknown","reason":"test"}}`,
	} {
		if _, e := s.dispatch(ctx, model.WireRequest{Operation: "mesh_operator_prepare", Params: json.RawMessage(raw)}); e == nil {
			t.Fatal("browser arbitrary execution input accepted", raw)
		}
	}
	for _, op := range []string{"mesh_init", "mesh_pair", "mesh_setup_prepare", "mesh_dispatch", "mesh_renew", "mesh_recovery_identity"} {
		if browserAllowedOperation(op) {
			t.Fatal("raw privileged mesh exposed to browser", op)
		}
	}
	service, e := s.meshOperatorService()
	if e != nil {
		t.Fatal(e)
	}
	all, e := service.List()
	if e != nil || len(all) != 0 {
		t.Fatal("invalid draft wrote intent", all, e)
	}
}
