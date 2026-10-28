package kubejournal

import (
	"fmt"
	"math"
	"slices"
	"time"

	"getarm.org/internal/model"
)

// These are closed evidence snapshots. Conversion from mesh's live types is
// explicit at the coordinator boundary; no snapshot is a native dial authority.
type Identity struct {
	Owner         string    `json:"owner"`
	OwnerUsername string    `json:"owner_username"`
	Host          string    `json:"host"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Certificate   string    `json:"certificate"`
	SPKI          string    `json:"spki_sha256"`
	Fingerprint   string    `json:"fingerprint"`
	Expires       time.Time `json:"expires_at"`
}
type Grant struct {
	Contacts      []string        `json:"contact_ids"`
	OperatorInbox bool            `json:"operator_inbox"`
	Permissions   []string        `json:"permissions"`
	Limits        model.Resources `json:"limits"`
	MaxTTL        time.Duration   `json:"max_ttl_ns"`
}
type PreparedSetup struct {
	Key      string    `json:"key"`
	Token    string    `json:"token"`
	Identity Identity  `json:"identity"`
	Listen   string    `json:"listen"`
	Expires  time.Time `json:"expires_at"`
	Existing bool      `json:"existing_listener"`
}
type Peer struct {
	Identity Identity  `json:"identity"`
	Grant    Grant     `json:"grant"`
	Pair     string    `json:"pair"`
	State    string    `json:"state"`
	Deadline time.Time `json:"deadline"`
	Proven   bool      `json:"proven"`
	Revision int       `json:"revision"`
	Reason   string    `json:"reason"`
}

func (g Grant) Empty() bool {
	return len(g.Contacts) == 0 && !g.OperatorInbox && len(g.Permissions) == 0 && g.Limits == (model.Resources{}) && g.MaxTTL == 0
}
func (g Grant) Validate() error {
	allowed := []string{"observe-host", "observe-own", "observe-all", "lease-own", "run", "message", "contact-read", "ssh-exec", "node-stop", "node-uninstall"}
	if len(g.Contacts) > 64 || len(g.Permissions) > len(allowed) || math.IsNaN(g.Limits.CPU) || math.IsInf(g.Limits.CPU, 0) || g.Limits.CPU < 0 || g.Limits.CPU > 65536 || g.Limits.Memory < 0 || g.Limits.Disk < 0 || g.MaxTTL < 0 || g.MaxTTL > 7*24*time.Hour {
		return fmt.Errorf("invalid Kubernetes directional grant")
	}
	seen := map[string]bool{}
	for _, id := range g.Contacts {
		if id == "" || len(id) > 128 || seen[id] {
			return fmt.Errorf("invalid Kubernetes contact grant")
		}
		seen[id] = true
	}
	seen = map[string]bool{}
	for _, p := range g.Permissions {
		if !slices.Contains(allowed, p) || seen[p] {
			return fmt.Errorf("unsupported or duplicate Kubernetes permission")
		}
		seen[p] = true
	}
	if seen["lease-own"] && (g.MaxTTL <= 0 || g.Limits == (model.Resources{})) || (seen["run"] || seen["ssh-exec"]) && !seen["lease-own"] {
		return fmt.Errorf("Kubernetes execution lacks explicit resource ceilings")
	}
	return nil
}
