package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func cpuStatusPresentation() map[string]any {
	return map[string]any{"host": "target-node", "resources": []any{map[string]any{"kind": "cpu", "budget_total": 64, "reserved": 1.25, "available": 62.75, "unit": "logical-core-equivalents", "availability_known": true}}, "cpu_budget": map[string]any{"known": true, "stale": false, "capacity_cores": 2.5, "reserved_cores": 1.25, "available_cores": 1.25, "deficit_cores": 0, "ceiling_cores": 8, "observation": map[string]any{"known": true, "available_cores": 2.5}}}
}

func TestCPUStatusHumanLiveHeadroomAndMachineOutput(t *testing.T) {
	var out bytes.Buffer
	a := app{out: &out, outputTTY: true, commandName: "status"}
	value := cpuStatusPresentation()
	if e := a.print(value); e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"Host: target-node", "CPU · LIVE HEADROOM", "Live headroom: 2.5 cores", "Promised: 1.25 cores", "To lease: 1.25 cores", "Shortfall: 0 cores", "counted in host activity"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	if strings.Contains(out.String(), "62.75") {
		t.Fatal("legacy fixed pool leaked into current headroom", out.String())
	}
	out.Reset()
	a.forceJSON = true
	if e := a.print(value); e != nil {
		t.Fatal(e)
	}
	var compact bytes.Buffer
	if e := json.Compact(&compact, out.Bytes()); e != nil {
		t.Fatal(e)
	}
	want, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(compact.Bytes(), want) {
		t.Fatal("presentation modified complete machine data", out.String())
	}
}

func TestCPUStatusUnknownStaleAndBusyZeroRemainDistinct(t *testing.T) {
	for _, kind := range []string{"unknown", "stale", "busy-zero", "remote"} {
		t.Run(kind, func(t *testing.T) {
			value := cpuStatusPresentation()
			cpu := value["cpu_budget"].(map[string]any)
			switch kind {
			case "unknown":
				cpu["known"] = false
			case "stale":
				cpu["stale"] = true
			case "busy-zero":
				cpu["capacity_cores"] = 0
				cpu["available_cores"] = 0
				cpu["deficit_cores"] = 1.25
				cpu["observation"].(map[string]any)["available_cores"] = 0
			case "remote":
				delete(cpu, "observation")
				cpu["observed_available_cores"] = 2.5
				value["cpu"] = cpu
				delete(value, "cpu_budget")
			}
			var out bytes.Buffer
			a := app{out: &out, outputTTY: true, commandName: "status"}
			if e := a.print(value); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(out.String(), "Promised: 1.25 cores") {
				t.Fatal("uncertainty hid promises", out.String())
			}
			if kind == "busy-zero" {
				if !strings.Contains(out.String(), "Live headroom: 0 cores") || !strings.Contains(out.String(), "Shortfall: 1.25 cores") {
					t.Fatal(out.String())
				}
			} else if kind == "remote" {
				if !strings.Contains(out.String(), "Live headroom: 2.5 cores") {
					t.Fatal(out.String())
				}
			} else if !strings.Contains(out.String(), "Live headroom: unavailable") || !strings.Contains(out.String(), "new CPU grants wait") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestCPUDisplayPrecision(t *testing.T) {
	for _, row := range []struct{ raw, want string }{{"7.03584", "7.04"}, {"18", "18"}, {"0.25", "0.25"}, {"0.0004", "<0.01"}, {"0", "0"}} {
		if got := cpuDisplayValue(json.Number(row.raw)); got != row.want {
			t.Fatal(row, got)
		}
	}
}
