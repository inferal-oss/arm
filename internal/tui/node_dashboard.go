package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/model"
)

// Remote summary is deliberately NOT model.Status. The peer supplies a narrow
// allowlisted DTO, without local contacts, provider paths or event cursors.
type nodeResourceSummary struct {
	ID        string      `json:"id"`
	Host      string      `json:"host"`
	Kind      string      `json:"kind"`
	Unit      string      `json:"unit"`
	Total     json.Number `json:"budget_total"`
	Reserved  json.Number `json:"reserved"`
	Available json.Number `json:"available"`
	Known     *bool       `json:"availability_known"`
	Stale     bool        `json:"stale"`
}
type nodeSummary struct {
	Host      string                `json:"host"`
	Resources []nodeResourceSummary `json:"resources"`
	Leases    []model.Lease         `json:"leases"`
	Memory    json.RawMessage       `json:"memory"`
	Disk      json.RawMessage       `json:"disk"`
	CPU       json.RawMessage       `json:"cpu"`
	Reported  time.Time             `json:"reported_at"`
}
type nodeDashboard struct {
	selection model.NodeSelection
	session   *client.Session
	context   model.NodeContextReport
	metadata  *model.NodeMetadata
	summary   nodeSummary
	loading   bool
	connected bool
	err       string
	lastRead  time.Time
	selected  int
	detail    *model.Lease
}
type nodeReadMsg struct {
	ui       *nodeDashboard
	session  *client.Session
	context  model.NodeContextReport
	metadata *model.NodeMetadata
	summary  nodeSummary
	detail   *model.Lease
	opening  bool
	err      error
}
type nodeLostMsg struct {
	ui      *nodeDashboard
	session *client.Session
	err     error
}

func (m *dashboardModel) openSelectedNode() tea.Cmd {
	ui := m.meshUI
	if ui == nil || ui.node == nil || !m.connected {
		return nil
	}
	node := ui.node
	if node.Local {
		m.nodeUI = nil
		m.view = 0
		m.viewport.GotoTop()
		return nil
	}
	if m.nodeUI != nil && m.nodeUI.session != nil {
		m.nodeUI.session.Close()
	}
	n := &nodeDashboard{selection: model.NodeSelection{Host: node.Host, Source: m.data.status.Host}, loading: true}
	m.nodeUI = n
	m.view = 1
	m.record = nil
	m.viewport.GotoTop()
	ctx, home := m.ctx, m.home
	return func() tea.Msg {
		session, report, err := client.OpenNodeSession(ctx, home, n.selection)
		msg := nodeReadMsg{ui: n, session: session, context: report, opening: true, err: err}
		if err == nil {
			msg = readNodeSnapshot(ctx, msg)
		}
		if msg.err != nil && session != nil {
			session.Close()
		}
		return msg
	}
}

func readNodeSnapshot(ctx context.Context, msg nodeReadMsg) nodeReadMsg {
	if msg.err = msg.session.Call(ctx, "node_context", nil, &msg.context); msg.err != nil {
		return msg
	}
	if msg.context.Context.Host != msg.ui.selection.Host || msg.context.Context.Source != msg.ui.selection.Source {
		msg.err = model.Err("wrong_host", "selected authority changed")
		return msg
	}
	msg.err = msg.session.Call(ctx, "status", nil, &msg.summary)
	if msg.err == nil && msg.summary.Host != msg.ui.selection.Host {
		msg.err = model.Err("wrong_host", "destination status differs from selected host")
	}
	// Metadata requires observe-host independently; unavailable metadata does
	// not convert an authorized own-lease view into a total connection failure.
	if msg.err == nil {
		var metadata model.NodeMetadata
		if err := msg.session.Call(ctx, "node_metadata", nil, &metadata); err == nil {
			if metadata.Host != msg.ui.selection.Host {
				msg.err = model.Err("wrong_host", "reported metadata differs from selected host")
			} else {
				msg.metadata = &metadata
			}
		}
	}
	return msg
}

