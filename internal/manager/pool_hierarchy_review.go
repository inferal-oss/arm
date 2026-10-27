package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"

	"getarm.org/internal/model"
)

const maxPoolHierarchyReviewBytes = 4 << 20

type poolReviewDefinition struct {
	ID          string              `json:"id"`
	Parent      string              `json:"parent"`
	Name        string              `json:"name"`
	Authority   string              `json:"authority"`
	Owner       string              `json:"owner"`
	Scope       string              `json:"scope"`
	Host        string              `json:"host"`
	Unit        string              `json:"unit"`
	Capacity    int64               `json:"capacity"`
	Revision    int64               `json:"revision"`
	State       string              `json:"state"`
	Description string              `json:"description"`
	Attribution []string            `json:"attribution"`
	Policy      []string            `json:"policy"`
	Path        []string            `json:"path"`
	Partition   model.PoolPartition `json:"partition"`
}
type poolReviewConsumer struct {
	ID       string                 `json:"id"`
	Revision int                    `json:"revision"`
	State    string                 `json:"state"`
	Claims   []model.BoundPoolClaim `json:"claims"`
}
type hierarchyDigest struct {
	sum                     hash.Hash
	bytes, pools, consumers int
	unavailable             bool
}

func (d *hierarchyDigest) add(value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(raw)+1 > maxPoolHierarchyReviewBytes-d.bytes {
		return model.Err("hierarchy_review_too_large", "Complete hierarchy review exceeds 4 MiB; no scope was truncated and no change can be approved from this snapshot")
	}
	d.bytes += len(raw) + 1
	_, _ = d.sum.Write(raw)
	_, _ = d.sum.Write([]byte{'\n'})
	return nil
}

// Caller holds Manager.mu. No observation timestamps, process samples or
// telemetry enter this digest. Definition revisions and exact claims do.
func (m *Manager) hierarchyReviewTokens(t *poolTree, leases []model.Lease, accounting map[string]model.PoolPartition, selectedRoot string) (map[string]*model.PoolHierarchyReview, map[string]string, error) {
	groups := map[string]*hierarchyDigest{}
	ids := make([]string, 0, len(t.nodes))
	for id := range t.nodes {
		if selectedRoot != "" && t.paths[id][0] != selectedRoot {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := t.nodes[id]
		path := t.paths[id]
		root := path[0]
		g := groups[root]
		if g == nil {
			g = &hierarchyDigest{sum: sha256.New()}
			groups[root] = g
		}
		d := poolReviewDefinition{p.ID, p.Parent, p.Name, p.Authority, p.Owner, p.Scope, p.Host, p.Unit, p.Capacity, p.Revision, p.State, p.Description, []string{p.OwnerUsername, p.Agent, p.Session, p.Registration, p.Project}, []string{p.Accounting, p.Expiry}, path, accounting[id]}
		if !g.unavailable {
			if e := g.add(d); e != nil {
				var api *model.Error
				if !errors.As(e, &api) || api.Code != "hierarchy_review_too_large" {
					return nil, nil, e
				}
				g.unavailable = true
			}
		}
		g.pools++
	}
	sorted := append([]model.Lease{}, leases...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, lease := range sorted {
		if !poolCharged(lease.State) && lease.State != "queued" {
			continue
		}
		byRoot := map[string][]model.BoundPoolClaim{}
		for _, c := range lease.Request.Pools {
			path := t.paths[c.Pool]
			if len(path) == 0 {
				return nil, nil, model.Err("invalid_accounting", "Hierarchy claim has no exact recorded lineage")
			}
			root := path[0]
			if selectedRoot != "" && root != selectedRoot {
				continue
			}
			byRoot[root] = append(byRoot[root], model.BoundPoolClaim{Pool: c.Pool, Quantity: c.Quantity, Path: path})
		}
		for root, claims := range byRoot {
			sort.Slice(claims, func(i, j int) bool { return claims[i].Pool < claims[j].Pool })
			g := groups[root]
			if g.unavailable {
				continue
			}
			g.consumers++
			if g.consumers > maxReviewedPoolConsumers {
				g.unavailable = true
				continue
			}
			if e := g.add(poolReviewConsumer{lease.ID, lease.Revision, lease.State, claims}); e != nil {
				var api *model.Error
				if !errors.As(e, &api) || api.Code != "hierarchy_review_too_large" {
					return nil, nil, e
				}
				g.unavailable = true
			}
		}
	}
	out := map[string]*model.PoolHierarchyReview{}
	unavailable := map[string]string{}
	for _, id := range ids {
		root := t.paths[id][0]
		g := groups[root]
		if g.unavailable {
			unavailable[id] = "hierarchy_review_too_large"
			continue
		}
		raw, _ := json.Marshal([]string{m.host, id, root, hex.EncodeToString(g.sum.Sum(nil))})
		digest := sha256.Sum256(raw)
		out[id] = &model.PoolHierarchyReview{Schema: 1, Pool: id, Root: root, Pools: g.pools, Consumers: g.consumers, SHA256: hex.EncodeToString(digest[:])}
	}
	return out, unavailable, nil
}
func validHierarchyReview(h *model.PoolHierarchyReview) bool {
	if h == nil || h.Schema != 1 || h.Pool == "" || len(h.Pool) > 128 || h.Root == "" || len(h.Root) > 128 || h.Pools < 1 || h.Pools > maxPoolDefinitions || h.Consumers < 0 || h.Consumers > maxReviewedPoolConsumers || len(h.SHA256) != 64 {
		return false
	}
	raw, e := hex.DecodeString(h.SHA256)
	return e == nil && fmt.Sprintf("%x", raw) == h.SHA256
}
