package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"getarm.org/internal/kubeprovision"
	"getarm.org/internal/model"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Only fixed diagnostics cross the browser boundary. API errors can contain
// credential helper output, private endpoints, names and admission messages.
func kubernetesInventoryError(stage string, err error) error {
	switch {
	case apierrors.IsForbidden(err):
		return model.Err("kubernetes_forbidden", "Kubernetes access denied while reading "+stage+"; check the selected identity's RBAC")
	case apierrors.IsUnauthorized(err):
		return model.Err("kubernetes_unauthorized", "Kubernetes credentials were not accepted while reading "+stage)
	case apierrors.IsNotFound(err):
		return model.Err("kubernetes_not_found", "Selected Kubernetes "+stage+" no longer exists; refresh the selection")
	case errors.Is(err, context.DeadlineExceeded), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return model.Err("kubernetes_timeout", "Kubernetes timed out while reading "+stage+"; no empty inventory was established")
	case errors.Is(err, context.Canceled):
		return model.Err("kubernetes_cancelled", "Kubernetes inventory read cancelled; refresh before selecting")
	default:
		return model.Err("kubernetes_unavailable", "Kubernetes "+stage+" unavailable; no empty inventory was established")
	}
}

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
		return nil, model.Err("kubernetes_transport_unavailable", "Kubernetes inventory transport or credentials are unsupported or unavailable; context discovery alone does not establish API access")
	}
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cluster, err := api.Cluster(readCtx)
	if err != nil {
		return nil, kubernetesInventoryError("effective identity", err)
	}
	namespace, err := api.Namespace(readCtx, q.Namespace)
	if err != nil {
		return nil, kubernetesInventoryError("Namespace", err)
	}
	all, err := api.Nodes(readCtx)
	if err != nil {
		return nil, kubernetesInventoryError("Nodes", err)
	}
	nodes := kubeprovision.FilterNodes(all, q.Query, q.Limit)
	// Counts describe a complete bounded cluster-scoped Node read, before
	// typeahead filtering. Namespace is a separate installation prerequisite;
	// it never becomes a Node label selector.
	matched := 0
	for _, node := range all {
		if len(kubeprovision.FilterNodes([]kubeprovision.Node{node}, q.Query, 1)) != 0 {
			matched++
		}
	}
	result := struct {
		Cluster   kubeprovision.Cluster   `json:"cluster"`
		Namespace kubeprovision.Namespace `json:"namespace"`
		Nodes     []kubeprovision.Node    `json:"nodes"`
		Inventory struct {
			Total     int  `json:"total"`
			Matched   int  `json:"matched"`
			Truncated bool `json:"truncated"`
		} `json:"inventory"`
	}{Cluster: cluster, Namespace: namespace, Nodes: nodes}
	result.Inventory.Total = len(all)
	result.Inventory.Matched = matched
	result.Inventory.Truncated = matched > len(nodes)
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1<<20 {
		return nil, model.Err("kubernetes_unavailable", "Kubernetes Node inventory exceeds the dashboard limit")
	}
	return result, nil
}
