package tui

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/meshops"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

type meshStopReview = meshops.StopReview

func meshStopPolicy(policy model.ShutdownRequest) string {
	if policy.Force && policy.Wait || policy.Timeout < 0 || policy.Timeout > 0 && !policy.Wait {
		return "Unrecognized stop policy"
	}
	if policy.Force {
		return "Reclaim ARM-owned work, then stop"
	}
	if policy.Wait {
		if policy.Timeout > 0 {
			return "Wait; reclaim ARM-owned work after " + reviewDuration(policy.Timeout)
		}
		return "Wait for existing work, then stop"
	}
	return "Refuse while work or cleanup remains"
}

// This is a display gate, never current grant/admission authority. The concrete
// destination contract validates the plan and rechecks it before any effect.
func parseMeshStopReview(raw json.RawMessage) (meshStopReview, bool) {
	var review meshStopReview
	if json.Unmarshal(raw, &review) != nil {
		return review, false
	}
	err := review.Validate()
	return review, err == nil && review.Plan.Impact.Complete && len(review.Plan.Impact.Blockers) == 0 && review.CompletionCheck.Available && (review.Plan.Policy.Wait || review.Plan.Policy.Force || review.Plan.Impact.Outstanding == 0)
}

func meshStopReviewSummary(raw json.RawMessage) (string, bool) {
	r, valid := parseMeshStopReview(raw)
	p := r.Plan
	if r.Operation != "stop-arm-node" {
		return "Exact node-stop review is unavailable. Read the complete record; approval is unavailable.", false
	}
	account := "UID " + safe(p.Target.Owner)
	if r.OwnerUsername != "" {
		account = safe(r.OwnerUsername) + " (" + account + ")"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "STOP THIS ARM BACKEND\nThe host remains running; all connections to this authority are affected.\n\nExact host: %s\nRun as: %s\nARM home: %s\nBackend generation: %s\nDesired-state revision: %d\n\nACCESS USED\nSource host: %s\nExact enrollment: %s\nDestination grant revision: %d\n\nPOLICY\n%s\n\n", safe(p.Target.Host), account, safe(p.Target.Home), safe(p.Target.Generation), p.DesiredRevision, safe(p.Actor.Host), safe(p.Actor.Pair), p.Actor.GrantRevision, meshStopPolicy(p.Policy))
	if !p.Impact.Complete {
		out.WriteString("INCOMPLETE CENSUS · cannot be treated as an empty backend.\n")
	} else {
		fmt.Fprintf(&out, "Outstanding obligations: %d · affected peers: %d\n", p.Impact.Outstanding, len(p.Impact.Peers))
	}
	for _, b := range p.Impact.Blockers {
		fmt.Fprintf(&out, "BLOCKED · %s · %s: %s\n", safe(b.Class), safe(b.ID), safe(b.Detail))
	}
	if !r.CompletionCheck.Available {
		out.WriteString("Positive original-generation completion observer unavailable.\n")
	}
	out.WriteString(safe(r.CompletionCheck.Detail) + "\n" + safe(p.Supervisor) + "\n\nWHY\n" + safe(p.Reason) + "\n")
	if !p.Policy.Wait && !p.Policy.Force && p.Impact.Outstanding > 0 {
		out.WriteString("\nExisting work prevents the default stop. Keep ARM running, then prepare a new Wait or ARM-owned reclamation review.\n")
	}
	if !valid {
		out.WriteString("\nExact plan, complete scope or usable completion proof is missing; approval is unavailable.\n")
	}
	return out.String(), valid
}

func (m *dashboardModel) meshStopPaneWidth() int {
	if m.meshModal() {
		// This is the actual modal viewport, not the wider background pane.
		return max(16, min(m.mainWidth(), 88)-4)
	}
	return max(16, m.mainWidth()-8)
}

func meshStopWindow(title, text string, width int, tone string) string {
	return window(title, ansi.Wrap(text, max(1, width-4), ""), width, 0, false, tone)
}

