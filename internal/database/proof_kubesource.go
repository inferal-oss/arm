package database

import (
	"fmt"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

// validateKubernetesSourcePairs is relationship validation, never native source
// authority. readLeases already blocks each unsettled source/storage promise.
func validateKubernetesSourcePairs(leases map[string]model.Lease) error {
	byKey := map[string]model.Lease{}
	references := map[string]int{}
	for _, l := range leases {
		if l.Request.StorageLease != "" {
			references[l.Request.StorageLease]++
		}
		if !kubesource.IsReserved(l.Request) {
			continue
		}
		if _, ok := byKey[l.Request.Key]; ok {
			return fmt.Errorf("duplicate Kubernetes source tuple role")
		}
		byKey[l.Request.Key] = l
	}
	for _, l := range byKey {
		s, parent, err := kubesource.ValidateRole(l)
		if err != nil {
			return err
		}
		if parent {
			source, ok := byKey[s.Key(false)]
			if !ok || source.Host != l.Host || source.Owner != l.Owner || source.Request.Budget != l.Request.Budget {
				return fmt.Errorf("orphan or foreign Kubernetes source parent")
			}
			if source.Request.StorageLease == "" {
				if references[l.ID] != 0 {
					return fmt.Errorf("unattached source parent has foreign consumers")
				}
				if l.Artifact != nil && (l.Artifact.ContentObserved || l.Artifact.AllocatedBefore != nil) {
					return fmt.Errorf("unattached source parent carries content history")
				}
				continue
			}
			if references[l.ID] != 1 || kubesource.ValidatePair(source, l) != nil {
				return fmt.Errorf("nonunique Kubernetes source relationship")
			}
		} else if l.Request.StorageLease != "" {
			parent, ok := leases[l.Request.StorageLease]
			if !ok || parent.Request.Key != s.Key(true) || kubesource.ValidatePair(l, parent) != nil {
				return fmt.Errorf("missing or mismatched Kubernetes source parent")
			}
		}
	}
	return nil
}
