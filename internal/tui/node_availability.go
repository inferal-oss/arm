package tui

import (
	"fmt"
	"strings"

	"getarm.org/internal/nodeavailability"
)

// Prepared node-detail section. Its caller supplies an exact host-bound read;
// rendering never makes an RPC, changes a profile or authorizes placement.
func meshAvailabilityText(host string, report *nodeavailability.Report, profileState, connection, resourceState string) string {
	declaration := "Unknown / unsupported"
	if profileState == "reported" && report != nil && report.Schema == 1 && report.Host == host {
		declaration = map[string]string{"unspecified": "Unspecified (owner declared)", "stable": "Stable (owner declared)", "intermittent": "Mobile / intermittent (owner declared)"}[report.OwnerDeclared.Profile]
		if declaration == "" {
			declaration = "Unknown / unsupported"
		}
	} else if profileState == "stale" {
		declaration = "Stale owner declaration; refresh this exact node"
		if report != nil && report.Schema == 1 && report.Host == host {
			if profile := map[string]string{"unspecified": "Unspecified", "stable": "Stable", "intermittent": "Mobile / intermittent"}[report.OwnerDeclared.Profile]; profile != "" {
				declaration = "Last reported: " + profile + " (owner declared); stale declaration"
			}
		}
	}
	reachability := map[string]string{"reachable": "Reachable", "rejoining": "Rejoining", "offline": "Offline", "stale": "Stale", "denied": "Observation denied", "unsupported": "Observation unsupported", "identity_unavailable": "Identity unavailable"}[connection]
	if reachability == "" {
		reachability = "Unknown"
	}
	resources := map[string]string{"fresh": "Fresh report; destination admission still required", "stale": "Stale; unavailable for placement", "unknown": "Unknown; unavailable for placement"}[resourceState]
	if resources == "" {
		resources = "Unknown; unavailable for placement"
	}
	return fmt.Sprintf("OWNER AVAILABILITY\n%s\nConnection: %s\nResource availability: %s\nStable host ID: %s\n%s", safe(declaration), safe(reachability), safe(resources), safe(host), strings.Join([]string{"The owner declaration is advisory; observed freshness is separate.", "Prefer stable hosts for long or deadline-sensitive work. Automatic cross-host placement is not implemented."}, "\n"))
}

// meshAvailabilityDetails consumes only observations bound to the selected ID.
// Failure of an optional read leaves cached declarations explicitly stale.
func meshAvailabilityDetails(host string, report *nodeavailability.Report, observation *nodeavailability.Inspection) string {
	profileState, connection, resources := "unsupported", "unobserved", "unknown"
	if observation != nil && observation.Schema == 1 && observation.Host == host {
		connection = observation.ConnectionState
		states := observation.Resources
		if states.CPU == "fresh" && states.Memory == "fresh" && states.Disk == "fresh" {
			resources = "fresh"
		} else if states.CPU == "stale" || states.Memory == "stale" || states.Disk == "stale" {
			resources = "stale"
		}
		if report == nil && observation.OwnerDeclared != nil && observation.LastSuccess != nil {
			report = &nodeavailability.Report{Schema: 1, Host: host, OwnerDeclared: *observation.OwnerDeclared, ObservedAt: *observation.LastSuccess}
			profileState = "stale"
		}
	}
	if report != nil && report.Host == host && profileState != "stale" {
		profileState = "reported"
	}
	return meshAvailabilityText(host, report, profileState, connection, resources)
}
