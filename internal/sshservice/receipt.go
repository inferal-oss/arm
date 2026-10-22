package sshservice

import (
	"encoding/json"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

// DefaultSSHPolicyID mirrors the destination's versioned own-UID root policy
// identity. SSH has no caller-selectable policy attachment: ordinary admission
// still applies this destination-owned root automatically.
func DefaultSSHPolicyID(host, owner string) string {
	raw, _ := json.Marshal([]string{host, "local_uid", owner})
	return "budget_owner_" + stateHash(raw)
}
func ValidateSSHLeaseAttachments(l model.Lease) error {
	r := l.Request
	root := DefaultSSHPolicyID(l.Host, l.Owner)
	if l.Cargo != nil || l.Artifact != nil || len(l.Ports) != 0 || l.ManagedDir != "" || l.PoolBinding != nil || l.QueueInfo != nil || len(r.Pools) != 0 || len(r.Ports) != 0 || r.StorageLease != "" || r.ServiceLease != "" || r.PortLease != "" || (r.Budget != "" && r.Budget != root) {
		return fmt.Errorf("SSH lease has unsupported attached obligations")
	}
	if l.Budget != nil {
		b := l.Budget
		if b.Authority != l.Host || b.Owner != l.Owner || b.Selected != root || len(b.Path) != 1 || b.Path[0] != root || b.Bound.IsZero() {
			return fmt.Errorf("SSH lease has nondefault policy ancestry")
		}
	}
	for _, p := range l.BudgetPolicy {
		if p.ID != root || p.Revision < 1 {
			return fmt.Errorf("SSH lease has nondefault policy revision")
		}
	}
	return nil
}
