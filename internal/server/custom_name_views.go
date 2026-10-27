package server

import (
	"context"

	"getarm.org/internal/customnames"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

type customNameIndex map[customnames.Entry]string

func makeCustomNameIndex(doc customnames.Document) customNameIndex {
	index := make(customNameIndex)
	for _, entry := range doc.Entries {
		if entry.Active {
			name := entry.Name
			entry.Name, entry.Active = "", false
			index[entry] = name
		}
	}
	return index
}

func (index customNameIndex) name(kind, authority, id, bindingLease string) string {
	return index[customnames.Entry{Kind: kind, AuthorityHost: authority, ID: id, BindingLease: bindingLease}]
}

func (index customNameIndex) lease(view namedLease) namedLease {
	if name := index.name("lease", view.Host, view.ID, ""); name != "" {
		view.DisplayName = name
	}
	if name := index.name("host", view.Host, view.Host, ""); name != "" {
		view.HostDisplayName = name
	}
	return view
}

func (index customNameIndex) uses(authority string, uses []namedResourceUse) []namedResourceUse {
	if uses == nil {
		return nil
	}
	view := append([]namedResourceUse{}, uses...)
	for i := range view {
		if name := index.name("lease", authority, view[i].Lease, ""); name != "" {
			view[i].LeaseDisplayName = name
		}
	}
	return view
}

func (index customNameIndex) resources(authority string, resources []namedResource) []namedResource {
	if resources == nil {
		return nil
	}
	view := append([]namedResource{}, resources...)
	originals := make([]model.ResourceView, len(view))
	for i := range view {
		originals[i] = view[i].ResourceView
	}
	portOwners := manager.CustomNamePortReservations(authority, originals)
	for i := range view {
		resource := &view[i]
		ref, err := manager.CustomNameResourceReference(authority, resource.ResourceView)
		if err == nil && (resource.Kind != "port" || portOwners[resource.ID] == ref.BindingLease) {
			if name := index[ref]; name != "" {
				resource.DisplayName = name
			}
		}
		if name := index.name("host", resource.Host, resource.Host, ""); name != "" {
			resource.HostDisplayName = name
		}
		resource.Users = index.uses(authority, resource.Users)
	}
	return view
}

func (index customNameIndex) pool(view namedPoolView) namedPoolView {
	if name := index.name("pool", view.Pool.Authority, view.Pool.ID, ""); name != "" {
		view.DisplayName = name
	}
	view.Users = index.uses(view.Pool.Authority, view.Users)
	view.Queued = index.uses(view.Pool.Authority, view.Queued)
	return view
}

type customNodeRegistry struct {
	model.NodeRegistry
	NamingDiagnostic *model.Error `json:"naming_diagnostic,omitempty"`
}

func namingViewOperation(operation string) bool {
	switch operation {
	case "status", "resources", "inspect", "acquire", "run", "release", "cancel", "renew", "reconcile", "storage_cleanup", "storage_handoff", "storage_retain", "storage_list", "node_list", "node_inspect", "pool_list", "pool_inspect", "pool_consumers":
		return true
	}
	return false
}

// Apply local cosmetic metadata after the existing authorized response has
// been projected/redacted. This never runs on destination mesh responses or
// changes a durable receipt, request, signed review or canonical action model.
func (s *Server) customNamedView(ctx context.Context, operation string, result any) (any, *model.Error) {
	if snapshot, ok := result.(nameSnapshot); ok && operation == "name_list" {
		return snapshot, snapshot.NamingDiagnostic
	}
	if !namingViewOperation(operation) {
		return result, nil
	}
	doc, err := s.Manager.CustomNames(ctx)
	diagnostic := nameReadDiagnostic(err)
	index := makeCustomNameIndex(doc)
	switch view := result.(type) {
	case namedStatus:
		view.NamingDiagnostic = diagnostic
		if name := index.name("host", view.Host, view.Host, ""); name != "" {
			view.DisplayName = name
		}
		if view.Leases != nil {
			view.Leases = append([]namedLease{}, view.Leases...)
			for i := range view.Leases {
				view.Leases[i] = index.lease(view.Leases[i])
			}
		}
		view.Resources = index.resources(view.Host, view.Resources)
		return view, diagnostic
	case namedLease:
		return index.lease(view), diagnostic
	case []namedLease:
		if view != nil {
			view = append([]namedLease{}, view...)
			for i := range view {
				view[i] = index.lease(view[i])
			}
		}
		return view, diagnostic
	case []namedResource:
		return index.resources(doc.Host, view), diagnostic
	case namedPoolView:
		return index.pool(view), diagnostic
	case []namedPoolView:
		if view != nil {
			view = append([]namedPoolView{}, view...)
			for i := range view {
				view[i] = index.pool(view[i])
			}
		}
		return view, diagnostic
	case namedPoolConsumerPage:
		if view.Entries != nil {
			view.Entries = append([]namedPoolConsumer{}, view.Entries...)
			for i := range view.Entries {
				use := view.Entries[i].Use
				if name := index.name("lease", doc.Host, use.Lease, ""); name != "" {
					view.Entries[i].Use.LeaseDisplayName = name
				}
			}
		}
		return view, diagnostic
	case model.NodeRegistry:
		if view.Nodes != nil {
			view.Nodes = append([]model.NodeEntry{}, view.Nodes...)
			for i := range view.Nodes {
				if name := index.name("host", view.Nodes[i].Host, view.Nodes[i].Host, ""); name != "" {
					view.Nodes[i].DisplayName = name
				}
			}
		}
		return customNodeRegistry{NodeRegistry: view, NamingDiagnostic: diagnostic}, diagnostic
	case model.NodeEntry:
		if name := index.name("host", view.Host, view.Host, ""); name != "" {
			view.DisplayName = name
		}
		return view, diagnostic
	}
	return result, diagnostic
}

func poolProjectedRead(operation string) bool {
	return operation == "pool_list" || operation == "pool_inspect" || operation == "pool_consumers"
}
