package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/model"
)

const maxBudgetDepth = 16
const maxBudgetDefinitions = 4096
const maxBudgetPoolLimits = 64

func ownerBudgetID(authority, uid string) string {
	raw, _ := json.Marshal([]string{authority, "local_uid", uid})
	hash := sha256.Sum256(raw)
	return "budget_owner_" + hex.EncodeToString(hash[:])
}
func budgetText(value string, limit int, required bool) bool {
	if len(value) > limit || !utf8.ValidString(value) || (required && strings.TrimSpace(value) == "") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func validBudgetLimits(limits model.BudgetLimits) error {
	if limits.CPU != nil && (math.IsNaN(*limits.CPU) || math.IsInf(*limits.CPU, 0) || *limits.CPU < 0) {
		return model.Err("invalid_request", "CPU policy ceiling must be finite and nonnegative; omit it for no additional ceiling")
	}
	if (limits.Memory != nil && *limits.Memory < 0) || (limits.Disk != nil && *limits.Disk < 0) {
		return model.Err("invalid_request", "byte policy ceilings must be nonnegative; explicit zero means zero, not automatic")
	}
	if len(limits.Pools) > maxBudgetPoolLimits {
		return model.Err("invalid_request", "at most64 explicit pool ceilings per policy budget")
	}
	for id, count := range limits.Pools {
		if !budgetText(id, 128, true) || count < 0 {
			return model.Err("invalid_request", "pool ceilings require exact nonempty IDs and nonnegative int64 counts")
		}
	}
	return nil
}
func copyBudgetLimits(limits model.BudgetLimits) model.BudgetLimits {
	result := limits
	if limits.CPU != nil {
		value := *limits.CPU
		result.CPU = &value
	}
	if limits.Memory != nil {
		value := *limits.Memory
		result.Memory = &value
	}
	if limits.Disk != nil {
		value := *limits.Disk
		result.Disk = &value
	}
	if limits.Pools != nil {
		result.Pools = make(map[string]int64, len(limits.Pools))
		for key, value := range limits.Pools {
			result.Pools[key] = value
		}
	}
	return result
}
func addBudgetAmounts(a, b model.BudgetAmounts) (model.BudgetAmounts, error) {
	result := model.BudgetAmounts{Pools: map[string]int64{}}
	for _, r := range []model.Resources{a.Resources, b.Resources} {
		if math.IsNaN(r.CPU) || math.IsInf(r.CPU, 0) || r.CPU < 0 || r.Memory < 0 || r.Disk < 0 {
			return result, model.Err("invalid_accounting", "persisted policy charge is negative or nonfinite")
		}
	}
	if b.Resources.CPU > math.MaxFloat64-a.Resources.CPU || b.Resources.Memory > math.MaxInt64-a.Resources.Memory || b.Resources.Disk > math.MaxInt64-a.Resources.Disk {
		return result, model.Err("invalid_accounting", "policy charge aggregation overflows")
	}
	result.Resources = a.Resources.Add(b.Resources)
	if math.IsInf(result.Resources.CPU, 0) {
		return result, model.Err("invalid_accounting", "CPU policy charge aggregation overflows")
	}
	for _, source := range []map[string]int64{a.Pools, b.Pools} {
		for id, value := range source {
			if value < 0 || value > math.MaxInt64-result.Pools[id] {
				return result, model.Err("invalid_accounting", "pool policy charge aggregation is negative or overflows")
			}
			result.Pools[id] += value
		}
	}
	return result, nil
}

// Available ceilings refer only to policy, never a grant of physical/live
// capacity. Descendant and ancestor rollups intentionally overlap.
func remainingBudgetLimits(limits model.BudgetLimits, charged model.BudgetAmounts) (model.BudgetLimits, model.BudgetAmounts) {
	available := copyBudgetLimits(limits)
	deficit := model.BudgetAmounts{Pools: map[string]int64{}}
	if limits.CPU != nil {
		value := max(0, *limits.CPU-charged.Resources.CPU)
		available.CPU = &value
		deficit.Resources.CPU = max(0, charged.Resources.CPU-*limits.CPU)
	}
	if limits.Memory != nil {
		value := max(int64(0), *limits.Memory-charged.Resources.Memory)
		available.Memory = &value
		deficit.Resources.Memory = max(int64(0), charged.Resources.Memory-*limits.Memory)
	}
	if limits.Disk != nil {
		value := max(int64(0), *limits.Disk-charged.Resources.Disk)
		available.Disk = &value
		deficit.Resources.Disk = max(int64(0), charged.Resources.Disk-*limits.Disk)
	}
	for id, limit := range limits.Pools {
		available.Pools[id] = max(int64(0), limit-charged.Pools[id])
		deficit.Pools[id] = max(int64(0), charged.Pools[id]-limit)
	}
	return available, deficit
}
func minBudgetLimits(a, b model.BudgetLimits) model.BudgetLimits {
	result := copyBudgetLimits(a)
	if b.CPU != nil && (result.CPU == nil || *b.CPU < *result.CPU) {
		value := *b.CPU
		result.CPU = &value
	}
	if b.Memory != nil && (result.Memory == nil || *b.Memory < *result.Memory) {
		value := *b.Memory
		result.Memory = &value
	}
	if b.Disk != nil && (result.Disk == nil || *b.Disk < *result.Disk) {
		value := *b.Disk
		result.Disk = &value
	}
	if len(b.Pools) > 0 && result.Pools == nil {
		result.Pools = map[string]int64{}
	}
	for id, value := range b.Pools {
		if existing, ok := result.Pools[id]; !ok || value < existing {
			result.Pools[id] = value
		}
	}
	return result
}
