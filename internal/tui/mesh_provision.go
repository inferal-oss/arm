package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
)

type meshProvisionPrepareRequest struct {
	Key       string                 `json:"key"`
	Provision meshops.ProvisionDraft `json:"provision"`
}

func newMeshProvisionForm() *meshForm {
	return &meshForm{provisionInstall: true, fields: []meshField{{label: "SSH destination or @reviewed-connector"}, {label: "Reason for installing ARM"}}}
}
func (f *meshForm) provisionRequest() (meshIntent, error) {
	d := meshops.ProvisionDraft{ConnectionKey: local.ID(), Destination: strings.TrimSpace(f.fields[0].value), Reason: f.fields[1].value}
	if strings.HasPrefix(d.Destination, "@") {
		d.Connector = strings.TrimPrefix(d.Destination, "@")
		d.Destination = ""
	}
	key := meshops.ProvisionKey(d.ConnectionKey)
	if e := d.Validate(key); e != nil {
		return meshIntent{}, e
	}
	return newMeshIntent("mesh_operator_provision_prepare", key, meshProvisionPrepareRequest{Key: key, Provision: d})
}
func parseMeshProvisionReview(raw json.RawMessage) (meshops.ProvisionPlan, bool) {
	var p meshops.ProvisionPlan
	if mesh.Decode(raw, &p) != nil || p.Validate() != nil {
		return p, false
	}
	return p, true
}
func meshProvisionOperationBound(op meshops.Operation) bool {
	if op.Kind != "provision" || !mesh.ValidKey(op.Key) || op.Revision <= 0 || len(op.Targets) != 1 || !strings.HasPrefix(op.Targets[0], "ssh:") {
		return false
	}
	if len(op.Review) == 0 {
		return !op.Approved && !op.ProvisionConnectionReady && op.Phase == "preparing"
	}
	if op.ReviewRevision <= 0 || op.ReviewRevision > op.Revision {
		return false
	}
	p, ok := parseMeshProvisionReview(op.Review)
	if !ok || p.Key != op.Key || op.Targets[0] != "ssh:"+p.Request.Destination {
		return false
	}
	digest, e := mesh.ReviewDigest(p)
	return e == nil && digest == op.ReviewHash
}
func meshProvisionConfirmed(op meshops.Operation) bool {
	if !meshProvisionOperationBound(op) || !op.ProvisionConnectionReady || !op.Approved || op.WorkerLive || op.Error != nil || op.Phase != "completed" {
		return false
	}
	p, _ := parseMeshProvisionReview(op.Review)
	var result meshops.ProvisionResult
	return mesh.Decode(op.Result, &result) == nil && meshops.ValidateProvisionResultForPlan(p, result) == nil
}
func meshProvisionSameReview(before, after meshops.Operation) bool {
	if !meshProvisionOperationBound(before) || !meshProvisionOperationBound(after) || after.Revision < before.Revision || before.Key != after.Key || len(before.Targets) != 1 || before.Targets[0] != after.Targets[0] {
		return false
	}
	if len(before.Review) == 0 {
		return before.Phase == "preparing" && !before.Approved
	}
	old, ok := parseMeshProvisionReview(before.Review)
	fresh, valid := parseMeshProvisionReview(after.Review)
	if !ok || !valid {
		return false
	}
	a, _ := mesh.ReviewDigest(old)
	b, _ := mesh.ReviewDigest(fresh)
	return a == b && before.ReviewHash == after.ReviewHash && (before.ReviewRevision == after.ReviewRevision || before.Phase == "preparing" && !before.Approved && !before.LookupOnly && before.ReviewRevision < after.ReviewRevision && after.ReviewRevision <= after.Revision)
}
func meshProvisionReviewSummary(raw json.RawMessage) (string, bool) {
	p, ok := parseMeshProvisionReview(raw)
	if !ok {
		return "Exact installation review unavailable. Keep this host unchanged; inspect the original record.", false
	}
	return fmt.Sprintf("INSTALL ARM · NO PEER TRUST\nSSH destination %s\nAccount %s\nSSH host key %s\nPlatform %s / %s\nPayload %s · %s\nExecutable %s\nState home %s\n\n%s\n\nWHY\n%s", safe(p.Request.Destination), safe(p.Request.SSH.Account), safe(p.Request.SSH.HostKey), safe(p.Request.Transfer.OS), safe(p.Request.Transfer.Arch), safe(p.Request.Transfer.SHA256), byteCount(fmt.Sprint(p.Request.Transfer.Size)), safe(p.Binary), safe(p.Request.Home), safe(p.Effect), safe(p.Request.Transfer.Reason)), true
}
func meshProvisionReviewWindows(raw json.RawMessage, width int) string {
	p, ok := parseMeshProvisionReview(raw)
	if !ok {
		return meshStopWindow("INSTALLATION REVIEW UNAVAILABLE", "Keep this host unchanged. The exact original plan could not be read.", width, "warning")
	}
	account := fmt.Sprintf("Destination %s\nVerified account %s · UID %s\nKnown-hosts SSH key %s", safe(p.Request.Destination), safe(p.Request.SSH.Account), safe(p.Request.Transfer.ExpectedUID), safe(p.Request.SSH.HostKey))
	image := fmt.Sprintf("%s / %s · %s\nSHA-256 %s\nInstall ONLY %s\nARM home %s", safe(p.Request.Transfer.OS), safe(p.Request.Transfer.Arch), byteCount(fmt.Sprint(p.Request.Transfer.Size)), safe(p.Request.Transfer.SHA256), safe(p.Binary), safe(p.Request.Home))
	startup := fmt.Sprintf("Start this exact user-owned ARM home with %g CPU budget.\nLocal API listener %s\nUse existing saved policy when present. No PATH, system service, root or host shutdown changes.\nExisting user data, configuration, identities and history are preserved.\nNo mesh trust or resource grants are created; review the separate connection afterwards.", p.Request.Startup.Resources.CPU, safe(p.Request.Startup.Listen))
	catalog := "No onward catalog is included; this node cannot automatically provide other platforms from this installation."
	if p.Request.Seed != nil {
		catalog = "Retain the reviewed four-platform companion catalog for onward provisioning. This grants no trust.\nManifest SHA-256 " + safe(p.Request.Seed.Digest)
		for _, a := range p.Request.Seed.Manifest.Artifacts {
			catalog += "\n" + safe(a.OS) + " / " + safe(a.Arch) + " · " + byteCount(fmt.Sprint(a.CompressedSize)) + " compressed\nSHA-256 " + safe(a.SHA256)
		}
	}
	return strings.Join([]string{meshStopWindow("VERIFIED SSH ACCOUNT", account, width, "warning"), meshStopWindow("TRUSTED INSTALLATION", image, width, "warning"), meshStopWindow("STARTUP · PRESERVED DATA", startup, width, ""), meshStopWindow("COMPANION CATALOG", catalog, width, ""), meshStopWindow("WHY", safe(p.Request.Transfer.Reason), width, "")}, "\n\n")
}
func (m *dashboardModel) meshConnectProvisioned() tea.Cmd {
	ui := m.meshUI
	if ui == nil || ui.operation == nil || !meshProvisionConfirmed(*ui.operation) {
		if ui != nil {
			ui.err = "A completely verified original installation is required before a separate connection review."
		}
		return nil
	}
	p, _ := parseMeshProvisionReview(ui.operation.Review)
	for _, known := range ui.operations {
		if known.Key == p.Request.ConnectionKey {
			if known.Kind != "connect" || len(known.Targets) != 1 || known.Targets[0] != "ssh:"+p.Request.Destination {
				ui.err = "The original linked connection key has a different recorded target. Inspect its exact record; no new review was prepared."
				return nil
			}
			return m.readMesh("provision-connection", p.Request.ConnectionKey)
		}
	}
	f := newMeshForm()
	f.provisionSourceKey = ui.operation.Key
	f.provisionConnectionKey = p.Request.ConnectionKey
	f.fields[0].label = "Original installed destination (read-only)"
	f.fields[0].value = p.Request.Destination
	f.fields = f.fields[:18]
	ui.form = f
	ui.err = ""
	m.viewport.GotoTop()
	return nil
}
