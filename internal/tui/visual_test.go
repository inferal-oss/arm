package tui

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

func visualDashboard(t *testing.T) *dashboardModel {
	t.Helper()
	m, _ := fixtureDashboard(t)
	m.notice = ""
	now := time.Date(2026, 10, 21, 12, 30, 0, 0, time.UTC)
	m.timeNow = now
	m.lastRefresh = now
	m.timeMonitor = now
	m.data.status.Host = "local-workstation"
	m.data.status.OwnerUsername = "developer"
	measure := func(n float64, unit string) model.Measurement {
		return model.Measurement{Value: &n, Unit: unit, Quality: "reported", At: now}
	}
	resource := func(total, used float64) model.ResourceMeasurement {
		return model.ResourceMeasurement{Total: measure(total, "bytes"), Used: measure(used, "bytes"), Available: measure(total-used, "bytes")}
	}
	const gib = 1024 * 1024 * 1024
	sample := &model.MonitorSnapshot{At: now, CPU: measure(34.8, "percent"), Memory: resource(32*gib, 12.8*gib), Swap: resource(4*gib, .5*gib), Filesystem: resource(1024*gib, 424*gib), GPU: model.Measurement{Unit: "percent"}}
	sample.Filesystem.Filesystem = &model.Filesystem{Type: "apfs", Shared: true}
	for index := 0; index < 12; index++ {
		sample.Cores = append(sample.Cores, model.MonitorCPU{ID: fmt.Sprint("cpu", index), Busy: measure(float64((12+index*6)%85), "percent")})
	}
	sample.DiskIO = []model.MonitorIO{{ID: "disk-0", Name: "Internal SSD", Read: measure(12.3*1024*1024, "bytes/second"), Write: measure(4.5*1024*1024, "bytes/second")}, {ID: "disk-1", Name: "External drive", Read: measure(0, "bytes/second"), Write: measure(0, "bytes/second")}}
	sample.Network = []model.MonitorIO{{ID: "net-0", Name: "Wi-Fi · en0", Read: measure(2.1*1024*1024, "bytes/second"), Write: measure(420*1024, "bytes/second")}, {ID: "net-1", Name: "Ethernet", Read: measure(0, "bytes/second"), Write: measure(0, "bytes/second")}}
	m.page = model.MonitorPage{Latest: sample, Epoch: "preview", Cursor: 60, Cadence: time.Second, StaleAfter: 3 * time.Second, Host: m.data.status.Host}
	for index := 0; index < 60; index++ {
		cpu := 32 + 21*math.Sin(float64(index)/6) + 9*math.Cos(float64(index)/2)
		memory := 12*gib + float64(index)*.015*gib
		memoryAvailable := 32*gib - memory
		free := 610*gib - float64(index)*.17*gib
		zero := 0.0
		diskR := (8 + 6*math.Sin(float64(index)/4)) * 1024 * 1024
		diskW := (3 + 2*math.Cos(float64(index)/5)) * 1024 * 1024
		netR := (1.4 + .8*math.Sin(float64(index)/7)) * 1024 * 1024
		netW := (.4 + .2*math.Cos(float64(index)/6)) * 1024 * 1024
		m.history = append(m.history, model.MonitorPoint{Sequence: int64(index + 1), At: now.Add(time.Duration(index-59) * time.Second), CPU: &cpu, MemoryUsed: &memory, MemoryAvailable: &memoryAvailable, FilesystemAvailable: &free, DiskIO: []model.MonitorHistoryIO{{ID: "disk-0", Read: &diskR, Write: &diskW}, {ID: "disk-1", Read: &zero, Write: &zero}}, Network: []model.MonitorHistoryIO{{ID: "net-0", Read: &netR, Write: &netW}, {ID: "net-1", Read: &zero, Write: &zero}}})
	}
	lease := activeLease("a1b2c3d4e5f60123456789abcdef01234")
	lease.Owner = "1000"
	lease.OwnerUsername = "developer"
	lease.Host = m.data.status.Host
	lease.Request.Reason = "Compile the release binary; keep build artifacts briefly"
	lease.Request.Agent = "build-worker"
	lease.Request.Project = "api-server"
	lease.Request.Resources = model.Resources{CPU: 4, Memory: 4 * gib}
	lease.Expires = new(time.Time)
	*lease.Expires = now.Add(20 * time.Minute)
	lease.Request.Kind = "execution"
	m.data.status.Leases = []model.Lease{lease}
	m.data.status.Resources = []model.ResourceView{{ID: "cpu", Kind: "cpu", Unit: "logical-core-equivalents", BudgetTotal: "16", Reserved: "4", Available: "12", Users: []model.ResourceUse{{Lease: lease.ID, Owner: "1000", OwnerUsername: "developer", Agent: "build-worker", Reason: lease.Request.Reason, Quantity: "4"}}}, {ID: "memory", Kind: "memory", Unit: "bytes", BudgetTotal: "17179869184", Reserved: "4294967296", Available: "12884901888", Users: []model.ResourceUse{{Lease: lease.ID, Owner: "1000", OwnerUsername: "developer", Agent: "build-worker", Reason: "Rust compilation workspace", Quantity: "4294967296"}}}, {ID: "tokens", Kind: "custom", Unit: "tokens", BudgetTotal: "9223372036854775807", Reserved: "0", Available: "9223372036854775807", Custom: &model.PoolView{Pool: model.Pool{Name: "Coordination tokens", Scope: "global"}}}}
	physicalTotal, physicalAvailable := int64(32*gib), int64(19*gib)
	m.data.status.Memory = &model.MemoryBudget{Observation: model.MemoryAvailability{At: now, Total: &physicalTotal, Available: &physicalAvailable}, Capacity: 16 * gib, Reserved: 4 * gib, Available: 12 * gib, StaleAfter: 10 * time.Second}
	m.data.messages = []manager.Message{{ID: "message-preview", Created: now.Add(-20 * time.Second), Sender: "build-worker", Audience: "operator", Body: "Release build finished. Export the binary before cleaning artifacts."}}
	m.data.contacts = []agents.Contact{{ID: "build-worker", Adapter: "generic", Session: "11111111-1111-4111-8111-111111111111", Registration: "preview-registration", Delivery: "poll", CWD: "/workspace/api-server"}}
	m.events = []model.Event{{Sequence: 3, At: now.Add(-8 * time.Second), Type: "lease.renewed", Entity: lease.ID}, {Sequence: 2, At: now.Add(-time.Minute), Type: "storage.created", Entity: "scratch artifacts · 10 GiB"}, {Sequence: 1, At: now.Add(-2 * time.Minute), Type: "lease.created", Entity: lease.ID}}
	m.width = 140
	m.height = 42
	m.resize()
	m.syncViewport()
	return m
}

