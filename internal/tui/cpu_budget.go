package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/model"
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
	age := b.Age
	if !m.lastRefresh.IsZero() && m.timeNow.After(m.lastRefresh) {
		age += m.timeNow.Sub(m.lastRefresh)
	}
	copy.Stale = copy.Stale || !m.connected || m.lastRefresh.IsZero() || age < 0 || copy.StaleAfter <= 0 || age >= copy.StaleAfter
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
	out := fmt.Sprintf("CPU · live host headroom\nHost: %s\n\nLive headroom: %s\nPromised: %s\nTo lease: %s\nShortfall: %s\n", safe(r.Host), total, promised, available, deficit)
	if b != nil && b.Ceiling != nil {
		out += "Operator ceiling: " + cpuQuantity(model.FloatQuantity(*b.Ceiling)) + "\n"
	}
	if !cpuBudgetKnown(b) {
		out += "\nNew CPU grants wait for a fresh headroom reading. Unknown does not mean idle.\n"
	}
	out += "\nOther apps share this CPU. Full promises remain charged, including CPU already counted in host activity. This can make new jobs wait. [?] Help · [i] Observation and accounting details\n\nCONSUMERS · Enter opens selected lease\n"
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
