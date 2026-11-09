package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func TestKubernetesArtifactInventoryIsReadOnlyAndClosed(t *testing.T) {
	s := meshFixture(t)
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	query := func(raw string) (any, error) {
		return s.dispatch(ctx, model.WireRequest{Operation: "mesh_operator_kubernetes_artifacts", Params: json.RawMessage(raw)})
	}
	for _, raw := range []string{`null`, `[]`, `{} {}`, `{ "x":1, "x":2 }`, `{"artifact_path":"private-executable"}`, `{"approve":true}`, strings.Repeat(" ", 1025)} {
		if _, err := query(raw); err == nil {
			t.Fatal("artifact inventory accepted a selector or effect", raw)
		}
	}
	value, err := query(`{}`)
	rows, ok := value.([]map[string]any)
	if err != nil || !ok || len(rows) != 0 {
		t.Fatal(value, err)
	}
	dir := filepath.Join(s.home, "mesh-artifacts")
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("empty inventory created a catalog", err)
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i, platform := range []string{"linux", "darwin"} {
		sha := strings.Repeat(string(rune('a'+i)), 64)
		artifact := mesh.Artifact{Schema: 1, SHA256: sha, Size: 1, OS: platform, Arch: "arm64", Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "private fixture reason must not cross inventory"}
		raw, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "artifact-"+sha+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	value, err = query(`{}`)
	rows, ok = value.([]map[string]any)
	if err != nil || !ok || len(rows) != 1 || rows[0]["os"] != "linux" || rows[0]["sha256"] != strings.Repeat("a", 64) {
		t.Fatal(value, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private fixture") || strings.Contains(string(raw), "actor") || strings.Contains(string(raw), "path") {
		t.Fatal("artifact inventory exposed private native details", string(raw))
	}
	if s.meshOperator != nil {
		t.Fatal("read-only catalog started an operator worker")
	}
	status, err := s.Manager.Status()
	if err != nil || status.Reserved.CPU != 0 || status.Reserved.Memory != 0 || status.Reserved.Disk != 0 {
		t.Fatal("inventory acquired resources", status.Reserved, err)
	}
}

func TestKubernetesWorkflowEffectsRemainBrowserFenced(t *testing.T) {
	s := meshFixture(t)
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	for _, operation := range []string{"mesh_operator_kubernetes_prepare", "mesh_operator_kubernetes_lookup", "mesh_operator_kubernetes_decide", "kube_apply", "kube_ship", "kube_start"} {
		if browserAllowedOperation(operation) {
			t.Fatalf("unsupported workflow exposed: %s", operation)
		}
		if _, err := s.dispatch(ctx, model.WireRequest{Operation: operation, Params: json.RawMessage(`{}`)}); err == nil {
			t.Fatalf("unsupported workflow reached dispatch: %s", operation)
		}
	}
	if s.meshOperator != nil {
		t.Fatal("fenced operation started an operator worker")
	}
}

func TestKubernetesArtifactInventoryRedactsNativeCatalogFailure(t *testing.T) {
	s := meshFixture(t)
	dir := filepath.Join(s.home, "mesh-artifacts")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact-"+strings.Repeat("a", 64)+".json"), []byte(`{"private_path":"private native catalog diagnostic"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), browserContextKey{}, true)
	_, err := s.dispatch(ctx, model.WireRequest{Operation: "mesh_operator_kubernetes_artifacts", Params: json.RawMessage(`{}`)})
	if err == nil || strings.Contains(err.Error(), s.home) || strings.Contains(err.Error(), "private native") || !strings.Contains(err.Error(), "native trusted artifact inventory unavailable") {
		t.Fatal("catalog failure was not redacted", err)
	}
	if s.meshOperator != nil {
		t.Fatal("catalog failure started an operator worker")
	}
}
