package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/kubeprovision"
)

type kubeInventoryUI struct {
	contexts     []kubeprovision.ContextChoice
	contextIndex int
	namespace    string
	query        string
	nodes        []kubeprovision.Node
	selected     int
	chosen       *kubeprovision.Node
	sequence     uint64
	loading      bool
	err          string
}

type kubeInventoryMsg struct {
	ui       *kubeInventoryUI
	session  transport
	sequence uint64
	contexts []kubeprovision.ContextChoice
	result   struct {
		Cluster   kubeprovision.Cluster   `json:"cluster"`
		Namespace kubeprovision.Namespace `json:"namespace"`
		Nodes     []kubeprovision.Node    `json:"nodes"`
	}
	contextsOnly bool
	err          error
}

func (m *dashboardModel) readKubeContexts(ui *kubeInventoryUI) tea.Cmd {
	if m.session == nil {
		ui.err = "Connect to the local backend to browse Kubernetes Nodes."
		return nil
	}
	ui.loading = true
	ui.sequence++
	sequence, session, ctx := ui.sequence, m.session, m.ctx
	return func() tea.Msg {
		msg := kubeInventoryMsg{ui: ui, session: session, sequence: sequence, contextsOnly: true}
		msg.err = session.Call(ctx, "kube_contexts", nil, &msg.contexts)
		return msg
	}
}

func (m *dashboardModel) readKubeNodes(ui *kubeInventoryUI) tea.Cmd {
	if m.session == nil || len(ui.contexts) == 0 {
		return nil
	}
	ui.loading = true
	ui.sequence++
	sequence, session, ctx := ui.sequence, m.session, m.ctx
	contextName, namespace, query := ui.contexts[ui.contextIndex].Name, ui.namespace, ui.query
	ui.chosen = nil
	return func() tea.Msg {
		msg := kubeInventoryMsg{ui: ui, session: session, sequence: sequence}
		msg.err = session.Call(ctx, "kube_nodes", map[string]any{"context": contextName, "namespace": namespace, "query": query, "limit": 50}, &msg.result)
		return msg
	}
}

func (m *dashboardModel) acceptKubeInventory(msg kubeInventoryMsg) tea.Cmd {
	if m.meshUI == nil || m.meshUI.kube != msg.ui || msg.ui.sequence != msg.sequence || m.session != msg.session {
		return nil
	}
	ui := msg.ui
	ui.loading = false
	if msg.err != nil {
		ui.err = "Kubernetes inventory unavailable: " + safe(msg.err.Error())
		ui.nodes = nil
		ui.chosen = nil
		return nil
	}
	ui.err = ""
	if msg.contextsOnly {
		ui.contexts = msg.contexts
		ui.contextIndex = 0
		for i, c := range ui.contexts {
			if c.Current {
				ui.contextIndex = i
				break
			}
		}
		if len(ui.contexts) == 0 {
			ui.err = "No Kubernetes contexts are available."
			return nil
		}
		ui.namespace = ui.contexts[ui.contextIndex].Namespace
		return m.readKubeNodes(ui)
	}
	ui.nodes = msg.result.Nodes
	if ui.selected >= len(ui.nodes) {
		ui.selected = max(0, len(ui.nodes)-1)
	}
	return nil
}

func (m *dashboardModel) kubeKey(key string) tea.Cmd {
	ui := m.meshUI.kube
	if ui == nil {
		return nil
	}
	if len(ui.contexts) == 0 && key != "esc" {
		return nil
	}
	switch key {
	case "esc":
		m.meshUI.mode = "nodes"
		m.meshUI.kube = nil
		return nil
	case "[", "]":
		if len(ui.contexts) == 0 {
			return nil
		}
		step := 1
		if key == "[" {
			step = -1
		}
		ui.contextIndex = (ui.contextIndex + step + len(ui.contexts)) % len(ui.contexts)
		ui.namespace = ui.contexts[ui.contextIndex].Namespace
		ui.query = ""
		ui.selected = 0
		ui.nodes = nil
		return m.readKubeNodes(ui)
	case "up":
		ui.selected = max(0, ui.selected-1)
	case "down":
		ui.selected = min(len(ui.nodes)-1, ui.selected+1)
		ui.selected = max(0, ui.selected)
	case "enter":
		if ui.selected < len(ui.nodes) {
			node := ui.nodes[ui.selected]
			if node.Ready && !node.Unschedulable && node.UID != "" {
				ui.chosen = &node
			} else {
				ui.err = "This Node is unavailable for selection."
			}
		}
	case "backspace":
		if len(ui.query) > 0 {
			ui.query = ui.query[:len(ui.query)-1]
			return m.scheduleKubeSearch(ui)
		}
	case "ctrl+r":
		return m.readKubeNodes(ui)
	default:
		if len(key) == 1 && key[0] >= 32 && key[0] <= 126 && len(ui.query) < 128 {
			ui.query += key
			return m.scheduleKubeSearch(ui)
		}
	}
	return nil
}

