package observe

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/model"
)

func TestNodeMetadataAllowlistExactInventoryAndIndependentFreshness(t *testing.T) {
	now := time.Now().UTC()
	total := int64(9007199254740993)
	status := model.Status{Host: "stable-authority", Owner: "private-owner-sentinel", Capabilities: []string{"reported-feature"}, Memory: &model.MemoryBudget{Observation: model.MemoryAvailability{At: now, Total: &total, Detail: "private-memory-sentinel", Scope: "private-scope-sentinel"}, StaleAfter: 3 * time.Second}, Disk: &model.DiskBudget{Observation: model.DiskAvailability{At: now.Add(-time.Hour), Total: &total, Path: "/private/path-sentinel", Volume: "private-fsid-sentinel", Pool: "private-container-sentinel", Detail: "private-disk-sentinel"}, Age: time.Hour, Stale: true, StaleAfter: 3 * time.Second}}
	n := NodeMetadata(status)
	if n.Host != status.Host || n.SchemaVersion != 1 || n.Platform.OS != runtime.GOOS || n.Platform.Architecture != runtime.GOARCH || n.Build.Version != buildinfo.Version || n.Build.GoVersion != runtime.Version() || n.Hostname == "" || n.Platform.KernelRelease == "" || n.Inventory.LogicalCPUs < 1 {
		t.Fatal(n)
	}
	if !n.Inventory.Memory.Known || n.Inventory.Memory.Stale || *n.Inventory.Memory.Total != total || n.Inventory.StateFilesystem.Known || !n.Inventory.StateFilesystem.Stale || *n.Inventory.StateFilesystem.Total != total || !n.ReportedAt.After(*n.Inventory.StateFilesystem.ObservedAt) {
		t.Fatal(n.Inventory)
	}
	raw, err := json.Marshal(n)
	if err != nil || strings.Contains(string(raw), "sentinel") || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal(string(raw), err)
	}
	// Returned pointers do not expose mutable manager/cache evidence.
	*n.Inventory.Memory.Total = 1
	if total != 9007199254740993 {
		t.Fatal("metadata aliases source inventory")
	}
}

func TestNodeMetadataUnknownFutureInvalidInventoryAndTextBounds(t *testing.T) {
	for _, value := range []string{"bad\nvalue", strings.Repeat("x", 254), string([]byte{0xff})} {
		if metadataText(value, 253) != "" {
			t.Fatal("unsafe metadata text accepted")
		}
	}
	n := NodeMetadata(model.Status{})
	if n.Inventory.Memory.Known || n.Inventory.Memory.Total != nil || !n.Inventory.Memory.Stale || n.Inventory.StateFilesystem.Known || n.Inventory.StateFilesystem.ObservedAt != nil {
		t.Fatal(n.Inventory)
	}
	total := int64(10)
	for _, v := range []model.NodeInventorySize{nodeSize(&total, time.Now().Add(time.Hour), 0, time.Second, false), nodeSize(&total, time.Now(), -1, time.Second, false), nodeSize(&total, time.Now(), 0, 0, false)} {
		if v.Known || !v.Stale {
			t.Fatal(v)
		}
	}
	caps := make([]string, 200)
	for i := range caps {
		caps[i] = "feature"
	}
	n = NodeMetadata(model.Status{Capabilities: caps})
	if len(n.Capabilities) != 128 {
		t.Fatal(len(n.Capabilities))
	}
	if nodeKernelRelease() != n.Platform.KernelRelease {
		t.Fatal("cached kernel release changed")
	}
}
