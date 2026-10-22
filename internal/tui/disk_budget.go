package tui

import "github.com/yrashk/arm/internal/model"

// Keep live admission evidence with the resource's structured read-only record;
// the older physical measurement alone cannot explain stale headroom.
func (m *dashboardModel) resourceRecord(resource *model.ResourceView) any {
	if resource != nil && resource.Kind == "disk" && m.data.status.Disk != nil {
		return map[string]any{"resource": resource, "disk_budget": m.data.status.Disk}
	}
	if resource == nil {
		return nil
	}
	return *resource
}