func meshStopReviewWindows(raw json.RawMessage, width int) string {
	r, valid := parseMeshStopReview(raw)
	p := r.Plan
	width = max(16, width)
	if r.Operation != "stop-arm-node" {
		text, _ := meshStopReviewSummary(raw)
		return meshStopWindow("STOP REVIEW UNAVAILABLE", text, width, "warning")
	}
	account := "UID " + safe(p.Target.Owner)
	if r.OwnerUsername != "" {
		account = safe(r.OwnerUsername) + " (" + account + ")"
	}
	target := fmt.Sprintf("Host %s\nRun as %s\nHome %s\nGeneration %s\nDesired state revision %d", safe(p.Target.Host), account, safe(p.Target.Home), safe(p.Target.Generation), p.DesiredRevision)
	actor := fmt.Sprintf("Source %s\nEnrollment %s\nGrant revision %d\nSeparate destination-granted node-stop permission", safe(p.Actor.Host), safe(p.Actor.Pair), p.Actor.GrantRevision)
	var sections []string
	if width >= 100 {
		left := (width - 1) / 2
		sections = append(sections, lipgloss.JoinHorizontal(lipgloss.Top, meshStopWindow("EXACT BACKEND", target, left, "blue"), " ", meshStopWindow("ACCESS USED", actor, width-left-1, "blue")))
	} else {
		sections = append(sections, meshStopWindow("EXACT BACKEND", target, width, "blue"), meshStopWindow("ACCESS USED", actor, width, "blue"))
	}
	policyText := meshStopPolicy(p.Policy) + "\n" + safe(p.Supervisor)
	if !p.Policy.Wait && !p.Policy.Force && p.Impact.Outstanding > 0 {
		policyText += "\nExisting work prevents this default stop. Keep running and prepare a new policy."
	}
	sections = append(sections, meshStopWindow("WHAT WILL HAPPEN", policyText, width, "warning"))
	impact := fmt.Sprintf("%d outstanding obligations · %d affected peers", p.Impact.Outstanding, len(p.Impact.Peers))
	if !p.Impact.Complete {
		impact = "INCOMPLETE CENSUS · approval unavailable; unknown is not zero work."
	}
	if len(p.Impact.Obligations) > 0 {
		rows := make([][]string, 0, len(p.Impact.Obligations))
		headers := []string{"Work", "Who / why", "State", "Revision"}
		if width < 80 {
			headers = []string{"Work", "Who / why"}
		}
		for _, item := range p.Impact.Obligations {
			work := safe(item.ID) + "\n" + safe(item.Kind)
			if width < 80 {
				rows = append(rows, []string{work + "\n" + safe(item.State) + " · rev " + strconv.FormatInt(item.Revision, 10), meshStopLeasePreview(item)})
			} else {
				rows = append(rows, []string{work, meshStopLeasePreview(item), safe(item.State), strconv.FormatInt(item.Revision, 10)})
			}
		}
		grid := table.New().Headers(headers...).Rows(rows...).Width(max(12, width-4)).Wrap(true).Border(lipgloss.NormalBorder()).BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).BorderColumn(false).BorderRow(false).BorderStyle(lipgloss.NewStyle().Foreground(colorLine)).StyleFunc(func(row, col int) lipgloss.Style {
			style := panelStyle.Padding(0, 1)
			if row == table.HeaderRow {
				return style.Bold(true).Foreground(colorWarning)
			}
			if row%2 == 1 {
				return style.Background(lipgloss.Color("#182229"))
			}
			return style
		}).String()
		impact += "\n\n" + grid
	}
	for _, b := range p.Impact.Blockers {
		impact += "\nBLOCKED · " + safe(b.Class) + " · " + safe(b.ID) + ": " + safe(b.Detail)
	}
	if len(p.Impact.Peers) > 0 {
		impact += "\n\nAffected peers\n" + safe(strings.Join(p.Impact.Peers, "\n"))
	}
	sections = append(sections, meshStopWindow("AFFECTED WORK AND CONNECTIONS", impact, width, "warning"))
	complete := safe(r.CompletionCheck.Detail)
	if !r.CompletionCheck.Available {
		complete = "Positive original-generation completion observer unavailable.\n" + complete
	}
	if !valid {
		complete += "\nApproval unavailable: inspect the complete exact plan and blockers."
	}
	sections = append(sections, meshStopWindow("COMPLETION CHECK", complete, width, "warning"), meshStopWindow("OPERATOR REASON", safe(p.Reason), width, ""))
	sections = append(sections, fmt.Sprintf("Review expires %s · retained history %d/%d\n[i] Full immutable record and identity fingerprints. Default: keep running.", p.Expires.Local().Format(time.RFC1123), r.HistoryUsed, r.HistoryCapacity))
	return strings.Join(sections, "\n\n")
}

