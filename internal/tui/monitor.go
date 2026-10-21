package tui

import (
	"fmt"
	"strings"

	"github.com/yrashk/arm/internal/model"
)

func (m *dashboardModel) monitorText() string {
	s := m.page.Latest
	if s == nil {
		return "Waiting for the first host sample…\nUnknown measurements are shown as unavailable."
	}
	width := max(10, min(60, m.mainWidth()-22))
	var out strings.Builder
	out.WriteString("CPU  " + measurement(s.CPU) + "\n")
	out.WriteString(accent.Render(historyLine(m.history, width, m.page.Cadence, 100, func(p model.MonitorPoint) *float64 { return p.CPU })) + "\n\n")
	for i, core := range s.Cores {
		fmt.Fprintf(&out, "%s %s", safe(core.ID), measurement(core.Busy))
		if i%4 == 3 || i == len(s.Cores)-1 {
			out.WriteString("\n")
		} else {
			out.WriteString("   ")
		}
	}
	resource := func(label string, r model.ResourceMeasurement, value func(model.MonitorPoint) *float64) {
		fmt.Fprintf(&out, "\n%s\nUsed %s · Available %s · Total %s\n", label, measurement(r.Used), measurement(r.Available), measurement(r.Total))
		ceiling := float64(0)
		if r.Total.Value != nil {
			ceiling = *r.Total.Value
		}
		out.WriteString(accent.Render(historyLine(m.history, width, m.page.Cadence, ceiling, value)) + "\n")
	}
	resource("MEMORY", s.Memory, func(p model.MonitorPoint) *float64 { return p.MemoryUsed })
	resource("SWAP", s.Swap, func(p model.MonitorPoint) *float64 { return p.SwapUsed })
	fsLabel := "FILESYSTEM"
	if s.FilesystemStale {
		fsLabel += " · stale"
	}
	resource(fsLabel, s.Filesystem, func(p model.MonitorPoint) *float64 { return p.FilesystemAvailable })
	if s.Filesystem.Filesystem != nil {
		fs := s.Filesystem.Filesystem
		out.WriteString(safe(fs.Type) + " · " + safe(fs.Mount))
		if fs.Shared {
			out.WriteString(" · shared pool")
		}
		out.WriteString("\n")
	}
	ioGroup := func(label string, items []model.MonitorIO, disk bool) {
		out.WriteString("\n" + label + "\n")
		if len(items) == 0 {
			out.WriteString("Unavailable for this host/sample.\n")
		}
		for _, item := range items {
			fmt.Fprintf(&out, "%s   ↓ %s   ↑ %s\n", safe(item.Name), measurement(item.Read), measurement(item.Write))
			ceiling := float64(0)
			for _, p := range m.history {
				samples := p.Network
				if disk {
					samples = p.DiskIO
				}
				for _, v := range samples {
					if v.ID == item.ID {
						if v.Read != nil {
							ceiling = max(ceiling, *v.Read)
						}
						if v.Write != nil {
							ceiling = max(ceiling, *v.Write)
						}
					}
				}
			}
			get := func(read bool) func(model.MonitorPoint) *float64 {
				return func(p model.MonitorPoint) *float64 {
					samples := p.Network
					if disk {
						samples = p.DiskIO
					}
					for _, v := range samples {
						if v.ID == item.ID {
							if read {
								return v.Read
							}
							return v.Write
						}
					}
					return nil
				}
			}
			out.WriteString("↓ " + accent.Render(historyLine(m.history, width, m.page.Cadence, ceiling, get(true))) + "\n↑ " + muted.Render(historyLine(m.history, width, m.page.Cadence, ceiling, get(false))) + "\n")
		}
	}
	ioGroup("DISK I/O · per device", s.DiskIO, true)
	ioGroup("NETWORK · per interface", s.Network, false)
	out.WriteString("\nGPU  " + measurement(s.GPU) + "\n\n? explains accounting and measurement limits · i opens technical records.\nReservation budgets, their consumers and reasons are in Resources.")
	return out.String()
}
