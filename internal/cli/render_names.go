package cli

import (
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

// Render keys are canonical identities, never selectors. Resource labels are
// read directly from their wire views; reusable port locators are not cached.
type cliRenderKey struct{ kind, authority, id string }
type cliLeaseView struct {
	model.Lease
	DisplayName     string `json:"display_name"`
	HostDisplayName string `json:"host_display_name"`
}

// leaseRenderResult keeps additive view labels from the same RPC while copying
// exact typed models into the original result. It does not alter wire requests,
// stored intentions, JSON output models or retry/review payloads.
func (a *app) leaseRenderResult(result any) (any, func()) {
	switch target := result.(type) {
	case *model.Lease:
		if target == nil {
			return result, func() {}
		}
		view := cliLeaseView{Lease: *target}
		return &view, func() { *target = view.Lease; a.captureLeaseLabels([]cliLeaseView{view}) }
	case *[]model.Lease:
		if target == nil {
			return result, func() {}
		}
		var views []cliLeaseView
		return &views, func() {
			if views == nil {
				*target = nil
			} else {
				leases := make([]model.Lease, len(views))
				for i, view := range views {
					leases[i] = view.Lease
				}
				*target = leases
			}
			a.captureLeaseLabels(views)
		}
	default:
		return result, func() {}
	}
}

func (a *app) captureLeaseLabels(views []cliLeaseView) {
	// Each accepted typed response owns its labels; an empty/new scope cannot
	// borrow a prior response's custom labels.
	a.renderNames = map[cliRenderKey]string{}
	for _, view := range views {
		if !opaqueID(view.Host) || !opaqueID(view.ID) {
			continue
		}
		key := cliRenderKey{readablename.Lease, view.Host, view.ID}
		delete(a.renderNames, key)
		if view.DisplayName != "" {
			a.renderNames[key] = view.DisplayName
		}
		if view.HostDisplayName != "" {
			a.renderNames[cliRenderKey{readablename.Host, view.Host, view.Host}] = view.HostDisplayName
		}
	}
}

func (a *app) humanName(kind, authority, id, provided, supplied string) string {
	if provided == "" {
		provided = a.renderNames[cliRenderKey{kind, authority, id}]
	}
	fallback := readablename.FromID(kind, id)
	if provided != "" && provided != fallback {
		return provided
	}
	if supplied != "" {
		return supplied
	}
	if provided != "" {
		return provided
	}
	return fallback
}

func renderString(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}
