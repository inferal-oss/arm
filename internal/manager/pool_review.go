package manager

import (
	"bytes"
	"encoding/json"
	"github.com/yrashk/arm/internal/model"
	"reflect"
)

const maxReviewedPoolConsumers = 8192

// Called under the same lock as mutation/admission, after durable retry lookup.
// Review scope is complete; no truncation or subset can authorize a new change.
func (m *Manager) reviewPoolMutationLocked(r model.PoolMutation) error {
	if r.Reviewed == nil {
		return nil
	}
	if r.Reviewed.Unavailable != "" {
		return model.Err("hierarchy_review_too_large", "The complete selected tree exceeds review limits; it remains readable but cannot be changed through an incomplete dashboard review")
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return err
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
	if r.ID != "" && r.Revision != nil && t.nodes[r.ID].ID != "" && t.nodes[r.ID].Revision != *r.Revision {
		return model.Err("revision_conflict", "Pool definition changed; inspect its current revision before reviewing again")
	}
	selected := r.ID
	if r.Operation == "create" {
		selected = r.Parent
	}
	required := r.Parent != "" || selected != "" && (t.nodes[selected].Parent != "" || len(t.children[selected]) > 0)
	if required || r.Reviewed.Hierarchy != nil {
		if !validHierarchyReview(r.Reviewed.Hierarchy) || selected == "" || r.Reviewed.Hierarchy.Pool != selected {
			return model.Err("hierarchy_review_required", "Refresh and review the complete selected root tree before a partition change")
		}
		leases, e := m.list()
		if e != nil {
			return e
		}
		used, e := poolReservations(leases)
		if e != nil {
			return e
		}
		accounting, e := t.accounting(used)
		if e != nil {
			return e
		}
		root := ""
		if path := t.paths[selected]; len(path) > 0 {
			root = path[0]
		} else {
			return model.Err("pool_not_found", "Selected pool is missing")
		}
		tokens, unavailable, e := m.hierarchyReviewTokens(t, leases, accounting, root)
		if e != nil {
			return e
		}
		if unavailable[selected] != "" {
			return model.Err("hierarchy_review_too_large", "Complete selected tree exceeds review limits; no snapshot was truncated")
		}
		if !reflect.DeepEqual(tokens[selected], r.Reviewed.Hierarchy) {
			return model.Err("review_conflict", "Pool lineage, backing or queued/consuming claims changed; refresh and review the complete tree")
		}
	}
	if r.Operation == "create" && r.Parent == "" {
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
			if claim.Pool == selected {
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

// DecodePoolMutation closes the native/dashboard review wire shape before
// encoding/json can normalize aliases or explicit null concurrency evidence.
func DecodePoolMutation(raw []byte) (model.PoolMutation, error) {
	var r model.PoolMutation
	if len(raw) == 0 || len(raw) > 60<<10 {
		return r, model.Err("invalid_request", "One pool change within 60 KiB is required")
	}
	if e := decodePoolRecord(raw, &r); e != nil {
		return r, model.Err("invalid_request", e.Error())
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if review, ok := fields["reviewed"]; ok {
		if bytes.Equal(bytes.TrimSpace(review), []byte("null")) {
			return r, model.Err("invalid_request", "Omit absent review evidence; explicit null is unsupported")
		}
		var scope map[string]json.RawMessage
		_ = json.Unmarshal(review, &scope)
		if h, ok := scope["hierarchy"]; ok && bytes.Equal(bytes.TrimSpace(h), []byte("null")) {
			return r, model.Err("invalid_request", "Omit absent hierarchy evidence; explicit null is unsupported")
		}
	}
	return r, nil
}
