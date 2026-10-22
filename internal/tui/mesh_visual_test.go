package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/model"
)

func meshVisualDashboard(t *testing.T, variant string) *dashboardModel {
	t.Helper()
	m := visualDashboard(t)
	m.view = 6
	source, target := strings.Repeat("1", 32), strings.Repeat("2", 32)
	m.data.status.Host = source
	memory, disk := int64(32<<30), int64(1<<40)
	metadata := &model.NodeMetadata{SchemaVersion: 1, Host: target, Hostname: "build-node", Platform: model.NodePlatform{OS: "linux", Architecture: "arm64", KernelRelease: "6.12"}, Build: model.NodeBuild{Version: "development", GoVersion: "go1.27"}, Inventory: model.NodeInventory{LogicalCPUs: 8, Memory: model.NodeInventorySize{Known: true, Total: &memory, ObservedAt: &m.timeNow}, StateFilesystem: model.NodeInventorySize{Known: true, Total: &disk, ObservedAt: &m.timeNow}}, ReportedAt: m.timeNow, Capabilities: []string{"local-resource-leases", "native-dashboard", "mesh"}}
	grant := &model.NodeGrant{Permissions: []string{"observe-host", "observe-own", "lease-own"}, Limits: model.Resources{CPU: 4, Memory: 4 << 30, Disk: 8 << 30}, MaxTTL: 10 * time.Minute}
	localMetadata := *metadata
	localMetadata.Host = source
	localMetadata.Hostname = "workstation"
	localMetadata.Platform.OS = "darwin"
	local := model.NodeEntry{Host: source, Name: "workstation", Local: true, Health: "local", Trust: model.NodeTrust{State: "local", Account: &model.NodeExecutionAccount{UID: "1000", Username: "developer", Source: "authenticated-local"}}, Metadata: &localMetadata}
	peer := model.NodeEntry{Host: target, Name: "build-node", Health: "reachable", Trust: model.NodeTrust{State: "active", Account: &model.NodeExecutionAccount{UID: "1000", Username: "builder", Source: "paired-identity"}, Inbound: &model.NodeGrant{}, Pair: strings.Repeat("3", 64), Revision: 1, Fingerprint: strings.Repeat("4", 64), Address: "192.0.2.20:48520"}, Metadata: metadata, Outbound: &model.NodeAccessScope{Source: source, Target: target, Pair: strings.Repeat("3", 64), Revision: 1, Grant: *grant, Observed: m.timeNow}, MetadataFreshness: model.NodeReadFreshness{Known: true, Received: &m.timeNow}, ScopeFreshness: model.NodeReadFreshness{Known: true, Received: &m.timeNow}}
	m.meshUI = &meshDashboard{mode: "nodes", registry: model.NodeRegistry{Source: source, MeshState: "enabled", Nodes: []model.NodeEntry{local, peer}, At: m.timeNow}}
	switch variant {
	case "mesh-node-detail":
		m.meshUI.mode = "node"
		m.meshUI.node = &peer
	case "mesh-connect":
		m.meshUI.form = newMeshForm()
		m.meshUI.form.fields[0].value = "build-node.example"
		m.meshUI.form.fields[1].value = "Coordinate release builds with explicit resource ceilings"
	case "mesh-review", "mesh-review-end", "mesh-recovery-review":
		review := meshTrustReview{Operation: "connect-direct-peers", Request: meshops.ConnectRequest{Destination: "build-node.example", Reason: "Share limited build capacity", Forward: mesh.Grant{Permissions: grant.Permissions, Limits: grant.Limits, MaxTTL: grant.MaxTTL}}, Warning: meshops.PreparationNotice}
		review.LocalPrepared = meshReviewPrepared{Identity: &meshReviewIdentity{Host: source, Name: "workstation", Owner: "1000", Username: "developer", Address: "192.0.2.10:48510", Certificate: strings.Repeat("5", 64), PublicKey: strings.Repeat("6", 64), Expires: m.timeNow.Add(24 * time.Hour)}, Listen: "192.0.2.10:48510", Expires: m.timeNow.Add(time.Minute)}
		review.RemotePrepared = meshReviewPrepared{Identity: &meshReviewIdentity{Host: target, Name: "build-node", Owner: "1000", Username: "builder", Address: "192.0.2.20:48520", Certificate: strings.Repeat("7", 64), PublicKey: strings.Repeat("8", 64), Expires: m.timeNow.Add(24 * time.Hour)}, Listen: "192.0.2.20:48520", Expires: m.timeNow.Add(time.Minute)}
		review.SSH.Account = "builder"
		review.SSH.HostKey = "SHA256:synthetic-review-key"
		raw, _ := json.Marshal(review)
		if variant == "mesh-recovery-review" {
			raw, _ = json.Marshal(map[string]any{"operation": "verify-existing-connection", "original_key": "original-connection-receipt", "pair": strings.Repeat("3", 64), "local": map[string]any{"identity": review.LocalPrepared.Identity}, "remote": map[string]any{"identity": review.RemotePrepared.Identity}, "local_listen": review.LocalPrepared.Listen, "remote_listen": review.RemotePrepared.Listen, "local_revision": 2, "remote_revision": 3, "source_permissions_on_target": review.Request.Forward, "target_permissions_on_source": review.Request.Reverse, "effect": "Read-only recovery verifies the existing enabled enrollment with fresh mTLS. It does not change trust/listeners, start backends, or retry execution."})
		}
		m.meshUI.mode = "operation"
		m.meshUI.operation = &meshops.Operation{Key: strings.Repeat("9", 32), Kind: "connect", Phase: "awaiting_review", Revision: 3, WorkerLive: true, Review: raw, ReviewRevision: 3, ReviewHash: strings.Repeat("a", 64), ReviewExpires: m.timeNow.Add(time.Minute), Updated: m.timeNow}
		m.meshDecision(true)
	case "mesh-remote":
		m.nodeUI = &nodeDashboard{selection: model.NodeSelection{Host: target, Source: source}, connected: true, metadata: metadata, lastRead: m.timeNow, context: model.NodeContextReport{ReadOnly: true, Context: model.NodeContext{Host: target, Source: source}, AccessScope: peer.Outbound}, summary: nodeSummary{Host: target, Resources: []nodeResourceSummary{{Kind: "cpu", Unit: "logical-core-equivalents", Total: "8", Reserved: "2", Available: "6"}, {Kind: "memory", Unit: "bytes", Total: "17179869184", Reserved: "4294967296", Available: "12884901888"}, {Kind: "disk", Unit: "bytes", Total: "549755813888", Reserved: "8589934592", Available: "541165879296"}}, Memory: json.RawMessage(`{"estimated_available_bytes":17179869184,"stale":false,"stale_after_ns":4000000000}`), Disk: json.RawMessage(`{"known":true,"stale":false,"stale_after_ns":4000000000}`)}}
		m.view = 1
	case "mesh-45":
		m.width = 45
		m.height = 24
	}
	m.syncViewport()
	if variant == "mesh-review-end" {
		m.viewport.GotoBottom()
	}
	return m
}

