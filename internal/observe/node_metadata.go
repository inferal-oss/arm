package observe

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

func metadataText(s string, limit int) string {
	if !utf8.ValidString(s) || len(s) > limit || strings.ContainsFunc(s, unicode.IsControl) {
		return ""
	}
	return s
}

// NodeMetadata reads only bounded native system facts and precollected budget
// observations. It never shells out, reads environment variables, or includes
// raw collector detail, filesystem identity/path, owner or agent data.
func NodeMetadata(status model.Status) model.NodeMetadata {
	n := model.NodeMetadata{SchemaVersion: 1, Host: status.Host, Platform: model.NodePlatform{OS: runtime.GOOS, Architecture: runtime.GOARCH}, Build: model.NodeBuild{Version: buildinfo.Version, GoVersion: runtime.Version()}, Inventory: model.NodeInventory{LogicalCPUs: runtime.NumCPU()}, ReportedAt: time.Now().UTC(), Capabilities: []string{}, Unavailable: []string{}}
	if hostname, err := os.Hostname(); err == nil {
		n.Hostname = metadataText(hostname, 253)
	}
	if n.Hostname == "" {
		n.Unavailable = append(n.Unavailable, "hostname")
	}
	n.Platform.KernelRelease = nodeKernelRelease()
	if n.Platform.KernelRelease == "" {
		n.Unavailable = append(n.Unavailable, "kernel_release")
	}
	// Capabilities are build/authority declarations, not permissions. Keep this
	// reported list bounded even if a corrupt internal caller supplies more.
	for _, capability := range status.Capabilities {
		if len(n.Capabilities) == 128 {
			break
		}
		if value := metadataText(capability, 128); value != "" {
			n.Capabilities = append(n.Capabilities, value)
		}
	}
	n.Inventory.Memory = unknownNodeSize()
	if b := status.Memory; b != nil {
		n.Inventory.Memory = nodeSize(b.Observation.Total, b.Observation.At, b.Age, b.StaleAfter, b.Stale)
	}
	n.Inventory.StateFilesystem = unknownNodeSize()
	if b := status.Disk; b != nil {
		n.Inventory.StateFilesystem = nodeSize(b.Observation.Total, b.Observation.At, b.Age, b.StaleAfter, b.Stale)
	}
	return n
}

func unknownNodeSize() model.NodeInventorySize { return model.NodeInventorySize{Stale: true} }
func nodeSize(total *int64, at time.Time, age, after time.Duration, stale bool) model.NodeInventorySize {
	v := model.NodeInventorySize{Stale: stale, Age: age, StaleAfter: after}
	if !at.IsZero() {
		stamp := at
		v.ObservedAt = &stamp
	}
	if total != nil && *total > 0 {
		value := *total
		v.Total = &value
	}
	// A fresh response never launders old/future/unknown source observations.
	v.Stale = v.Stale || at.IsZero() || age < 0 || after <= 0 || age > after || time.Since(at) < 0 || time.Since(at) > after
	v.Known = v.Total != nil && !v.Stale
	return v
}

var nodeKernelOnce sync.Once
var nodeKernel string

func nodeKernelRelease() string {
	nodeKernelOnce.Do(func() {
		var system unix.Utsname
		if err := unix.Uname(&system); err == nil {
			nodeKernel = metadataText(unix.ByteSliceToString(system.Release[:]), 256)
		}
	})
	return nodeKernel
}