func (m *dashboardModel) refreshNode(id string) tea.Cmd {
	ui := m.nodeUI
	if ui == nil || !ui.connected || ui.loading || ui.session == nil {
		return nil
	}
	ui.loading = true
	ctx, session := m.ctx, ui.session
	return func() tea.Msg {
		msg := nodeReadMsg{ui: ui, session: session, context: ui.context}
		if id != "" {
			msg.detail = &model.Lease{}
			msg.err = session.Call(ctx, "inspect", map[string]string{"id": id}, msg.detail)
			if msg.err == nil && msg.detail.Host != ui.selection.Host {
				msg.err = model.Err("wrong_host", "lease differs from selected host")
			}
		} else {
			msg = readNodeSnapshot(ctx, msg)
		}
		return msg
	}
}

func (m *dashboardModel) acceptNodeRead(msg nodeReadMsg) tea.Cmd {
	if m.nodeUI != msg.ui || (!msg.opening && msg.session != msg.ui.session) {
		if msg.opening && msg.session != nil {
			msg.session.Close()
		}
		return nil
	}
	ui := m.nodeUI
	ui.loading = false
	if msg.err != nil {
		ui.err = safe(msg.err.Error()) + " · no localhost fallback"
		var api *model.Error
		invalidContext := errors.As(msg.err, &api) && (api.Code == "wrong_host" || api.Code == "node_context_changed")
		if msg.opening || invalidContext {
			ui.connected = false
			if ui.session != nil {
				ui.session.Close()
			}
		}
		return nil
	}
	ui.err = ""
	if msg.opening {
		ui.session = msg.session
		ui.connected = true
	}
	if msg.detail != nil {
		ui.detail = msg.detail
		m.viewport.GotoTop()
	} else {
		ui.summary = msg.summary
		ui.context = msg.context
		ui.metadata = msg.metadata
		ui.lastRead = time.Now()
	}
	if msg.opening {
		return func() tea.Msg {
			<-msg.session.Done()
			return nodeLostMsg{ui: ui, session: msg.session, err: msg.session.Err()}
		}
	}
	return nil
}

func (m *dashboardModel) nodeKey(k string) tea.Cmd {
	ui := m.nodeUI
	if ui == nil {
		return nil
	}
	switch k {
	case "7":
		if ui.session != nil {
			ui.session.Close()
		}
		m.nodeUI = nil
		m.view = 6
		m.viewport.GotoTop()
		return nil
	case "esc":
		if ui.detail != nil {
			ui.detail = nil
			m.viewport.GotoTop()
			return nil
		}
		return m.nodeKey("7")
	case "1", "2", "3", "4", "5", "6":
		m.view = int(k[0] - '1')
		ui.detail = nil
		ui.selected = 0
		m.viewport.GotoTop()
	case "r":
		return m.refreshNode("")
	case "enter":
		if m.view == 2 && ui.selected < len(ui.summary.Leases) && ui.detail == nil {
			return m.refreshNode(ui.summary.Leases[ui.selected].ID)
		}
	case "i":
		var record any = ui.summary
		if m.view == 5 {
			record = ui.metadata
		}
		if ui.detail != nil {
			record = ui.detail
		}
		m.record, _ = newRecord(record, "Selected remote authority · read only")
		m.viewport.GotoTop()
	case "j", "down":
		if m.view == 2 && ui.detail == nil {
			ui.selected = min(max(0, len(ui.summary.Leases)-1), ui.selected+1)
		} else {
			m.viewport.ScrollDown(1)
		}
	case "k", "up":
		if m.view == 2 && ui.detail == nil {
			ui.selected = max(0, ui.selected-1)
		} else {
			m.viewport.ScrollUp(1)
		}
	default:
		scrollReview(&m.viewport, k)
	}
	return nil
}

