package tui

import (
	"fmt"
	"getarm.org/internal/model"
	"strings"
)

func storagePoolLines(p *model.StoragePoolView) []string {
	l := p.Location
	lines := []string{"Storage pool: " + safe(p.Name), "Path: " + safe(l.Path), "Mount: " + safe(l.Mount) + " · " + safe(l.Filesystem), "Source: " + safe(l.Source), "Backing: " + safe(l.Backing)}
	if p.Tier != "" {
		lines = append(lines, "Tier: "+safe(p.Tier)+" (operator label)")
	}
	for _, d := range l.Devices {
		media := "media unknown"
		if d.SolidState != nil {
			if *d.SolidState {
				media = "non-rotational (reported)"
			} else {
				media = "rotational (reported)"
			}
		}
		lines = append(lines, safe(strings.Join([]string{d.Device, d.Model, d.Transport, media}, " · ")))
	}
	if b := p.Performance; b != nil {
		lines = append(lines, fmt.Sprintf("Write %.1f MiB/s · Read %.1f MiB/s", b.WriteBytesPerSecond/(1<<20), b.ReadBytesPerSecond/(1<<20)), fmt.Sprintf("QD1 random read %.0f IOPS · mean %.3f ms", b.RandomReadIOPS, float64(b.RandomReadLatencyNS)/1e6), "Measured: "+b.At.Local().Format("2006-01-02 15:04:05"), "Method: "+safe(b.Method))
		if p.PerformanceStale {
			lines = append(lines, "Performance stale · remeasure before relying on ranking")
		}
	} else {
		lines = append(lines, "Performance unmeasured")
	}
	if !p.Enabled {
		lines = append(lines, "Location disabled · existing promises retained")
	}
	return append(lines, "Shared backing views overlap; do not sum available bytes.")
}
