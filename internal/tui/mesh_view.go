package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/meshops"
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
	if ui.mode == "kubernetes" && ui.kube != nil {
		return len(ui.kube.nodes)
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
		if len(ui.prompt.uninstallReview) > 0 {
			text = meshUninstallReviewWindows(ui.prompt.uninstallReview, m.meshStopPaneWidth()) + "\n\n" + text
		}
		if len(ui.prompt.provisionReview) > 0 {
			text = meshProvisionReviewWindows(ui.prompt.provisionReview, m.meshStopPaneWidth()) + "\n\n" + text
		}
		return out.String() + text + "\n\n" + keycap("y", "Confirm exact request", "") + "  " + keycap("n/Esc", "Keep unchanged", "quiet")
	}
	if ui.form != nil {
		if ui.form.provisionInstall {
			out.WriteString(meshStopWindow("INSTALL ARM · USER-OWNED NODE", "Enter an SSH destination or a reviewed native connector ID. ARM selects trusted embedded/catalog payloads for the verified platform and derives private user paths.\nNo executable, shell, home, startup or payload overrides are accepted here.\nPreparation is read-only; installation and later mesh trust are separate decisions.", m.meshStopPaneWidth(), "warning") + "\n\n")
		} else if ui.form.provisionSourceKey != "" {
			out.WriteString(meshStopWindow("SEPARATE MESH TRUST REVIEW", "Installation completed. Mesh trust is managed separately.\nOriginal provisioning "+safe(ui.form.provisionSourceKey)+"\nConnection key "+safe(ui.form.provisionConnectionKey)+"\nThe destination and executable are fixed by that receipt. Choose fresh directional permissions below; a separate exact review follows.", m.meshStopPaneWidth(), "warning") + "\n\n")
		} else if ui.form.uninstallHost != "" {
			width := m.meshStopPaneWidth()
			out.WriteString(meshStopWindow("UNINSTALL ARM · SELECT MANAGED INSTALLATION", "Exact host "+safe(ui.form.uninstallHost)+"\nStop the reviewed backend; remove only its proven managed executable. Database, identity, configuration, history, backups and recovery remain. A separate concrete approval follows.", width, "warning") + "\n\n")
			provenance := safe(ui.form.provenanceReason)
			if !ui.form.contextReady {
				provenance = "Reading current permissions and completed source provenance. Preparation is disabled."
			}
			for _, candidate := range ui.form.provisions {
				prefix := "  "
				if candidate.ProvisionKey == ui.form.provisionKey {
					prefix = "▸ "
				}
				provenance += "\n\n" + prefix + "Source receipt " + safe(candidate.ProvisionKey) + "\n" + safe(candidate.Binary) + "\n" + safe(candidate.OS) + " / " + safe(candidate.Arch) + " · " + byteCount(fmt.Sprint(candidate.Size)) + "\nSHA-256 " + safe(candidate.SHA256)
			}
			provenance += "\n\n[ / ] select exact receipt · [r] refresh provenance"
			out.WriteString(meshStopWindow("ORIGINAL PUBLICATION", provenance, width, "") + "\n\n")
		} else if ui.form.stopHost != "" {
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
			if ui.form.uninstallHost != "" {
				value = ansi.Wrap(value, max(1, m.meshStopPaneWidth()-6), "")
			}
			fmt.Fprintf(&out, "%s%s\n  %s\n", selectedPrefix(ui.form.selected, i), safe(field.label), value)
		}
		if ui.form.provisionInstall {
			out.WriteString("\n[Enter] edit · [p] prepare read-only installation review · Esc keep host unchanged")
		} else if ui.form.stopHost == "" {
			out.WriteString("\n" + meshops.PreparationNotice)
		} else {
			out.WriteString("\n[Enter] edit · [Space] choose policy · [p] prepare review · Esc keep running\nTimeout is whole seconds, Wait only; positive timeout requests ARM-owned reclamation. Force never discards foreign or unknown work.")
		}
		return out.String()
	}
	if ui.mode == "kubernetes" && ui.kube != nil {
		return out.String() + m.kubeText(ui.kube)
	}
	if ui.unknown != nil {
		advice := "[l] Read durable keyed receipt · [t] Retry this exact request only"
		if ui.unknown.LookupOnly {
			advice = "RESEND DISABLED · Lifecycle outcome is uncertain.\n[l] Read the exact original receipt; no decision, apply, resume or handoff is resent."
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
		if allowed, why := meshUninstallAvailability(n); allowed {
			out.WriteString("[u] Review uninstall · remove managed executable; keep data and identity\n")
		} else if !n.Local {
			out.WriteString("Uninstall unavailable · " + safe(why) + "\n")
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
			if op.Kind == "provision" && op.Error.Code == "confirmation_declined" {
				out.WriteString("You kept this host unchanged; no installation was approved.\n\n")
			} else {
				out.WriteString(safe(op.Error.Error()) + "\n\n")
			}
		}
		if len(op.Review) > 0 {
			summary, _ := meshReviewSummary(op.Review)
			if op.Kind == "uninstall" {
				summary = meshUninstallReviewWindows(op.Review, m.meshStopPaneWidth())
			}
			if op.Kind == "provision" {
				summary = meshProvisionReviewWindows(op.Review, m.meshStopPaneWidth())
			}
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
			if op.Kind == "provision" {
				text := "Installation outcome is unresolved. Read the exact original receipt; no upload/start/seed is repeated."
				if meshProvisionConfirmed(op) {
					text = "This receipt records verified ARM installation and startup. Installation itself grants no mesh trust or permissions; current availability and current trust are separate."
				}
				out.WriteString(meshStopWindow("RECORDED INSTALLATION OUTCOME", text, m.meshStopPaneWidth(), "warning") + "\n\n")
			} else if op.Kind == "uninstall" {
				out.WriteString(meshStopWindow("RECORDED UNINSTALL OUTCOME", meshUninstallOutcome(op), m.meshStopPaneWidth(), "warning") + "\n\n")
			} else if op.Kind == "stop" {
				out.WriteString(window("RECORDED STOP OUTCOME", meshStopOperationResult(op), max(16, m.mainWidth()-8), 0, false, "warning") + "\n\n")
			} else {
				out.WriteString("A durable result is recorded. [i] opens its complete collapsible record.\n\n")
			}
		}
		if op.Kind == "provision" {
			out.WriteString("[r] Read exact source receipt · [i] Complete collapsible record · Esc operations\n")
			if meshCanReview(op, time.Now()) {
				out.WriteString("[a] Review installation · [n] Keep host unchanged\n")
			}
			if op.Approved {
				out.WriteString("[l] Inspect original installation · read only; no upload/start/seed resent\n")
			}
			if meshProvisionConfirmed(op) {
				out.WriteString("[g] Review separate mesh connection · new trust/permissions decision\n")
			}
			return out.String()
		}
		if op.Kind == "uninstall" {
			out.WriteString("[r] Read exact source receipt · [i] Complete record · Esc operations\n")
			if op.Approved {
				out.WriteString("[l] Check original uninstall · explicit read only; no Stop is resent.\n")
				if meshUninstallFinishReady(ui) {
					out.WriteString("[f] Review original finish · separate default-No confirmation\n")
				} else {
					out.WriteString("Finish requires a fresh original-key check, positive original Stop and a still-present owned executable.\n")
				}
			} else {
				if meshCanReview(op, time.Now()) {
					out.WriteString("[a] Review uninstall · ")
				}
				if meshUninstallCanDecline(op, time.Now()) {
					out.WriteString("[n] Keep installation unchanged\n")
				}
			}
			out.WriteString("No resume, handoff or automatic retry. Retained data and history stay.\n")
		} else if op.Kind == "stop" {
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
		out.WriteString("\n[Enter] Node details · [K] Kubernetes Nodes · [n] Connect node · [o] Operations · [r] Refresh\nReading this registry never initializes Mesh or fans out to all peers.")
	}
	if ui.loading {
		out.WriteString("\n\nReading exact authority…")
	}
	return out.String()
}
