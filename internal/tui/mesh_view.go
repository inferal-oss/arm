package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/meshops"
)

func (m *dashboardModel) meshItemCount() int {
	ui := m.meshUI
	if ui == nil {
		return 0
	}
	if ui.form != nil {
		return len(ui.form.fields)
	}
	if ui.mode == "nodes" {
		return len(ui.registry.Nodes)
	}
	if ui.mode == "operations" {
		return len(ui.operations)
	}
	return 0
}

func (m *dashboardModel) meshText() string {
	ui := m.meshUI
	if ui == nil {
		return "Mesh · local operator\n\n[r] Read local node and configured direct peers. Reading never initializes Mesh."
	}
	var out strings.Builder
	if ui.err != "" {
		out.WriteString(strong.Foreground(colorWarning).Render(ui.err) + "\n\n")
	}
	if ui.busy {
		out.WriteString("REQUEST IN FLIGHT · closing does not cancel the backend workflow\nNo automatic retry; exact request is retained.\n\n")
	}
	if ui.prompt != nil {
		text := ui.prompt.text
		if len(ui.prompt.stopReview) > 0 {
			// Reframe the immutable display snapshot at the current modal width;
			// resizing must not wrap previously rendered borders or change consent.
			text = meshStopReviewWindows(ui.prompt.stopReview, m.meshStopPaneWidth()) + "\n\n" + text
		}
		return out.String() + text + "\n\n" + keycap("y", "Confirm exact request", "") + "  " + keycap("n/Esc", "Keep unchanged", "quiet")
	}
	if ui.form != nil {
		if ui.form.stopHost != "" {
			width := m.meshStopPaneWidth()
			out.WriteString(meshStopWindow("EXACT BACKEND", "Exact host "+safe(ui.form.stopHost)+"\nStops this ARM backend and its connections. The host remains running.\nA separate current review follows; default is to refuse outstanding work.", width, "warning") + "\n\n")
		} else {
			out.WriteString("NODE ENROLLMENT · LOCAL OPERATOR\n\nUse an SSH destination or @native-reviewed-connector.\n[Enter] edit · [Space] cycle choices · [p] prepare · Esc back\nResource ceilings apply to leases/run; mode none grants nothing.\n\n")
		}
		if ui.form.reconnectHost != "" {
			fmt.Fprintf(&out, "RECONNECT EXACT NODE %s\nNew enrollment and fresh permissions; previous grants are not restored.\nA blank locator uses the retained record only after fresh verification.\n\n", safe(ui.form.reconnectHost))
			if !ui.form.contextReady {
				out.WriteString("READING RECONNECT CONTEXT · preparation is disabled. [r] retry read.\n\n")
			}
			if len(ui.form.blockers) > 0 {
				out.WriteString("PENDING WORKFLOW · preparation is disabled.\nSettle the original operation or explicitly review its handoff; old obligations remain.\n[ / ] select blocker · [l] inspect its recovery actions · [r] refresh context\n")
				if ui.form.blockerIndex < 0 || ui.form.blockerIndex >= len(ui.form.blockers) {
					out.WriteString("No workflow selected · press [ or ] to choose an exact pending key.\n")
				}
				for i, b := range ui.form.blockers {
					fmt.Fprintf(&out, "%s%s · %s · %s\n", selectedPrefix(ui.form.blockerIndex, i), safe(b.Key), safe(b.Kind), safe(b.Phase))
				}
				out.WriteString("\n")
			}
		}
		for i, field := range ui.form.fields {
			value := safe(field.value)
			if ui.form.stopHost != "" && i == 0 {
				value = map[string]string{"refuse": "Refuse while work remains", "wait": "Wait for existing work", "force": "Reclaim ARM-owned work"}[field.value]
			}
			if ui.form.stopHost != "" && i == 1 && field.value == "" {
				value = "No timeout"
			}
			if value == "" {
				value = "(empty)"
			}
			fmt.Fprintf(&out, "%s%s\n  %s\n", selectedPrefix(ui.form.selected, i), safe(field.label), value)
		}
		if ui.form.stopHost == "" {
			out.WriteString("\n" + meshops.PreparationNotice)
		} else {
			out.WriteString("\n[Enter] edit · [Space] choose policy · [p] prepare review · Esc keep running\nTimeout is whole seconds, Wait only; positive timeout requests ARM-owned reclamation. Force never discards foreign or unknown work.")
		}
		return out.String()
	}
	if ui.unknown != nil {
		advice := "[l] Read durable keyed receipt · [t] Retry this exact request only"
		if ui.unknown.LookupOnly {
			advice = "RESEND DISABLED · Stop outcome is uncertain.\n[l] Read the exact source receipt; no decision, apply, resume or handoff is resent."
		} else if ui.unknown.Operation == "mesh_operator_resume" {
			advice = "RESEND DISABLED · Resume can create another child attempt.\n[l] Read exact parent · [o] Read durable attempts; inspect the recorded child before any separate new recovery. This retained uncertainty cannot be automatically resolved."
		}
		fmt.Fprintf(&out, "RETAINED UNCERTAIN REQUEST\nOperation: %s\nKey: %s\n%s\n[u] Private Recovery across restarts · [i] Exact retained envelope\n\n", safe(ui.unknown.Operation), safe(ui.unknown.Key), advice)
	}
	switch ui.mode {
	case "node":
		if ui.node == nil {
			return out.String() + "No exact node selected."
		}
		n := ui.node
		fmt.Fprintf(&out, "%s\nStable host: %s\nRegistry source: %s\nHealth: %s · trust %s\n\n", strong.Render(safe(n.Name)), safe(n.Host), safe(ui.registry.Source), safe(n.Health), safe(n.Trust.State))
		if n.Local {
			out.WriteString("LOCAL AUTHORITY · native operator\n")
		}
		if account := n.Trust.Account; account != nil {
			fmt.Fprintf(&out, "Execution account: %s (UID %s) · %s snapshot\n", safe(account.Username), safe(account.UID), safe(account.Source))
		}
		if n.Metadata != nil {
			fmt.Fprintf(&out, "\nReported: %s\nPlatform: %s / %s\nARM: %s\n", safe(n.Metadata.Hostname), safe(n.Metadata.Platform.OS), safe(n.Metadata.Platform.Architecture), safe(n.Metadata.Build.Version))
			fmt.Fprintf(&out, "Logical CPUs: %d\n", n.Metadata.Inventory.LogicalCPUs)
		}
		out.WriteString(meshFreshnessText(n.MetadataFreshness) + "\n")
		if !n.Local {
			fmt.Fprintf(&out, "\nPEER MAY DO HERE · inbound rights on this source\n%s\n\nWE MAY DO THERE · destination-reported observation\n", meshGrantText(n.Trust.Inbound))
			if n.Outbound == nil {
				out.WriteString("Unknown · no authority inferred\n")
			} else {
				out.WriteString(meshGrantText(&n.Outbound.Grant) + "\n")
				fmt.Fprintf(&out, "Source: %s\nTarget: %s\nPair: %s · target grant revision %d\n", safe(n.Outbound.Source), safe(n.Outbound.Target), safe(n.Outbound.Pair), n.Outbound.Revision)
			}
			out.WriteString(meshFreshnessText(n.ScopeFreshness) + "\n")
			fmt.Fprintf(&out, "\nPaired certificate fingerprint: %s\nPair: %s · local enrollment revision %d\nAddress: %s\n", safe(n.Trust.Fingerprint), safe(n.Trust.Pair), n.Trust.Revision, safe(n.Trust.Address))
		}
		if n.Error != nil {
			out.WriteString("\n" + safe(n.Error.Error()) + "\n")
		}
		allowed, why := meshStopAvailability(ui.node)
		if allowed {
			out.WriteString("\n[s] Review Stop ARM · affects every connection to this backend\n")
		} else if !n.Local {
			out.WriteString("\nStop ARM unavailable · " + safe(why) + "\n")
		}
		out.WriteString("\n[v] Open exact node dashboard · [d] Review disconnect · [e] Reconnect revoked node · [r] Refresh exact node\n[i] Complete reported/trust record · Esc node list\nMetadata/capabilities are observations, never grants.")
	case "operation":
		if ui.operation == nil {
			return out.String() + "No operation selected."
		}
		op := *ui.operation
		fmt.Fprintf(&out, "%s · %s\nKey: %s\nRevision: %d\nUpdated: %s\n\n%s\n\n", strong.Render(meshPhaseLabel(op)), safe(op.Kind), safe(op.Key), op.Revision, op.Updated.Local().Format("Jan 02 15:04:05"), meshOperationAdvice(op))
		if op.ParentKey != "" {
			fmt.Fprintf(&out, "Original intent: %s · [p] inspect parent\n", safe(op.ParentKey))
		}
		if op.ResolvedBy != "" {
			fmt.Fprintf(&out, "Resolved by: %s · [f] inspect recorded recovery\n", safe(op.ResolvedBy))
		}
		if op.Error != nil {
			out.WriteString(safe(op.Error.Error()) + "\n\n")
		}
		if len(op.Review) > 0 {
			summary, _ := meshReviewSummary(op.Review)
			if op.Kind == "stop" {
				summary = meshStopReviewWindows(op.Review, m.meshStopPaneWidth())
				leases := meshStopLeases(&op)
				if len(leases) > 0 {
					selected := min(ui.stopLeaseIndex, len(leases)-1)
					summary = "Selected lease " + safe(leases[selected].ID) + fmt.Sprintf(" · %d/%d\n[ / ] select lease · [d] read-only details · original review retained\n\n", selected+1, len(leases)) + summary
				}
				if ui.stopLease != nil {
					summary = meshStopLeaseWindow(*ui.stopLease, m.meshStopPaneWidth()) + "\n\n" + summary
				}
			}
			fmt.Fprintf(&out, "EXACT IMMUTABLE REVIEW\nReview revision: %d\nSHA256: %s\nExpires: %s\n\n%s\n\n", op.ReviewRevision, safe(op.ReviewHash), op.ReviewExpires.Local().Format("Jan 02 15:04:05 MST"), summary)
		}
		if len(op.Result) > 0 {
			if op.Kind == "stop" {
				out.WriteString(window("RECORDED STOP OUTCOME", meshStopOperationResult(op), max(16, m.mainWidth()-8), 0, false, "warning") + "\n\n")
			} else {
				out.WriteString("A durable result is recorded. [i] opens its complete collapsible record.\n\n")
			}
		}
		if op.Kind == "stop" {
			out.WriteString("[r] Read exact source receipt · [i] Complete collapsible record · Esc operations\n")
			if op.LookupOnly || op.Approved {
				out.WriteString("[l] Check original stop · explicit read only; nothing is reapplied.\n")
			} else if meshCanReview(op, time.Now()) {
				out.WriteString("[a] Review Stop ARM · [n] Keep running · [c] Request cancellation\n")
			} else if meshStopCanDecline(op, time.Now()) {
				out.WriteString("[n] Keep running · [c] Request cancellation; no stop is approved.\n")
			}
			if op.CanPrepareFresh && !op.LookupOnly && !op.Approved {
				out.WriteString("[g] Prepare fresh review with new key; old receipt remains.\n")
			}
		} else {
			out.WriteString("[a] Review approval · [n] Review decline · [c] Review cancellation\n[s] Explicit recovery/resume · [h] Review handoff · [r] Read current receipt\n[i] Complete collapsible operation · Esc operation list")
		}
	case "operations":
		out.WriteString("DURABLE MESH OPERATIONS · LOCAL COORDINATOR\nClosing or reconnecting never retries a mutation.\n\n")
		for i, op := range ui.operations {
			fmt.Fprintf(&out, "%s%s · %s\n  Key: %s · revision %d\n  %s\n\n", selectedPrefix(ui.selected, i), safe(op.Kind), meshPhaseLabel(op), safe(op.Key), op.Revision, meshOperationAdvice(op))
			if op.ParentKey != "" {
				fmt.Fprintf(&out, "  Recovery of: %s\n\n", safe(op.ParentKey))
			}
		}
		if len(ui.operations) == 0 {
			out.WriteString("No recorded operator workflows.\n")
		}
		out.WriteString("\n[Enter] Inspect exact operation · [o] Nodes · [r] Refresh")
	default:
		fmt.Fprintf(&out, "MESH · LOCAL OPERATOR\nAuthority\n%s\nMesh: %s\n\n", safe(ui.registry.Source), safe(ui.registry.MeshState))
		for i, n := range ui.registry.Nodes {
			label := "Direct peer"
			if n.Local {
				label = "Localhost"
			}
			platform := "Metadata not yet read"
			if n.Metadata != nil {
				platform = fmt.Sprintf("%s / %s · ARM %s", safe(n.Metadata.Platform.OS), safe(n.Metadata.Platform.Architecture), safe(n.Metadata.Build.Version))
			}
			account := "Execution account not reported"
			if a := n.Trust.Account; a != nil {
				account = safe(a.Username) + " · UID " + safe(a.UID)
			}
			connection := human(safe(n.Health)) + " · " + human(safe(n.Trust.State))
			if n.Local {
				connection = "This backend"
			}
			lines := []string{"ID " + safe(n.Host), platform, align(muted.Render("Connection"), connection, max(1, m.mainWidth()-12)), "Run as " + account}
			title := safe(n.Name) + " · " + label
			if i == ui.selected {
				title = "> " + title
			}
			out.WriteString(window(title, strings.Join(lines, "\n"), max(12, m.mainWidth()-4), 0, i == ui.selected, "") + "\n\n")
		}
		if ui.registry.Error != nil {
			out.WriteString(safe(ui.registry.Error.Error()) + "\n")
		}
		out.WriteString("\n[Enter] Node details · [n] Connect node · [o] Operations · [r] Refresh\nReading this registry never initializes Mesh or fans out to all peers.")
	}
	if ui.loading {
		out.WriteString("\n\nReading exact authority…")
	}
	return out.String()
}
