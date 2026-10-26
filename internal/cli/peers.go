package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// peers off/on is a private local preference. The daemon observes it on its
// next presence tick; the peer-only endpoint checks it on every request.
func (a *app) peers(args []string) (int, error) {
	f := a.flags("peers")
	f.Bool("json", false, "JSON output")
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	rest := f.Args()
	action := "status"
	if len(rest) > 1 {
		return 2, fmt.Errorf("use peers [status|off|on]")
	}
	if len(rest) == 1 {
		action = rest[0]
	}
	switch action {
	case "status":
		var report model.PeerOverview
		if err := a.call("peers_status", nil, &report); err != nil {
			return 1, err
		}
		return 0, a.print(report)
	case "off":
		if err := local.Prepare(a.home); err != nil {
			return 1, err
		}
		if err := local.AtomicJSON(filepath.Join(a.home, "presence-disabled"), map[string]any{"disabled_at": time.Now().UTC()}); err != nil {
			return 1, err
		}
		return 0, a.print(map[string]any{"presence": "disabled", "effective": "immediate for new peer requests; advertisement removed on next tick"})
	case "on":
		if err := local.Prepare(a.home); err != nil {
			return 1, err
		}
		if err := os.Remove(filepath.Join(a.home, "presence-disabled")); err != nil && !os.IsNotExist(err) {
			return 1, err
		}
		return 0, a.print(map[string]any{"presence": "enabled", "effective": "next daemon presence tick"})
	default:
		return 2, fmt.Errorf("use peers [status|off|on]")
	}
}

func peerResources(r model.Resources) string {
	return fmt.Sprintf("%.3g cores · %s RAM · %s disk", r.CPU, humanBytes(strconv.FormatInt(r.Memory, 10)), humanBytes(strconv.FormatInt(r.Disk, 10)))
}
func peerItemLine(item model.PeerItem) string {
	line := fmt.Sprintf("%s  %s", terminalSafe(item.State), peerResources(item.Resources))
	if item.State == "queued" {
		if item.RequestedTTL > 0 {
			line += " · requested for " + item.RequestedTTL.String()
		}
		if item.QueuePosition > 0 {
			line += fmt.Sprintf(" · queue #%d", item.QueuePosition)
		}
		if item.EstimatedStart != nil {
			line += " · estimated start " + item.EstimatedStart.Local().Format(time.RFC3339)
		} else if item.EstimateKind != "" {
			line += " · estimate " + terminalSafe(item.EstimateKind)
		}
		if item.StartDeadline != nil {
			line += " · start by " + item.StartDeadline.Local().Format(time.RFC3339)
		}
	} else if item.Expires != nil {
		line += " · expires " + item.Expires.Local().Format(time.RFC3339)
	} else {
		line += " · expiry unknown"
	}
	if item.OtherClaims {
		line += " · other claims omitted"
	}
	return line
}
func (a *app) peersHuman(report model.PeerOverview) error {
	var b strings.Builder
	fmt.Fprintln(&b, a.paint("1;36", "ARM · Nearby instances"))
	fmt.Fprintf(&b, "Local held: %s (%d)\n", peerResources(report.Local.Held), report.Local.HeldCount)
	fmt.Fprintf(&b, "Local queued: %s (%d)\n", peerResources(report.Local.Queued), report.Local.QueuedCount)
	for _, item := range report.Local.Items {
		fmt.Fprintln(&b, "  "+peerItemLine(item))
	}
	if report.Local.Truncated {
		fmt.Fprintln(&b, "  Local itemized list truncated; totals include all claims.")
	}
	fmt.Fprintf(&b, "Peer-reported held: %s (%d)\n", peerResources(report.PeerHeld), report.PeerHeldCount)
	fmt.Fprintf(&b, "Peer-reported queued: %s (%d)\n", peerResources(report.PeerQueued), report.PeerQueuedCount)
	if !report.TotalsComplete {
		fmt.Fprintln(&b, "Peer totals incomplete: discovery or a peer is unavailable.")
	}
	if report.DiscoveryUnavailable != "" {
		fmt.Fprintln(&b, "Discovery: "+terminalSafe(report.DiscoveryUnavailable))
	}
	if report.DiscoveryTruncated {
		fmt.Fprintln(&b, "Discovery: scan truncated")
	}
	for _, peer := range report.Instances {
		fmt.Fprintf(&b, "\nUID %d · %s · %s\n", peer.UID, terminalSafe(peer.Host), terminalSafe(peer.URL))
		if peer.Snapshot == nil {
			fmt.Fprintln(&b, "  "+terminalSafe(peer.Error))
			continue
		}
		fmt.Fprintf(&b, "  Held: %s (%d)\n", peerResources(peer.Snapshot.Held), peer.Snapshot.HeldCount)
		fmt.Fprintf(&b, "  Queued: %s (%d)\n", peerResources(peer.Snapshot.Queued), peer.Snapshot.QueuedCount)
		for _, item := range peer.Snapshot.Items {
			fmt.Fprintln(&b, "    "+peerItemLine(item))
		}
		if peer.Snapshot.Truncated {
			fmt.Fprintln(&b, "    Itemized list truncated; totals include all reported claims.")
		}
	}
	fmt.Fprintln(&b, "\n"+terminalSafe(report.Advisory))
	_, err := io.WriteString(a.out, b.String())
	return err
}
