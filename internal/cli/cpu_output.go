package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

func cpuBudgetMapKnown(b map[string]any) bool {
	if b == nil || b["known"] != true || b["stale"] == true {
		return false
	}
	available := b["observed_available_cores"] // approved remote summary
	if o, ok := b["observation"].(map[string]any); ok {
		if o["known"] != true {
			return false
		}
		available = o["available_cores"]
	}
	for _, v := range []any{available, b["capacity_cores"], b["reserved_cores"], b["available_cores"], b["deficit_cores"]} {
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		f, e := strconv.ParseFloat(n.String(), 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
			return false
		}
	}
	return true
}

func (a *app) cpuBudgetStatus(w io.Writer, b map[string]any) {
	known := cpuBudgetMapKnown(b)
	value := func(key string) any {
		if !known {
			return nil
		}
		return b[key]
	}
	cores := func(v any) string {
		if v == nil {
			return "unavailable"
		}
		return cpuDisplayValue(v) + " cores"
	}
	fmt.Fprintln(w, "\nCPU · LIVE HEADROOM")
	fmt.Fprintln(w, "  Live headroom: "+cores(value("capacity_cores")))
	fmt.Fprintln(w, "  Promised: "+cores(b["reserved_cores"]))
	fmt.Fprintln(w, "  To lease: "+cores(value("available_cores")))
	fmt.Fprintln(w, "  Shortfall: "+cores(value("deficit_cores")))
	if b["ceiling_cores"] != nil {
		fmt.Fprintln(w, "  Operator ceiling: "+cores(b["ceiling_cores"]))
	}
	if !known {
		fmt.Fprintln(w, "  CPU reading is unknown or stale; new CPU grants wait.")
	}
	fmt.Fprintln(w, "  Other apps share this CPU. Full promises stay charged, including CPU already counted in host activity; this can make new jobs wait.")
	fmt.Fprintln(w, "  Cooperative promises do not impose OS CPU limits or compare processor speed across hosts.")
}

func cpuDisplayValue(v any) string {
	n, ok := v.(json.Number)
	if !ok {
		return "unavailable"
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return "unavailable"
	}
	if f > 0 && f < .01 {
		return "<0.01"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 2, 64), "0"), ".")
}