func meshStopAvailability(n *model.NodeEntry) (bool, string) {
	if n == nil || n.Local {
		return false, "Use the local ARM stop workflow for this backend."
	}
	if n.Trust.State != "active" {
		return false, "Reconnect this exact node before reviewing a stop."
	}
	if !n.ScopeFreshness.Known || n.ScopeFreshness.Stale {
		return false, "Refresh this node to read current stop support and permissions."
	}
	scope := n.Outbound
	if scope == nil || scope.Target != n.Host {
		return false, "This node has not reported current stop permissions for this source."
	}
	granted := false
	for _, permission := range scope.Grant.Permissions {
		if permission == "node-stop" {
			granted = true
		}
	}
	if !granted {
		return false, "This node has not granted this source permission to stop ARM."
	}
	if scope.NodeStop == nil {
		return false, "This ARM backend does not report usable remote stop support."
	}
	if !scope.NodeStop.Supported {
		if scope.NodeStop.Reason != "" {
			return false, safe(scope.NodeStop.Reason)
		}
		return false, "This ARM backend cannot currently confirm a remote stop."
	}
	return true, "Prepare a current review of this exact ARM backend."
}

func newMeshStopForm(host string) *meshForm {
	return &meshForm{stopHost: host, fields: []meshField{{"Stop policy", "refuse", []string{"refuse", "wait", "force"}}, {"Reclaim after (whole seconds; Wait only, blank: none)", "", nil}, {"Reason", "", nil}}}
}

func (f *meshForm) stopRequest() (meshIntent, error) {
	if len(f.fields) != 3 || !validMeshStopHost(f.stopHost) {
		return meshIntent{}, fmt.Errorf("exact stop target and draft required")
	}
	reason := f.fields[2].value
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return meshIntent{}, fmt.Errorf("explain the stop in at most4096 bytes")
	}
	mode := f.fields[0].value
	if mode != "refuse" && mode != "wait" && mode != "force" {
		return meshIntent{}, fmt.Errorf("choose a stop policy")
	}
	timeout := strings.TrimSpace(f.fields[1].value)
	policy := model.ShutdownRequest{Force: mode == "force", Wait: mode == "wait"}
	if mode != "wait" && timeout != "" && timeout != "0" {
		return meshIntent{}, fmt.Errorf("a reclaim timeout is available only with Wait for work")
	}
	if mode == "wait" && timeout != "" && timeout != "0" {
		if len(timeout) > 10 || timeout[0] == '0' || strings.Trim(timeout, "0123456789") != "" {
			return meshIntent{}, fmt.Errorf("use positive whole seconds or leave timeout empty")
		}
		seconds, err := strconv.ParseUint(timeout, 10, 64)
		if err != nil || seconds > uint64((time.Duration(1<<63-1))/time.Second) {
			return meshIntent{}, fmt.Errorf("reclaim timeout is too large")
		}
		policy.Timeout = time.Duration(seconds) * time.Second
	}
	key := local.ID()
	return newMeshIntent("mesh_operator_prepare", key, meshPrepareRequest{Key: key, Kind: "stop", Stop: &meshops.StopDraft{Host: f.stopHost, Reason: reason, Policy: policy}})
}

func validMeshStopHost(host string) bool {
	raw, err := hex.DecodeString(host)
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == host
}

