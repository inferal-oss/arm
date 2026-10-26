package server

import "github.com/yrashk/arm/internal/model"

// Export only target headroom/freshness. Native affinity and cgroup paths,
// collector diagnostics and local process evidence stay on their own host.
func meshCPUSummary(b *model.CPUBudget) any {
	if b == nil {
		return nil
	}
	return map[string]any{"observation_at": b.Observation.At, "quality": b.Observation.Quality,
		"eligible_cpus": b.Observation.EligibleCPUs, "observed_capacity_cores": b.Observation.Capacity,
		"busy_cores": b.Observation.Busy, "observed_available_cores": b.Observation.Available,
		"capacity_cores": b.Capacity, "reserved_cores": b.Reserved, "managed_credit_cores": b.ManagedCredit, "available_cores": b.Available,
		"ceiling_cores": b.Ceiling, "deficit_cores": b.Deficit, "known": b.Known,
		"stale": b.Stale, "age_ns": b.Age, "stale_after_ns": b.StaleAfter}
}
