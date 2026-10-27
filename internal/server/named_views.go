package server

import (
	"net"
	"strconv"

	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

// These wrappers exist only at the response boundary. Original identities,
// persisted records, signed reviews, request hashes and journals are untouched.
// Typed embedding preserves exact integer values and omitted/null shapes.
type namedLease struct {
	model.Lease
	DisplayName     string `json:"display_name"`
	HostDisplayName string `json:"host_display_name"`
}
type namedResourceUse struct {
	model.ResourceUse
	LeaseDisplayName string `json:"lease_display_name"`
}
type namedResource struct {
	model.ResourceView
	DisplayName     string             `json:"display_name"`
	HostDisplayName string             `json:"host_display_name"`
	Users           []namedResourceUse `json:"users"`
}
type namedStatus struct {
	model.Status
	DisplayName      string          `json:"display_name"`
	Leases           []namedLease    `json:"leases"`
	Resources        []namedResource `json:"resources"`
	NamingDiagnostic *model.Error    `json:"naming_diagnostic,omitempty"`
}

func leaseView(l model.Lease) namedLease {
	return namedLease{Lease: l, DisplayName: readablename.FromID(readablename.Lease, l.ID), HostDisplayName: readablename.FromID(readablename.Host, l.Host)}
}
func leaseViews(ls []model.Lease) []namedLease {
	if ls == nil {
		return nil
	}
	out := make([]namedLease, len(ls))
	for i, l := range ls {
		out[i] = leaseView(l)
	}
	return out
}
func resourceLabel(r model.ResourceView) string {
	if r.Custom != nil && r.Custom.Pool.Name != "" {
		return r.Custom.Pool.Name
	}
	if r.Port != nil {
		if r.Port.Name != "" {
			return r.Port.Name
		}
		return r.Port.Network + "://" + net.JoinHostPort(r.Port.Address, strconv.Itoa(r.Port.Port))
	}
	switch r.Kind {
	case "custom":
		return readablename.FromID(readablename.Pool, r.ID)
	case "cpu":
		return "CPU"
	case "memory":
		return "Memory"
	case "disk":
		return "Disk"
	}
	return readablename.FromID(readablename.Resource, r.ID)
}
func resourceViews(rs []model.ResourceView) []namedResource {
	if rs == nil {
		return nil
	}
	out := make([]namedResource, len(rs))
	for i, r := range rs {
		var users []namedResourceUse
		if r.Users != nil {
			users = make([]namedResourceUse, len(r.Users))
			for j, u := range r.Users {
				users[j] = namedResourceUse{ResourceUse: u, LeaseDisplayName: readablename.FromID(readablename.Lease, u.Lease)}
			}
		}
		out[i] = namedResource{ResourceView: r, DisplayName: resourceLabel(r), HostDisplayName: readablename.FromID(readablename.Host, r.Host), Users: users}
	}
	return out
}

func (s *Server) hostDisplayName(host string) string {
	// Reuse only the already-open local identity cache; no filesystem or RPC
	// read, and no peer-name expansion beyond the authorized response payload.
	s.meshMu.Lock()
	service := s.mesh
	s.meshMu.Unlock()
	if service != nil {
		identity := service.store.Config().Identity
		if identity.Host == host && identity.Name != "" {
			return identity.Name
		}
	}
	return readablename.FromID(readablename.Host, host)
}

func (s *Server) namedView(operation string, result any) any {
	switch operation {
	case "status":
		if status, ok := result.(model.Status); ok {
			return namedStatus{Status: status, DisplayName: s.hostDisplayName(status.Host), Leases: leaseViews(status.Leases), Resources: resourceViews(status.Resources)}
		}
	case "resources":
		if rs, ok := result.([]model.ResourceView); ok {
			return resourceViews(rs)
		}
	case "inspect", "acquire", "run", "release", "cancel", "renew", "reconcile", "storage_cleanup", "storage_handoff", "storage_retain":
		if l, ok := result.(model.Lease); ok {
			return leaseView(l)
		}
	case "storage_list":
		if ls, ok := result.([]model.Lease); ok {
			return leaseViews(ls)
		}
	}
	return result
}

func (s *Server) namedPeerView(operation string, result any) any {
	// Immutable remote mutation receipts may return a concrete first result or
	// retained raw JSON on an exact-key retry. Preserve both wire shapes without
	// projecting display metadata into either the response or durable receipt.
	switch operation {
	case "status":
		// mesh dispatch strips native-only policy fields from concrete leases
		// before reaching this response boundary. Keep that type until filtering
		// has completed; wrapping earlier would bypass its closed type switch.
		if status, ok := result.(map[string]any); ok {
			view := make(map[string]any, len(status)+1)
			for key, value := range status {
				view[key] = value
			}
			if host, ok := status["host"].(string); ok {
				view["display_name"] = readablename.FromID(readablename.Host, host)
			}
			if leases, ok := status["leases"].([]model.Lease); ok {
				view["leases"] = leaseViews(leases)
			}
			return view
		}
		return result
	case "resources", "inspect":
		return s.namedView(operation, result)
	default:
		return result
	}
}