func meshStopResultText(raw json.RawMessage, expectedKey ...string) string {
	var result meshops.StopResult
	if nodestop.Decode(raw, &result) != nil {
		return "Recorded stop outcome is unavailable. Look up this exact operation."
	}
	status := map[string]string{"accepted": "Stop accepted", "draining": "Waiting for work and cleanup", "stopped": "Original ARM backend stopped", "unknown": "Stop outcome unknown", "blocked": "Stop needs attention"}[result.Status]
	if status == "" {
		status = "Recorded stop outcome"
	}
	if result.Status == "stopped" {
		o := result.Observation
		if result.Operation != "stop-arm-node" || (len(expectedKey) > 0 && result.Key != expectedKey[0]) || result.Record != nil || o == nil || !o.ServerJoined || !o.OriginalGenerationAbsent || o.Record.Plan.Key != result.Key || o.Record.Phase != "drain-ready" || o.Record.Validate() != nil || o.Desired.Validate(o.Record) != nil {
			status = "Stop outcome unknown · original ARM backend stop is not confirmed"
		} else if o.Desired.Running {
			status = "Original ARM backend stopped · restart requested separately"
		}
	}
	return status + "\n" + safe(result.Detail) + "\nThis exact receipt remains available. Starting the node is a separate reviewed operation."
}

func meshStopDraftSummary(raw json.RawMessage) string {
	var q meshPrepareRequest
	if json.Unmarshal(raw, &q) != nil || q.Kind != "stop" || q.Stop == nil {
		return "Exact Stop draft unavailable."
	}
	return "Exact host: " + safe(q.Stop.Host) + "\nPolicy: " + meshStopPolicy(q.Stop.Policy) + "\nReason: " + safe(q.Stop.Reason) + "\n\n[i] Complete exact request; no stop has been approved."
}

func meshStopCanDecline(op meshops.Operation, now time.Time) bool {
	if op.Kind != "stop" || op.Phase != "awaiting_review" || !op.WorkerLive || op.Approved || op.LookupOnly || op.ReviewRevision < 1 || !now.Before(op.ReviewExpires) {
		return false
	}
	raw, err := hex.DecodeString(op.ReviewHash)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == op.ReviewHash
}

func meshStopOperationBound(op meshops.Operation) bool {
	if op.Kind != "stop" || len(op.Targets) != 1 || !strings.HasPrefix(op.Targets[0], "host:") || !validMeshStopHost(strings.TrimPrefix(op.Targets[0], "host:")) {
		return false
	}
	if len(op.Review) > 0 {
		var review meshStopReview
		if json.Unmarshal(op.Review, &review) != nil || review.Key != op.Key || review.Plan.Key != op.Key || op.Targets[0] != "host:"+review.Plan.Target.Host {
			return false
		}
	}
	return true
}
func meshStopOperationResult(op meshops.Operation) string {
	var review meshStopReview
	var result meshops.StopResult
	if !meshStopOperationBound(op) || json.Unmarshal(op.Review, &review) != nil || nodestop.Decode(op.Result, &result) != nil || result.Key != op.Key || result.Validate(review) != nil {
		return "Stop outcome unknown\nThe result does not match the exact reviewed backend. Read its source receipt."
	}
	return meshStopResultText(op.Result, op.Key)
}