func TestMeshWindowsAndReviewedControlsFitViewport(t *testing.T) {
	for _, variant := range []string{"mesh-nodes", "mesh-node-detail", "mesh-connect", "mesh-review", "mesh-recovery-review", "mesh-remote"} {
		for _, size := range [][2]int{{45, 14}, {70, 24}, {140, 42}} {
			m := meshVisualDashboard(t, variant)
			m.width, m.height = size[0], size[1]
			m.syncViewport()
			text := ansi.Strip(m.View().Content)
			lines := strings.Split(text, "\n")
			if len(lines) != m.height {
				t.Fatal("Mesh escaped height", variant, size)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("Mesh escaped width", variant, size)
				}
			}
			if !strings.Contains(lines[1], "Mesh") {
				t.Fatal("seventh tab hidden", variant, size)
			}
			if variant == "mesh-review" && (!strings.Contains(lines[m.height-3], "[n] Keep") || !strings.Contains(lines[m.height-3], "[y] Confirm")) {
				t.Fatal("trusted default-keep controls clipped", size, text)
			}
		}
	}
}

func TestWriteMeshPreviews(t *testing.T) {
	dir := os.Getenv("ARM_TUI_MESH_PREVIEW_DIR")
	if dir == "" {
		t.Skip("optional deterministic synthetic artifacts")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute output directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"mesh-nodes", "mesh-node-detail", "mesh-connect", "mesh-review", "mesh-review-end", "mesh-recovery-review", "mesh-remote", "mesh-45"} {
		m := meshVisualDashboard(t, variant)
		raw := profileView(t, m.View().Content, colorprofile.TrueColor)
		for ext, content := range map[string]string{"ansi": raw, "txt": ansi.Strip(raw)} {
			if err := os.WriteFile(filepath.Join(dir, variant+"."+ext), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
