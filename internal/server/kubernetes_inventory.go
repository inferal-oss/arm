package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/inferal-oss/arm/internal/kubeprovision"
	"github.com/inferal-oss/arm/internal/model"
)

// Kubernetes inventory is local to this backend. It does not route through
// mesh peers or grant a Pod/CPU entitlement.
func (s *Server) kubernetesInventory(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	if operation == "kube_contexts" {
		if len(bytes.TrimSpace(raw)) > 0 && string(bytes.TrimSpace(raw)) != "null" && string(bytes.TrimSpace(raw)) != "{}" {
			return nil, model.Err("invalid_request", "contexts takes no parameters")
		}
		choices, err := kubeprovision.Contexts()
		if err != nil {
			return nil, model.Err("kubernetes_unavailable", "Kubernetes contexts unavailable")
		}
		if len(choices) > 128 {
			return nil, model.Err("kubernetes_unavailable", "Kubernetes context inventory exceeds the dashboard limit")
		}
		encoded, err := json.Marshal(choices)
		if err != nil || len(encoded) > 128<<10 {
			return nil, model.Err("kubernetes_unavailable", "Kubernetes context inventory exceeds the dashboard limit")
		}
		return choices, nil
	}
	if len(raw) == 0 || len(raw) > 1024 {
		return nil, model.Err("invalid_request", "bounded Kubernetes inventory parameters required")
	}
	var q struct {
		Context   string `json:"context"`
		Namespace string `json:"namespace"`
		Query     string `json:"query"`
		Limit     int    `json:"limit"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var trailing any
	if err := decoder.Decode(&q); err != nil || decoder.Decode(&trailing) != io.EOF || len(q.Context) == 0 || len(q.Context) > 253 || len(q.Namespace) > 253 || len(q.Query) > 128 || q.Limit < 1 || q.Limit > 100 {
		return nil, model.Err("invalid_request", "select a context, query and limit within bounds")
	}
	choices, err := kubeprovision.Contexts()
	if err != nil {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes contexts unavailable")
	}
	if len(choices) > 128 {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes context inventory exceeds the dashboard limit")
	}
	selected := false
	for _, choice := range choices {
		if choice.Name == q.Context {
			selected = true
			if q.Namespace == "" {
				q.Namespace = choice.Namespace
			}
			break
		}
	}
	if !selected {
		return nil, model.Err("invalid_request", "selected Kubernetes context unavailable")
	}
	api, err := kubeprovision.OpenInventoryContext(q.Context)
	if err != nil {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes inventory transport unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cluster, err := api.Cluster(readCtx)
	if err != nil {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes identity unavailable")
	}
	namespace, err := api.Namespace(readCtx, q.Namespace)
	if err != nil {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes namespace unavailable")
	}
	nodes, err := (kubeprovision.Provider{API: api}).Candidates(readCtx, q.Query, q.Limit)
	if err != nil {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes Nodes unavailable")
	}
	result := struct {
		Cluster   kubeprovision.Cluster   `json:"cluster"`
		Namespace kubeprovision.Namespace `json:"namespace"`
		Nodes     []kubeprovision.Node    `json:"nodes"`
	}{cluster, namespace, nodes}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes Node inventory exceeds the dashboard limit")
	}
	return result, nil
}
