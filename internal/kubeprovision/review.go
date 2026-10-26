package kubeprovision

import (
	"context"
	"errors"
)

// Draft contains only operator-selected inputs and source-owned artifact
// evidence. The API supplies current authenticated cluster, namespace and Node
// identities; the caller must show the returned Plan before Apply.
type Draft struct {
	Key             string    `json:"key"`
	Owner           string    `json:"owner"`
	Reason          string    `json:"reason"`
	Context         string    `json:"context"`
	Namespace       string    `json:"namespace"`
	SelectedNode    string    `json:"selected_node"`
	SelectedNodeUID string    `json:"selected_node_uid"`
	Image           string    `json:"image"`
	BinarySHA256    string    `json:"binary_sha256"`
	BinaryBytes     int64     `json:"binary_bytes"`
	CatalogSHA256   string    `json:"catalog_sha256"`
	Resources       Resources `json:"resources"`
}

// Prepare is read-only. It refuses a changed selected Node UID and never
// resolves an existing operation through the ambient current context.
func (p Provider) Prepare(ctx context.Context, d Draft) (Plan, error) {
	if p.API == nil || d.Context == "" || d.Namespace == "" || d.SelectedNode == "" || d.SelectedNodeUID == "" {
		return Plan{}, errors.New("explicit context, namespace and selected Node UID required")
	}
	cluster, err := p.API.Cluster(ctx)
	if err != nil {
		return Plan{}, err
	}
	if cluster.Context != d.Context {
		return Plan{}, ErrIdentity
	}
	namespace, err := p.API.Namespace(ctx, d.Namespace)
	if err != nil {
		return Plan{}, err
	}
	node, err := p.API.Node(ctx, d.SelectedNode)
	if err != nil {
		return Plan{}, err
	}
	if node.UID != d.SelectedNodeUID {
		return Plan{}, ErrIdentity
	}
	return NewPlan(Review{Key: d.Key, Owner: d.Owner, Reason: d.Reason, Cluster: cluster, Namespace: namespace,
		Node: node, Image: d.Image, BinarySHA256: d.BinarySHA256, BinaryBytes: d.BinaryBytes,
		CatalogSHA256: d.CatalogSHA256, Resources: d.Resources})
}
