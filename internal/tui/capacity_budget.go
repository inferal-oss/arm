package tui

import (
	"time"

	"getarm.org/internal/model"
)

// A held status ages locally even when the transport remains connected.
func (m *dashboardModel) heldBudgetStale(stale bool, age, after time.Duration) bool {
	if stale || !m.connected || m.lastRefresh.IsZero() || m.timeNow.Before(m.lastRefresh) || after <= 0 {
		return true
	}
	elapsed := m.timeNow.Sub(m.lastRefresh)
	return age < 0 || elapsed >= after || age >= after-elapsed
}

func (m *dashboardModel) memoryBudgetForView() *model.MemoryBudget {
	if m.data.status.Memory == nil {
		return nil
	}
	copy := *m.data.status.Memory
	copy.Stale = m.heldBudgetStale(copy.Stale, copy.Age, copy.StaleAfter)
	return &copy
}

func memoryBudgetKnown(b *model.MemoryBudget) bool {
	return b != nil && !b.Stale && b.Observation.Total != nil && *b.Observation.Total > 0 &&
		b.Observation.Available != nil && *b.Observation.Available >= 0 && *b.Observation.Available <= *b.Observation.Total &&
		b.Capacity >= 0 && b.Reserved >= 0 && b.Available >= 0 && b.Deficit >= 0
}

func (m *dashboardModel) diskBudgetForView() *model.DiskBudget {
	if m.data.status.Disk == nil {
		return nil
	}
	copy := *m.data.status.Disk
	copy.Stale = m.heldBudgetStale(copy.Stale, copy.Age, copy.StaleAfter)
	return &copy
}

func diskBudgetKnown(b *model.DiskBudget) bool { return b != nil && b.Known && !b.Stale }

// Availability comes from each backend admission budget, never from a fixed
// host total minus usage. Promises remain visible when headroom is unknown.
func (m *dashboardModel) resourceCapacityTexts(r *model.ResourceView) (total, promised, available, deficit string) {
	total, promised, available, deficit = quantity(r.BudgetTotal, r.Unit), quantity(r.Reserved, r.Unit), quantity(r.Available, r.Unit), quantity(r.Deficit, r.Unit)
	switch r.Kind {
	case "cpu":
		return cpuBudgetValues(r, m.cpuBudgetForView())
	case "memory":
		total, available, deficit = "unavailable", "unavailable", "unavailable"
		if b := m.memoryBudgetForView(); b != nil {
			promised = quantity(model.CountQuantity(b.Reserved), r.Unit)
			if memoryBudgetKnown(b) {
				total = quantity(model.CountQuantity(b.Capacity), r.Unit)
				available = quantity(model.CountQuantity(b.Available), r.Unit)
				deficit = quantity(model.CountQuantity(b.Deficit), r.Unit)
			}
		}
	case "disk":
		total, available, deficit = "unavailable", "unavailable", "unavailable"
		if b := m.diskBudgetForView(); b != nil {
			promised = quantity(model.CountQuantity(b.Reserved), r.Unit)
			if diskBudgetKnown(b) {
				total = quantity(model.CountQuantity(b.Capacity), r.Unit)
				available = quantity(model.CountQuantity(b.Available), r.Unit)
				deficit = quantity(model.CountQuantity(b.Deficit), r.Unit)
			}
		}
	}
	return
}