func (m *dashboardModel) nodeText() string {
	ui := m.nodeUI
	var out strings.Builder
	fmt.Fprintf(&out, "REMOTE NODE · READ-ONLY\nDestination: %s\nThrough LOCAL gateway: %s\n", safe(ui.selection.Host), safe(ui.selection.Source))
	if ui.metadata != nil {
		fmt.Fprintf(&out, "Reported: %s · %s / %s · ARM %s\n", safe(ui.metadata.Hostname), safe(ui.metadata.Platform.OS), safe(ui.metadata.Platform.Architecture), safe(ui.metadata.Build.Version))
	}
	if ui.err != "" {
		out.WriteString("\nSTALE / UNAVAILABLE · " + ui.err + "\n")
	}
	if ui.loading {
		out.WriteString("\nReading this exact node…\n")
	}
	if !ui.connected {
		out.WriteString("\nDisconnected. Return to local Mesh [7] and explicitly reopen this node.\n")
	}
	if ui.context.AccessScope != nil {
		out.WriteString("\nCurrent destination permission observation:\n" + meshGrantText(&ui.context.AccessScope.Grant) + "\n")
	}
	out.WriteString("\n")
	if ui.detail != nil {
		return out.String() + "DESTINATION LEASE · INSPECTION ONLY\n\n" + meshExactJSON(ui.detail) + "\n\nNo lease mutations are implemented here. [i] Collapsible record · Esc leases"
	}
	switch m.view {
	case 1:
		for _, resource := range ui.summary.Resources {
			available := quantity(resource.Available, resource.Unit)
			unavailable := !ui.connected || ui.err != "" || !ui.lastRead.IsZero() && m.timeNow.Sub(ui.lastRead) > 5*time.Second || resource.Stale || resource.Known != nil && !*resource.Known || nodeBudgetUnavailable(ui, resource.Kind, m.timeNow)
			if unavailable {
				available = "stale / unavailable"
			}
			if resource.Kind == "cpu" {
				total := cpuQuantity(resource.Total)
				available = cpuQuantity(resource.Available)
				if unavailable {
					available = "stale / unavailable"
				}
				deficit, credited := "unavailable", "unavailable"
				if unavailable {
					total = "stale / unavailable"
				} else {
					var b struct {
						Deficit       float64 `json:"deficit_cores"`
						ManagedCredit float64 `json:"managed_credit_cores"`
					}
					if json.Unmarshal(ui.summary.CPU, &b) == nil {
						deficit = cpuQuantity(model.FloatQuantity(b.Deficit))
						credited = cpuQuantity(model.FloatQuantity(b.ManagedCredit))
					}
				}
				fmt.Fprintf(&out, "%s\nLive headroom: %s\nPromised: %s · to lease %s\nShortfall: %s\nManaged use credited: %s\nOther apps share this CPU · full promises stay charged\n\n", strong.Render("CPU · LIVE HEADROOM"), total, cpuQuantity(resource.Reserved), available, deficit, credited)
			} else {
				fmt.Fprintf(&out, "%s\nAvailable: %s\nTotal: %s · reserved %s\n\n", strong.Render(safe(resource.Kind)), available, quantity(resource.Total, resource.Unit), quantity(resource.Reserved, resource.Unit))
			}
		}
		if len(ui.summary.Resources) == 0 {
			out.WriteString("No resource summary visible under the current grant.\n")
		}
	case 2:
		out.WriteString("Only destination-authorized leases are shown; detail lookup remains source-owned.\n\n")
		for i, lease := range ui.summary.Leases {
			fmt.Fprintf(&out, "%s%s · %s\n  %s\n  Why: %s\n\n", selectedPrefix(ui.selected, i), safe(lease.ID), leaseLabel(lease), safe(lease.Host), safe(lease.Request.Reason))
		}
		if len(ui.summary.Leases) == 0 {
			out.WriteString("No leases visible under the current destination grant.\n")
		}
	case 5:
		if ui.metadata == nil {
			out.WriteString("Reported metadata unavailable under this grant.\n")
		} else {
			out.WriteString(nodeMetadataSummary(*ui.metadata))
		}
	default:
		out.WriteString("UNAVAILABLE OVER THIS READ-ONLY GATEWAY\n\nRemote monitor, events, messages and contacts are not implemented. No local stream or snapshot is substituted.\n")
	}
	out.WriteString("\n[r] Read destination · [i] Complete record · [7/Esc] LOCAL Mesh\nClosing this dashboard never retries operations or releases leases.")
	return out.String()
}

