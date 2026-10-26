package manager

import (
	"sort"

	"github.com/inferal-oss/arm/internal/model"
)

func (m *Manager) subpoolQueueBlockers(l model.Lease, s queueSnapshot) ([]model.QueueBlocker, error) {
	if len(l.Request.Pools) == 0 {
		return nil, nil
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return nil, err
	}
	if _, err = m.poolClaimBinding(t, l); err != nil {
		return nil, err
	}
	a, err := t.accounting(s.poolUsed)
	if err != nil {
		return nil, err
	}
	next := map[string]int64{}
	for id, n := range s.poolUsed {
		next[id] = n
	}
	relevant := map[string]bool{}
	for _, c := range l.Request.Pools {
		next[c.Pool], err = addPoolCount(next[c.Pool], c.Quantity)
		if err != nil {
			return overflowPoolQueueBlockers(l, t, a), nil
		}
		for _, id := range t.paths[c.Pool] {
			relevant[id] = true
		}
	}
	after, err := t.accounting(next)
	if err != nil {
		return overflowPoolQueueBlockers(l, t, a), nil
	}
	out := []model.QueueBlocker{}
	for _, id := range sortedPoolIDs(relevant) {
		v := a[id]
		if v.Deficit == 0 && after[id].Deficit == 0 {
			continue
		}
		want := after[id].Committed - v.Committed
		code := "pool_capacity"
		detail := "Pool permits are unavailable; capacity may grow, but no future permit is reserved."
		if t.nodes[id].Parent != "" || len(t.children[id]) > 0 {
			code = "pool_partition"
			detail = "This pool retains direct claims and child backing. Idle child partitions cannot be borrowed; unresolved obligations retain backing after a reduction."
		}
		b := model.QueueBlocker{Code: code, Resource: id, Unit: t.nodes[id].Unit, Requested: number(model.CountQuantity(want)), Available: number(model.CountQuantity(v.Available)), Detail: detail, Partition: &v}
		for _, child := range t.children[id] {
			if a[child].Held > 0 {
				b.ChildCount++
				if len(b.Children) < 32 {
					b.Children = append(b.Children, child)
				}
			}
		}
		for _, lease := range s.leases {
			if !poolCharged(lease.State) {
				continue
			}
			matches := false
			for _, c := range lease.Request.Pools {
				for _, ancestor := range t.paths[c.Pool] {
					if ancestor == id {
						matches = true
					}
				}
			}
			if matches {
				b.BlockingCount++
				if len(b.Leases) < 32 {
					b.Leases = append(b.Leases, lease.ID)
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}

func sortedPoolIDs(v map[string]bool) []string {
	out := make([]string, 0, len(v))
	for id := range v {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func overflowPoolQueueBlockers(l model.Lease, t *poolTree, a map[string]model.PoolPartition) []model.QueueBlocker {
	out := []model.QueueBlocker{}
	seen := map[string]bool{}
	for _, c := range l.Request.Pools {
		for _, id := range t.paths[c.Pool] {
			if seen[id] {
				continue
			}
			seen[id] = true
			v := a[id]
			out = append(out, model.QueueBlocker{Code: "pool_capacity", Resource: id, Requested: number(model.CountQuantity(c.Quantity)), Available: number(model.CountQuantity(v.Available)), Unit: t.nodes[id].Unit, Partition: &v, Detail: "The complete candidate commitment exceeds representable capacity. Existing exact charges remain unchanged; release or a smaller request may allow admission."})
		}
	}
	return out
}
