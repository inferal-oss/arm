package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/inferal-oss/arm/internal/model"
)

func cpuBudgetKnown(b *model.CPUBudget) bool {
	if b == nil || !b.Known || b.Stale || !b.Observation.Known || b.Observation.Available == nil {
		return false
	}
	for _, v := range []float64{*b.Observation.Available, b.Capacity, b.Reserved, b.Available, b.Deficit} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return false
		}
	}
	return true
}

func (m *dashboardModel) cpuBudgetForView() *model.CPUBudget {
	b := m.data.status.CPU
	if b == nil {
		return nil
	}
	copy := *b
	copy.Stale = m.heldBudgetStale(copy.Stale, copy.Age, copy.StaleAfter)
	return &copy
}
func cpuBudgetValues(r *model.ResourceView, b *model.CPUBudget) (total, promised, available, deficit string) {
	total, available, deficit = "unavailable", "unavailable", "unavailable"
	promised = cpuQuantity(r.Reserved)
	if b != nil {
		promised = cpuQuantity(model.FloatQuantity(b.Reserved))
	}
	if cpuBudgetKnown(b) {
		total = cpuQuantity(model.FloatQuantity(b.Capacity))
		available = cpuQuantity(model.FloatQuantity(b.Available))
		deficit = cpuQuantity(model.FloatQuantity(b.Deficit))
	}
	return
}
func cpuBudgetDetail(r *model.ResourceView, b *model.CPUBudget) string {
	total, promised, available, deficit := cpuBudgetValues(r, b)
	out := fmt.Sprintf("CPU · live host headroom\nHost: %s\n\nTo lease: %s\nLive headroom: %s\nPromised: %s\nShortfall: %s\n", safe(r.Host), available, total, promised, deficit)
	if cpuBudgetKnown(b) {
		out += "Managed use credited: " + cpuQuantity(model.FloatQuantity(b.ManagedCredit)) + "\n"
	}
	if b != nil && b.Ceiling != nil {
		out += "Operator ceiling: " + cpuQuantity(model.FloatQuantity(*b.Ceiling)) + "\n"
	}
	if !cpuBudgetKnown(b) {
		out += "\nNew CPU grants wait for a fresh headroom reading. Unknown does not mean idle.\n"
	}
	out += "\nOther apps share this CPU. Full promises remain charged. Verified aligned owned usage is credited where supported; unverified work can still be counted twice. [?] Help · [i] Observation and accounting details\n\nCONSUMERS · Enter opens selected lease\n"
	return out
}

func cpuQuantity(n json.Number) string {
	v, err := n.Float64()
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return "unavailable"
	}
	if v > 0 && v < .01 {
		return "<0.01 cores"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 2, 64), "0"), ".") + " cores"
}
