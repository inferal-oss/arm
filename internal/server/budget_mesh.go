package server

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/model"
)

// Adding local ledger evidence to Lease must not silently expand the closed
// mesh read contract. Peers consume the host owner's root internally; local
// policy definitions, lineage and ceiling snapshots remain native-only.
func meshLeaseWithoutPolicy(lease model.Lease) model.Lease {
	lease.Request.Budget = ""
	lease.Budget = nil
	lease.BudgetPolicy = nil
	lease.BudgetError = nil
	lease.PoolBinding = nil
	lease.PoolError = nil
	if lease.QueueInfo != nil {
		info := *lease.QueueInfo
		info.Blockers = append([]model.QueueBlocker(nil), info.Blockers...)
		for i, b := range info.Blockers {
			switch b.Code {
			case "owner_budget", "budget_limit", "budget_draining", "invalid_accounting":
				info.Blockers[i] = model.QueueBlocker{Code: "local_policy", Detail: "Local admission policy requires operator attention or additional headroom; peer policy administration is not granted."}
			}
		}
		lease.QueueInfo = &info
	}
	return lease
}
func meshWithoutPolicy(value any) (any, error) {
	switch v := value.(type) {
	case model.Lease:
		return meshLeaseWithoutPolicy(v), nil
	case []model.Lease:
		out := make([]model.Lease, len(v))
		for i, l := range v {
			out[i] = meshLeaseWithoutPolicy(l)
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = item
		}
		if leases, ok := v["leases"]; ok {
			clean, err := meshWithoutPolicy(leases)
			if err != nil {
				return nil, err
			}
			out["leases"] = clean
		}
		return out, nil
	case json.RawMessage:
		// Durable peer lifecycle retries return the original encoded Lease. Never
		// bypass the same projection merely because the response came from a key.
		var l model.Lease
		if err := json.Unmarshal(v, &l); err != nil || l.ID == "" {
			return nil, model.Err("remote_outcome_unknown", "saved lifecycle result cannot be projected safely; inspect the owned lease before another change")
		}
		clean, err := json.Marshal(meshLeaseWithoutPolicy(l))
		return json.RawMessage(clean), err
	default:
		return value, nil
	}
}
