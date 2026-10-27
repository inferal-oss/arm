package server

import (
	"context"
	"encoding/json"
	"getarm.org/internal/model"
	"testing"
)

func TestProvisionRuntimeNeverBrowserAllowed(t *testing.T) {
	if browserAllowedOperation("provision_runtime") {
		t.Fatal("runtime artifact identity exposed to browser")
	}
	s := &Server{}
	if _, e := s.provisionRuntime(json.RawMessage(`{"host":"other"}`)); e == nil {
		t.Fatal("native runtime read accepted target selector")
	}
	if _, e := s.dispatch(context.WithValue(context.Background(), browserContextKey{}, true), model.WireRequest{Operation: "provision_runtime", Params: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("browser dispatched native runtime proof")
	}
}
