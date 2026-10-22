package server

import "github.com/yrashk/arm/internal/model"

// Peer summaries report target availability without local paths, filesystem
// identities, raw provider errors or any private owner-policy evidence.
func meshDiskSummary(b *model.DiskBudget) any {
	if b == nil {
		return nil
	}
	return map[string]any{"observation_at": b.Observation.At, "quality": b.Observation.Quality, "total_bytes": b.Observation.Total, "reported_available_bytes": b.Observation.Available, "capacity_bytes": b.Capacity, "reserved_bytes": b.Reserved, "available_bytes": b.Available, "ceiling_bytes": b.Ceiling, "deficit_bytes": b.Deficit, "known": b.Known, "stale": b.Stale, "age_ns": b.Age, "stale_after_ns": b.StaleAfter}
}
