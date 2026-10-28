package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func TestRunsBrowserReadOnlyBoundary(t *testing.T) {
	m, err := manager.Open(filepath.Join(t.TempDir(), "arm"), model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := &Server{Manager: m}
	q := model.RunCreateRequest{Key: "browser", Command: []string{"fixture"}, CWD: t.TempDir(), Reason: "browser read fixture"}
	r, err := m.CreateRun(q)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), browserContextKey{}, true)
	for _, op := range []string{"runs", "run_inspect", "run_output"} {
		if !browserAllowedOperation(op) {
			t.Fatal("missing read", op)
		}
		raw, _ := json.Marshal(map[string]any{"id": r.ID})
		if _, err = s.dispatch(ctx, model.WireRequest{Operation: op, Params: raw}); err != nil {
			t.Fatal(op, err)
		}
	}
	for _, op := range []string{"run_create", "run_append", "run_finish"} {
		if browserAllowedOperation(op) {
			t.Fatal("browser mutation", op)
		}
		if _, err = s.dispatch(ctx, model.WireRequest{Operation: op, Params: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("browser mutation reached manager", op)
		}
	}
}
