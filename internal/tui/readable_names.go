package tui

import (
	"net"
	"strconv"
	"strings"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

// Display metadata stays outside the persisted/action models. These typed read
// adapters consume the same authorized responses, preserving exact integers,
// nil/empty slices and original lease/resource objects without JSON roundtrips.
type leaseRenderView struct {
	model.Lease
	DisplayName     string `json:"display_name"`
	HostDisplayName string `json:"host_display_name"`
}
type useRenderView struct {
	model.ResourceUse
	LeaseDisplayName string `json:"lease_display_name"`
}
type resourceRenderView struct {
	model.ResourceView
	DisplayName     string          `json:"display_name"`
	HostDisplayName string          `json:"host_display_name"`
	Users           []useRenderView `json:"users"`
}
type statusRenderView struct {
	NamingDiagnostic *model.Error `json:"naming_diagnostic,omitempty"`
	model.Status
	DisplayName string               `json:"display_name"`
	Leases      []leaseRenderView    `json:"leases"`
	Resources   []resourceRenderView `json:"resources"`
}
type leaseRenderLabel struct{ host, name, hostName string }
type renderLabels struct {
	authority, hostName string
	leases              map[string]leaseRenderLabel
	resources           map[string]string
}

// Source row offsets are emitted by the renderer, never rediscovered by
// matching user-controlled names, reasons or other visible strings.
type textRows struct {
	text string
	rows map[int]int
}

func (n *renderLabels) addLease(l model.Lease, name, hostName string) {
	if n.authority == "" || l.ID == "" || l.Host != "" && l.Host != n.authority {
		return
	}
	if n.leases == nil {
		n.leases = make(map[string]leaseRenderLabel)
	}
	n.leases[l.ID] = leaseRenderLabel{host: n.authority, name: name, hostName: hostName}
	if n.hostName == "" {
		n.hostName = hostName
	}
}
func (v statusRenderView) values() (model.Status, renderLabels) {
	s := v.Status
	names := renderLabels{authority: s.Host, hostName: v.DisplayName}
	if v.Leases != nil {
		s.Leases = make([]model.Lease, len(v.Leases))
		for i, l := range v.Leases {
			s.Leases[i] = l.Lease
			names.addLease(l.Lease, l.DisplayName, l.HostDisplayName)
		}
	}
	if v.Resources != nil {
		s.Resources = make([]model.ResourceView, len(v.Resources))
		for i, r := range v.Resources {
			s.Resources[i] = r.ResourceView
			if names.hostName == "" && (r.Host == "" || r.Host == s.Host) {
				names.hostName = r.HostDisplayName
			}
			if r.Users != nil {
				s.Resources[i].Users = make([]model.ResourceUse, len(r.Users))
				for j, u := range r.Users {
					s.Resources[i].Users[j] = u.ResourceUse
					if _, exists := names.leases[u.Lease]; !exists && (r.Host == "" || r.Host == s.Host) {
						names.addLease(model.Lease{ID: u.Lease, Host: s.Host}, u.LeaseDisplayName, r.HostDisplayName)
					}
				}
			}
			if r.ID != "" && (r.Host == "" || r.Host == s.Host) {
				if names.resources == nil {
					names.resources = make(map[string]string)
				}
				names.resources[r.ID] = r.DisplayName
			}
		}
	}
	return s, names
}

func readableLabel(domain, id, provided string) string {
	if strings.TrimSpace(provided) != "" {
		return safe(provided)
	}
	if id != "" {
		return readablename.FromID(domain, id)
	}
	return "Unknown " + domain
}
func nodeDisplayName(n model.NodeEntry) string {
	provided := n.DisplayName
	if (strings.TrimSpace(provided) == "" || provided == readablename.FromID(readablename.Host, n.Host)) && n.Name != "localhost" && strings.TrimSpace(n.Name) != "" {
		provided = n.Name
	}
	return readableLabel(readablename.Host, n.Host, provided)
}
func (m *dashboardModel) labelsForHost(host string) renderLabels {
	if m.nodeUI != nil && m.nodeUI.selection.Source == m.data.status.Host && host == m.nodeUI.selection.Host && m.nodeUI.summary.names.authority == host {
		return m.nodeUI.summary.names
	}
	if host == m.data.status.Host && m.data.names.authority == host {
		return m.data.names
	}
	return renderLabels{}
}
func (m *dashboardModel) hostDisplayName(host string) string {
	provided := m.labelsForHost(host).hostName
	fallback := readablename.FromID(readablename.Host, host)
	if (strings.TrimSpace(provided) == "" || provided == fallback) && m.nodeUI != nil && m.nodeUI.selection.Source == m.data.status.Host && host == m.nodeUI.selection.Host && m.nodeUI.displayName != "" {
		provided = m.nodeUI.displayName
	}
	if (strings.TrimSpace(provided) == "" || provided == fallback) && m.meshUI != nil && m.meshUI.registry.Source == m.data.status.Host {
		for _, n := range m.meshUI.registry.Nodes {
			if n.Host == host {
				return nodeDisplayName(n)
			}
		}
	}
	return readableLabel(readablename.Host, host, provided)
}
func (m *dashboardModel) leaseDisplayName(id, host string) string {
	if host == "" {
		host = m.data.status.Host
	}
	provided := ""
	if label, ok := m.labelsForHost(host).leases[id]; ok && label.host == host {
		provided = label.name
	}
	if m.nodeUI == nil && m.poolUI != nil && host == m.data.status.Host && m.poolUI.names.authority == host {
		if label, ok := m.poolUI.names.leases[id]; ok && label.host == host && strings.TrimSpace(label.name) != "" {
			provided = label.name
		}
	}
	return readableLabel(readablename.Lease, id, provided)
}
func (m *dashboardModel) consumerDisplayLabel(id, host string) string {
	return m.leaseDisplayName(id, host) + " · ID " + safe(id)
}
func resourceFallback(id, kind string) string {
	switch kind {
	case "cpu":
		return "CPU"
	case "memory":
		return "Memory"
	case "disk":
		return "Disk"
	case "custom":
		return readableLabel(readablename.Pool, id, "")
	}
	return readableLabel(readablename.Resource, id, "")
}
func poolDisplayName(p model.Pool) string { return readableLabel(readablename.Pool, p.ID, p.Name) }
func (m *dashboardModel) resourceDisplayName(r model.ResourceView) string {
	if r.Storage != nil {
		return safe(r.Storage.Name)
	}
	host := r.Host
	if host == "" {
		host = m.data.status.Host
	}
	if name := m.labelsForHost(host).resources[r.ID]; strings.TrimSpace(name) != "" {
		return safe(name)
	}
	if r.Custom != nil && strings.TrimSpace(r.Custom.Pool.Name) != "" {
		return safe(r.Custom.Pool.Name)
	}
	if r.Port != nil {
		if strings.TrimSpace(r.Port.Name) != "" {
			return safe(r.Port.Name)
		}
		return safe(r.Port.Network + "://" + net.JoinHostPort(r.Port.Address, strconv.Itoa(r.Port.Port)))
	}
	return resourceFallback(r.ID, r.Kind)
}

type nodeResourceRenderView struct {
	nodeResourceSummary
	DisplayName     string `json:"display_name"`
	HostDisplayName string `json:"host_display_name"`
}
type nodeSummaryRenderView struct {
	nodeSummary
	DisplayName string                   `json:"display_name"`
	Resources   []nodeResourceRenderView `json:"resources"`
	Leases      []leaseRenderView        `json:"leases"`
}

func (v nodeSummaryRenderView) values() nodeSummary {
	s := v.nodeSummary
	s.names = renderLabels{authority: s.Host, hostName: v.DisplayName}
	if v.Leases != nil {
		s.Leases = make([]model.Lease, len(v.Leases))
		for i, l := range v.Leases {
			s.Leases[i] = l.Lease
			s.names.addLease(l.Lease, l.DisplayName, l.HostDisplayName)
		}
	}
	if v.Resources != nil {
		s.Resources = make([]nodeResourceSummary, len(v.Resources))
		for i, r := range v.Resources {
			s.Resources[i] = r.nodeResourceSummary
			if s.names.hostName == "" && (r.Host == "" || r.Host == s.Host) {
				s.names.hostName = r.HostDisplayName
			}
			if r.ID != "" && (r.Host == "" || r.Host == s.Host) {
				if s.names.resources == nil {
					s.names.resources = make(map[string]string)
				}
				s.names.resources[r.ID] = r.DisplayName
			}
		}
	}
	return s
}

func (m *dashboardModel) poolDisplayName(pool model.Pool) string {
	provided := ""
	if pool.Authority == m.data.status.Host {
		if m.poolUI != nil && m.poolUI.names.authority == pool.Authority {
			provided = m.poolUI.names.resources[pool.ID]
		}
		if provided == "" && m.data.names.authority == pool.Authority {
			provided = m.data.names.resources[pool.ID]
		}
	}
	if strings.TrimSpace(provided) != "" {
		return safe(provided)
	}
	return poolDisplayName(pool)
}