func TestWindowedDashboardViewportAndFocusAtWideAndCompactSizes(t *testing.T) {
	for _, size := range [][2]int{{160, 46}, {140, 42}, {110, 32}, {100, 30}, {70, 24}, {45, 14}} {
		for view := range viewNames {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], viewNames[view]), func(t *testing.T) {
				m := visualDashboard(t)
				m.width, m.height = size[0], size[1]
				m.view = view
				m.syncViewport()
				screen := ansi.Strip(m.View().Content)
				lines := strings.Split(screen, "\n")
				if len(lines) != m.height {
					t.Fatal("screen escaped viewport", len(lines), m.height)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > m.width {
						t.Fatal("window exceeded viewport width")
					}
				}
				if !strings.Contains(screen, "Local operator") {
					t.Fatal("fixed footer clipped by windows")
				}
				if size[0] >= 110 && !strings.Contains(screen, "LIVE FEED") {
					t.Fatal("side feed window missing")
				}
				if view == 0 && !strings.Contains(screen, "CPU") {
					t.Fatal("CPU window missing")
				}
			})
		}
	}
}
func TestWindowGeometryAndDynamicMemoryLabelsRemainExplicit(t *testing.T) {
	for _, focused := range []bool{false, true} {
		screen := ansi.Strip(window("TEST", strings.Repeat("x", 30), 40, 8, focused, ""))
		lines := strings.Split(screen, "\n")
		if len(lines) != 8 {
			t.Fatal("Lip Gloss window height wrong", len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != 40 {
				t.Fatal("window frame width wrong", ansi.StringWidth(line))
			}
		}
		if focused && !strings.Contains(screen, "╔") {
			t.Fatal("focus has no noncolor cue")
		}
	}
	m := visualDashboard(t)
	m.view = 1
	if text := ansi.Strip(m.resourcesText()); !strings.Contains(text, "To lease") || !strings.Contains(text, "16 GiB") || !strings.Contains(text, "not physical RAM") || !strings.Contains(text, "9223372036854775807 tokens") {
		t.Fatal("budget labels or exact custom counts lost", text)
	}
	if text := ansi.Strip(m.monitorText()); !strings.Contains(text, "32 GiB") || !strings.Contains(text, "DISK I/O") || !strings.Contains(text, "NETWORK") {
		t.Fatal("physical observations/window composition missing", text)
	}
	m.networkSelection = ioSelection{IDs: map[string]bool{"net-1": true}}
	if !strings.Contains(ansi.Strip(m.monitorText()), "1 selected interfaces") {
		t.Fatal("combined custom interface selection not shown")
	}

}

// Optional deterministic visual artifacts use synthetic data, never a user's
// live host measurements, agent paths or session credentials.
func TestWriteWindowedDashboardPreviews(t *testing.T) {
	dir := os.Getenv("ARM_TUI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set ARM_TUI_PREVIEW_DIR for deterministic visual review artifacts")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("preview path must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"monitor-wide", "disk-picker", "network-picker", "io-none", "io-missing", "monitor-compact", "resources", "leases", "messages", "activity", "host", "record", "release-review", "message-review", "connecting", "disconnected"} {
		m := visualDashboard(t)
		switch name {
		case "disk-picker":
			m.openIOPicker(true)
		case "network-picker":
			m.openIOPicker(false)
		case "io-none":
			m.diskSelection = ioSelection{}
			m.networkSelection = ioSelection{}
		case "io-missing":
			m.diskSelection = ioSelection{IDs: map[string]bool{"missing-disk": true}}
			m.openIOPicker(true)
		case "monitor-compact":
			m.width = 70
			m.height = 28
		case "resources":
			m.view = 1
		case "leases":
			m.view = 2
		case "messages":
			m.view = 3
		case "activity":
			m.view = 4
		case "host":
			m.view = 5
		case "record":
			m.record, _ = newRecord(m.data.status, "Host status")
		case "release-review":
			m.view = 2
			lease := m.data.status.Leases[0]
			lease.Request.Kind = "storage"
			lease.Artifact = &model.Artifact{Path: "/workspace/build/target", Policy: "cleanup"}
			m.detail = &lease
			m.startReview("release")
		case "message-review":
			m.view = 3
			m.composer = &messageComposer{review: true, request: manager.MessageRequest{Key: "preview-key", Audience: "build-worker", Targets: m.data.contacts, Body: "Please export the release binary, then release the managed build directory."}}
		case "connecting":
			m.data = snapshot{}
			m.page = model.MonitorPage{}
			m.history = nil
			m.events = nil
			m.connected = false
			m.connecting = true
		case "disconnected":
			m.connected = false
			m.notice = "Backend disconnected. Reconnecting; pending actions will not be retried."
		}
		m.syncViewport()
		raw := m.View().Content
		if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(ansi.Strip(raw)), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