func (m *dashboardModel) scheduleKubeSearch(ui *kubeInventoryUI) tea.Cmd {
	ui.sequence++
	sequence := ui.sequence
	ui.chosen = nil
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg { return kubeSearchTick{ui: ui, sequence: sequence} })
}

type kubeSearchTick struct {
	ui       *kubeInventoryUI
	sequence uint64
}

func (m *dashboardModel) acceptKubeSearch(tick kubeSearchTick) tea.Cmd {
	if m.meshUI == nil || m.meshUI.kube != tick.ui || tick.ui.sequence != tick.sequence {
		return nil
	}
	return m.readKubeNodes(tick.ui)
}

func (m *dashboardModel) kubeText(ui *kubeInventoryUI) string {
	var out strings.Builder
	out.WriteString("KUBERNETES · LOCAL NODE INVENTORY\nType to search name, architecture, zone or instance type. Selection is read-only.\n\n")
	if len(ui.contexts) > 0 {
		c := ui.contexts[ui.contextIndex]
		fmt.Fprintf(&out, "Context %s · cluster %s · namespace %s\n[ / ] change context · Ctrl+R refresh\n", safe(c.Name), safe(c.ClusterName), safe(ui.namespace))
	}
	fmt.Fprintf(&out, "Search: %s_\n\n", safe(ui.query))
	if ui.loading {
		out.WriteString("Reading current Kubernetes Nodes…\n")
	}
	if ui.err != "" {
		out.WriteString(safe(ui.err) + "\n")
	}
	for i, n := range ui.nodes {
		state := "Ready"
		if !n.Ready || n.Unschedulable {
			state = "Unavailable"
		}
		fmt.Fprintf(&out, "%s%s · %s / %s · %s\n  CPU %s · RAM %s · disk %s\n  %s · %s · UID %s\n", selectedPrefix(ui.selected, i), safe(n.Name), safe(n.OS), safe(n.Arch), state, safe(n.AllocatableCPU), safe(n.AllocatableMemory), safe(n.AllocatableEphemeral), safe(n.Zone), safe(n.InstanceType), safe(n.UID))
		if len(n.Taints) > 0 {
			fmt.Fprintf(&out, "  Taints %s\n", safe(strings.Join(n.Taints, ", ")))
		}
		var extras []string
		for name, value := range n.Allocatable {
			if name != "cpu" && name != "memory" && name != "ephemeral-storage" {
				extras = append(extras, safe(name)+" "+safe(value))
			}
		}
		sort.Strings(extras)
		if len(extras) > 12 {
			extras = extras[:12]
		}
		if len(extras) > 0 {
			fmt.Fprintf(&out, "  Other allocatable: %s\n", strings.Join(extras, ", "))
		}
	}
	if ui.chosen != nil {
		fmt.Fprintf(&out, "\nSELECTED · context %s · namespace %s\nNode %s · exact UID %s\nUse these exact values in a separate current Pod plan. Selection creates nothing.\n", safe(ui.contexts[ui.contextIndex].Name), safe(ui.namespace), safe(ui.chosen.Name), safe(ui.chosen.UID))
	}
	out.WriteString("\nAllocatable is a scheduler figure, not live ARM CPU admission.\n[Enter] select Node · [Backspace] edit · Esc Mesh Nodes")
	return out.String()
}