func nodeMetadataSummary(n model.NodeMetadata) string {
	var out strings.Builder
	fmt.Fprintf(&out, "REPORTED NODE METADATA\nHostname: %s\nOS: %s\nKernel: %s\nProcess architecture: %s\nARM version: %s\nGo version: %s\nLogical CPUs: %d\n", safe(n.Hostname), safe(n.Platform.OS), safe(n.Platform.KernelRelease), safe(n.Platform.Architecture), safe(n.Build.Version), safe(n.Build.GoVersion), n.Inventory.LogicalCPUs)
	for _, item := range []struct {
		label string
		size  model.NodeInventorySize
	}{{"Host memory", n.Inventory.Memory}, {"State filesystem", n.Inventory.StateFilesystem}} {
		quantity := "Unavailable"
		if item.size.Known && item.size.Total != nil {
			quantity = byteCount(fmt.Sprint(*item.size.Total))
		}
		if item.size.Stale {
			quantity += " · STALE"
		}
		fmt.Fprintf(&out, "%s total: %s\n", item.label, quantity)
		if item.size.ObservedAt != nil {
			fmt.Fprintf(&out, "  Measured: %s\n", item.size.ObservedAt.Format(time.RFC3339))
		}
	}
	out.WriteString("\nDeclared software capabilities · not permissions\n")
	for _, capability := range n.Capabilities {
		out.WriteString("  • " + safe(capability) + "\n")
	}
	if len(n.Unavailable) > 0 {
		out.WriteString("\nUnavailable facts: " + safe(strings.Join(n.Unavailable, ", ")) + "\n")
	}
	out.WriteString("\n[i] Complete collapsible reported record. Reported metadata is not authentication or a resource grant.\n")
	return out.String()
}

func nodeBudgetUnavailable(ui *nodeDashboard, kind string, now time.Time) bool {
	if kind == "cpu" {
		var b struct {
			Known     bool          `json:"known"`
			Stale     bool          `json:"stale"`
			Observed  *float64      `json:"observed_available_cores"`
			Capacity  *float64      `json:"capacity_cores"`
			Reserved  *float64      `json:"reserved_cores"`
			Available *float64      `json:"available_cores"`
			Deficit   *float64      `json:"deficit_cores"`
			Age       time.Duration `json:"age_ns"`
			After     time.Duration `json:"stale_after_ns"`
		}
		if json.Unmarshal(ui.summary.CPU, &b) != nil || !b.Known || b.Stale || b.After <= 0 || ui.lastRead.IsZero() {
			return true
		}
		for _, value := range []*float64{b.Observed, b.Capacity, b.Reserved, b.Available, b.Deficit} {
			if value == nil || *value < 0 {
				return true
			}
		}
		if b.Age < 0 || b.Age >= b.After {
			return true
		}
		return now.After(ui.lastRead) && now.Sub(ui.lastRead) >= b.After-b.Age
	}
	raw := ui.summary.Memory
	if kind == "disk" {
		raw = ui.summary.Disk
	} else if kind != "memory" {
		return false
	}
	var b struct {
		Known     *bool         `json:"known"`
		Stale     bool          `json:"stale"`
		Available *int64        `json:"estimated_available_bytes"`
		Age       time.Duration `json:"age_ns"`
		After     time.Duration `json:"stale_after_ns"`
	}
	if json.Unmarshal(raw, &b) != nil || string(raw) == "null" {
		return true
	}
	age := b.Age
	if !ui.lastRead.IsZero() && now.After(ui.lastRead) {
		age += now.Sub(ui.lastRead)
	}
	return b.Stale || b.Known != nil && !*b.Known || kind == "memory" && b.Available == nil || b.After > 0 && age > b.After
}