func meshStopLeasePreview(item nodestop.Obligation) string {
	if item.Kind != "lease" {
		return "ARM connection or owned interval"
	}
	v := item.Lease
	if item.LeaseDetails != "permitted" || v == nil {
		return "Details not shared by destination. Node-stop does not grant private lease reads."
	}
	who := "UID " + safe(v.Owner)
	if v.Username != "" {
		who = safe(v.Username) + " (" + who + ")"
	} else {
		if v.Truncated {
			who += " · name omitted from preview"
		} else {
			who += " · name not recorded"
		}
	}
	why := safe(v.Reason)
	if why == "" {
		if v.Truncated {
			why = "Purpose omitted from preview for size."
		} else {
			why = "Purpose not recorded."
		}
	}
	if v.Truncated {
		why += "\nShortened preview · [d] complete details"
	}
	return who + "\n" + why
}
func meshStopLeases(op *meshops.Operation) []nodestop.Obligation {
	if op == nil || op.Kind != "stop" {
		return nil
	}
	var review meshStopReview
	if json.Unmarshal(op.Review, &review) != nil || review.Validate() != nil || !meshStopOperationBound(*op) {
		return nil
	}
	var out []nodestop.Obligation
	for _, item := range review.Plan.Impact.Obligations {
		if item.Kind == "lease" {
			out = append(out, item)
		}
	}
	return out
}
func meshStopLeaseRequest(ui *meshDashboard) (meshops.StopLeaseRequest, error) {
	items := meshStopLeases(ui.operation)
	if ui.stopLeaseIndex < 0 || ui.stopLeaseIndex >= len(items) {
		return meshops.StopLeaseRequest{}, fmt.Errorf("Select a lease in the exact Stop review first")
	}
	item := items[ui.stopLeaseIndex]
	if item.LeaseDetails != "permitted" || item.Lease == nil {
		return meshops.StopLeaseRequest{}, fmt.Errorf("Lease details are not shared by this destination; node-stop does not grant private lease reads")
	}
	return meshops.StopLeaseRequest{Key: ui.operation.Key, ReviewRevision: ui.operation.ReviewRevision, ReviewHash: ui.operation.ReviewHash, Obligation: item.ID}, nil
}
func meshStopLeaseMatches(op *meshops.Operation, q meshops.StopLeaseRequest, d meshops.StopLeaseDetail) bool {
	if op == nil || op.Key != q.Key || op.ReviewRevision != q.ReviewRevision || op.ReviewHash != q.ReviewHash || d.Key != q.Key || d.ReviewRevision != q.ReviewRevision || d.ReviewHash != q.ReviewHash || d.Obligation != q.Obligation || d.ID != strings.TrimPrefix(q.Obligation, "lease:") {
		return false
	}
	for _, item := range meshStopLeases(op) {
		if item.ID == q.Obligation && len(op.Targets) == 1 && op.Targets[0] == "host:"+d.Host {
			return true
		}
	}
	return false
}
func meshStopLeaseWindow(d meshops.StopLeaseDetail, width int) string {
	who := "UID " + safe(d.Owner)
	if d.Username != "" {
		who = safe(d.Username) + " (" + who + ")"
	}
	text := fmt.Sprintf("Lease %s\nDestination %s\nOwner %s\n\nPURPOSE\n%s\n\nAgent %s\nProject %s\nOrigin %s\nState %s %s\nRevision %d · reviewed %d\nCPU %g cores · memory %s · disk %s\nCreated %s", safe(d.ID), safe(d.Host), who, safe(d.Reason), safe(d.Agent), safe(d.Project), safe(d.Origin), safe(d.State), safe(d.Outcome), d.Revision, d.ReviewedRevision, d.Resources.CPU, byteCount(fmt.Sprint(d.Resources.Memory)), byteCount(fmt.Sprint(d.Resources.Disk)), d.Created.Local().Format(time.RFC1123))
	if d.Kind != "" {
		text += "\nLease kind " + safe(d.Kind)
	}
	for _, claim := range d.Pools {
		text += fmt.Sprintf("\nCustom pool %s · %d units", safe(claim.ID), claim.Quantity)
	}
	for _, claim := range d.Ports {
		port := strconv.Itoa(claim.Port)
		if claim.RangeEnd > 0 {
			port += "-" + strconv.Itoa(claim.RangeEnd)
		}
		text += "\nPort " + safe(claim.Name) + " " + safe(claim.Network) + "://" + safe(claim.Address) + ":" + port + " · " + safe(claim.Mode) + " · " + safe(claim.State)
	}
	for _, v := range []struct {
		name string
		at   *time.Time
	}{{"Started", d.Started}, {"Expires", d.Expires}, {"Ended", d.Ended}} {
		if v.at != nil {
			text += "\n" + v.name + " " + v.at.Local().Format(time.RFC1123)
		}
	}
	if d.Changed {
		text += "\n\nChanged since review. These current details do not update or approve Stop; destination admission rechecks the complete scope."
	}
	text += "\n\nRead using current destination permissions. Esc closes these details; the original Stop review and Keep running choice remain."
	return window("LEASE DETAILS · READ ONLY", ansi.Wrap(text, max(1, width-4), ""), width, 0, false, "blue")
}
