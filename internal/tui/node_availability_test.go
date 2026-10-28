package tui

import (
	"encoding/json"
	"fmt"
	"getarm.org/internal/model"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/nodeavailability"
)

func TestPreparedAvailabilityTextSeparatesDeclarationAndFreshness(t *testing.T) {
	host := strings.Repeat("a", 32)
	report := &nodeavailability.Report{Schema: 1, Host: host, OwnerDeclared: nodeavailability.Declaration{Profile: "intermittent", Revision: 1}}
	text := meshAvailabilityText(host, report, "reported", "offline", "stale")
	for _, want := range []string{"Mobile / intermittent (owner declared)", "Connection: Offline", "Stale; unavailable for placement", "Automatic cross-host placement is not implemented"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing availability boundary", want, text)
		}
	}
	if text := meshAvailabilityText(strings.Repeat("b", 32), report, "reported", "reachable", "unknown"); strings.Contains(text, "Mobile / intermittent") {
		t.Fatal("another host declaration was rebound", text)
	}
}

func TestNodeAvailabilityActivatedCharmExactReadsAndFallback(t *testing.T) {
	for _, unsupported := range []bool{false, true} {
		t.Run(fmt.Sprint(unsupported), func(t *testing.T) {
			m, s := fixtureDashboard(t)
			host := strings.Repeat("a", 32)
			m.meshUI = &meshDashboard{mode: "nodes"}
			now := time.Now().UTC()
			s.reply = func(operation string, params, result any) error {
				if params.(map[string]string)["host"] != host {
					t.Fatal("host selector changed", params)
				}
				switch operation {
				case "node_inspect":
					*result.(*model.NodeEntry) = model.NodeEntry{Host: host, Name: "Mobile worker"}
				case "node_availability_read_v1":
					if unsupported {
						return model.Err("unsupported", "fixture")
					}
					raw, _ := json.Marshal(nodeavailability.Report{Schema: 1, Host: host, OwnerDeclared: nodeavailability.Declaration{Profile: "intermittent", Revision: 1, UpdatedAt: &now}, ObservedAt: now})
					*result.(*json.RawMessage) = raw
				case "node_availability_inspect_v1":
					if unsupported {
						return model.Err("unsupported", "fixture")
					}
					*result.(*nodeavailability.Inspection) = nodeavailability.Inspection{Schema: 1, Host: host, ConnectionState: "offline", Resources: nodeavailability.ResourceStates{CPU: "stale", Memory: "stale", Disk: "stale"}}
				default:
					t.Fatal("unexpected operation", operation)
				}
				return nil
			}
			cmd := m.readMesh("node", host)
			if cmd == nil {
				t.Fatal("node read unavailable")
			}
			m.acceptMeshRead(cmd().(meshReadMsg))
			if m.meshUI.node == nil || m.meshUI.node.Host != host || m.meshUI.err != "" {
				t.Fatal("optional capability hid exact node", m.meshUI)
			}
			text := meshAvailabilityDetails(host, m.meshUI.availabilityReport, m.meshUI.availabilityInspection)
			want := "Mobile / intermittent"
			if unsupported {
				want = "Unknown / unsupported"
			}
			if !strings.Contains(text, want) {
				t.Fatal("missing declaration state", text)
			}
			if !unsupported && (!strings.Contains(text, "Offline") || !strings.Contains(text, "Stale; unavailable for placement")) {
				t.Fatal("freshness conflated", text)
			}
		})
	}
}

func TestNodeAvailabilityActivatedCharmSelectionFence(t *testing.T) {
	m, s := fixtureDashboard(t)
	host := strings.Repeat("a", 32)
	ui := &meshDashboard{sequence: 2, mode: "node", node: &model.NodeEntry{Host: host}}
	m.meshUI = ui
	old := meshReadMsg{ui: ui, session: s, sequence: 1, mode: "node", node: &model.NodeEntry{Host: strings.Repeat("b", 32)}, availabilityReport: &nodeavailability.Report{Schema: 1, Host: strings.Repeat("b", 32), OwnerDeclared: nodeavailability.Declaration{Profile: "stable"}}}
	m.acceptMeshRead(old)
	if ui.node.Host != host || ui.availabilityReport != nil {
		t.Fatal("late selected read replaced current availability")
	}
}

func TestNodeAvailabilityActivatedCharmEscapeRetiresOriginalRead(t *testing.T) {
	m, s := fixtureDashboard(t)
	host := strings.Repeat("a", 32)
	ui := &meshDashboard{mode: "node", node: &model.NodeEntry{Host: host}, loading: true, sequence: 1, nodeSelection: 1}
	m.meshUI = ui
	m.meshKey("esc")
	if !ui.loading {
		t.Fatal("escape abandoned original read ownership")
	}
	m.acceptMeshRead(meshReadMsg{ui: ui, session: s, sequence: 1, nodeSelection: 1, mode: "node", node: &model.NodeEntry{Host: host}, availabilityReport: &nodeavailability.Report{Schema: 1, Host: host}})
	if ui.loading || ui.mode != "nodes" || ui.node != nil || ui.availabilityReport != nil {
		t.Fatal("late original read reopened escaped availability", ui)
	}
}
