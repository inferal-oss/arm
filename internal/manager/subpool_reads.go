package manager

import (
	"sort"

	"getarm.org/internal/model"
)

func poolPageLimit(q model.PoolTreeQuery) (int, error) {
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.After) > 128 || len(q.ID) > 128 {
		return 0, model.Err("invalid_request", "choose a bounded ID/cursor and limit1..100")
	}
	return q.Limit, nil
}
func (m *Manager) PoolChildren(q model.PoolTreeQuery) (model.PoolTreePage, error) {
	out := model.PoolTreePage{Parent: q.ID, Nodes: []model.PoolTreeNode{}, Cursor: q.After}
	limit, err := poolPageLimit(q)
	if err != nil {
		return out, err
	}
	if q.Descendants {
		return out, model.Err("invalid_request", "children lists one level; walk child IDs explicitly")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.checkedPoolTree()
	if err != nil {
		return out, err
	}
	if q.ID != "" && t.nodes[q.ID].ID == "" {
		return out, model.Err("pool_not_found", "parent pool is missing")
	}
	ls, err := m.list()
	if err != nil {
		return out, err
	}
	used, err := poolReservations(ls)
	if err != nil {
		return out, err
	}
	a, err := t.accounting(used)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for id, p := range t.nodes {
		if p.Parent == q.ID && p.State != "deleted" && id > q.After {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if len(out.Nodes) == limit {
			out.More = true
			break
		}
		children := 0
		for _, child := range t.children[id] {
			if t.nodes[child].State != "deleted" {
				children++
			}
		}
		out.Nodes = append(out.Nodes, model.PoolTreeNode{Pool: t.nodes[id], Partition: a[id], Ancestors: append([]string{}, t.paths[id]...), Children: children})
		out.Cursor = id
	}
	return out, nil
}
func (m *Manager) PoolConsumers(q model.PoolTreeQuery) (model.PoolConsumerPage, error) {
	out := model.PoolConsumerPage{Pool: q.ID, Descendants: q.Descendants, Entries: []model.PoolConsumer{}, Cursor: q.After}
	limit, err := poolPageLimit(q)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.checkedPoolTree()
	if err != nil {
		return out, err
	}
	if t.nodes[q.ID].ID == "" {
		return out, model.Err("pool_not_found", "select an exact pool ID")
	}
	ls, err := m.list()
	if err != nil {
		return out, err
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].ID < ls[j].ID })
	for _, l := range ls {
		if l.ID <= q.After || !poolCharged(l.State) && l.State != "queued" {
			continue
		}
		binding, err := m.poolClaimBinding(t, l)
		if err != nil {
			return out, err
		}
		claims := []model.BoundPoolClaim{}
		var total int64
		for _, c := range binding.Claims {
			matches := c.Pool == q.ID
			if q.Descendants {
				for _, id := range c.Path {
					matches = matches || id == q.ID
				}
			}
			if matches {
				total, err = addPoolCount(total, c.Quantity)
				if err != nil {
					return out, err
				}
				claims = append(claims, c)
			}
		}
		if len(claims) == 0 {
			continue
		}
		if len(out.Entries) == limit {
			out.More = true
			break
		}
		out.Entries = append(out.Entries, model.PoolConsumer{Use: model.ResourceUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, State: l.State, Quantity: model.CountQuantity(total), Expires: l.Expires}, Claims: claims})
		out.Cursor = l.ID
	}
	return out, nil
}
