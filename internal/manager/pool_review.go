package manager

import "github.com/yrashk/arm/internal/model"

const maxReviewedPoolConsumers = 8192

// Called under the same lock as mutation/admission, after durable retry lookup.
// Review scope is complete; no truncation or subset can authorize a new change.
func (m *Manager) reviewPoolMutationLocked(r model.PoolMutation) error {
	if r.Reviewed == nil {
		return nil
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return err
	}
	if r.Parent != "" || r.ID != "" && (t.nodes[r.ID].Parent != "" || len(t.children[r.ID]) > 0) {
		return model.Err("hierarchy_review_required", "partition changes require a complete hierarchy review; use native CLI until hierarchy review controls are available")
	}
	if r.Reviewed.Authority != m.host {
		return model.Err("authority_unavailable", "review belongs to another local pool authority")
	}
	if r.Reviewed.Consumers == nil || len(r.Reviewed.Consumers) > maxReviewedPoolConsumers {
		return model.Err("invalid_request", "review the complete pool consumers (at most 8192); an empty review must be an explicit array")
	}
	if r.Operation != "create" && r.Revision == nil {
		return model.Err("invalid_request", "reviewed pool mutations require a definition revision")
	}
	expected := make(map[string]int, len(r.Reviewed.Consumers))
	for _, item := range r.Reviewed.Consumers {
		if item.ID == "" || len(item.ID) > 128 || item.Revision <= 0 || expected[item.ID] != 0 {
			return model.Err("invalid_request", "reviewed pool consumers require distinct IDs and positive revisions")
		}
		expected[item.ID] = item.Revision
	}
	if r.Operation == "create" {
		if len(expected) != 0 {
			return model.Err("invalid_request", "a new pool has no existing consumers")
		}
		return nil
	}
	leases, err := m.list()
	if err != nil {
		return err
	}
	for _, lease := range leases {
		if !poolCharged(lease.State) && lease.State != "queued" {
			continue
		}
		for _, claim := range lease.Request.Pools {
			if claim.Pool == r.ID {
				if expected[lease.ID] != lease.Revision {
					return model.Err("review_conflict", "pool consumers changed; refresh and review the complete current scope")
				}
				delete(expected, lease.ID)
				break
			}
		}
	}
	if len(expected) != 0 {
		return model.Err("review_conflict", "pool consumers changed; refresh and review the complete current scope")
	}
	return nil
}
