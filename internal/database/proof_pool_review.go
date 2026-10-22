package database

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

// Independent persisted dashboard concurrency shape. A snapshot digest is not
// authorization or settlement proof; current lineage/claims are independently
// verified by the resource ledger. Reject future/null evidence explicitly.
type proofPoolHierarchyReview struct {
	Schema    int    `json:"schema_version"`
	Pool      string `json:"pool"`
	Root      string `json:"root"`
	Pools     int    `json:"pool_count"`
	Consumers int    `json:"consumer_count"`
	SHA256    string `json:"snapshot_sha256"`
}
type proofPoolLeaseRevision struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}
type proofPoolDashboardReview struct {
	Unavailable string                    `json:"unavailable,omitempty"`
	Authority   string                    `json:"authority"`
	Consumers   []proofPoolLeaseRevision  `json:"consumers"`
	Hierarchy   *proofPoolHierarchyReview `json:"hierarchy,omitempty"`
}

func (p *proofReader) validatePoolReviewRecord(raw string, q model.PoolMutation) error {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &fields)
	data, exists := fields["reviewed"]
	if !exists {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("explicit null dashboard pool review")
	}
	var view proofPoolDashboardReview
	if e := strictEvidence(string(data), &view, "consumers"); e != nil {
		return e
	}
	if view.Unavailable != "" || view.Authority != p.result.Host || view.Consumers == nil || len(view.Consumers) > 8192 {
		return fmt.Errorf("dashboard pool review authority/count unsupported")
	}
	seen := map[string]bool{}
	for _, v := range view.Consumers {
		if v.ID == "" || len(v.ID) > 128 || v.Revision < 1 || seen[v.ID] {
			return fmt.Errorf("dashboard pool consumer snapshot invalid")
		}
		seen[v.ID] = true
	}
	var reviewFields map[string]json.RawMessage
	_ = json.Unmarshal(data, &reviewFields)
	if h, ok := reviewFields["hierarchy"]; ok && bytes.Equal(bytes.TrimSpace(h), []byte("null")) {
		return fmt.Errorf("explicit null pool hierarchy review")
	}
	if h := view.Hierarchy; h != nil {
		selected := q.ID
		if q.Operation == "create" {
			selected = q.Parent
		}
		path := p.poolPaths[selected]
		if h.Schema != 1 || h.Pool != selected || len(path) == 0 || h.Root != path[0] || h.Pools < 1 || h.Pools > 4096 || h.Consumers < 0 || h.Consumers > 8192 || !sshProofHex(h.SHA256, 32) {
			return fmt.Errorf("dashboard hierarchy review has unsupported scope/shape")
		}
	}
	return nil
}
