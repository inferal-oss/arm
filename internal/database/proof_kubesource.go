package database

import (
	"fmt"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/model"
)

// Relationship validation is observation only. Every unsettled source, storage
// and port promise remains independently blocked by readLeases.
func validateKubernetesSourcePairs(leases map[string]model.Lease) error {
	byKey := map[string]model.Lease{}
	storageRefs, portRefs := map[string]int{}, map[string]int{}
	for _, l := range leases {
		if l.Request.StorageLease != "" {
			storageRefs[l.Request.StorageLease]++
		}
		if l.Request.PortLease != "" {
			portRefs[l.Request.PortLease]++
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
		s, role, err := kubesource.ValidateRoleKind(l)
		if err != nil {
			return err
		}
		if role == kubesource.SourceRole && (storageRefs[l.ID] != 0 || portRefs[l.ID] != 0) {
			return fmt.Errorf("source consumer cannot be another lease's resource parent")
		}
		if role != kubesource.SourceRole {
			if role == kubesource.StorageRole && portRefs[l.ID] != 0 || role == kubesource.PortRole && storageRefs[l.ID] != 0 {
				return fmt.Errorf("source parent referenced through the wrong resource role")
			}
			source, ok := byKey[s.Key(false)]
			if !ok || source.Host != l.Host || source.Owner != l.Owner || source.Request.Budget != l.Request.Budget {
				return fmt.Errorf("orphan or foreign Kubernetes source parent")
			}
			ref, refs := source.Request.StorageLease, storageRefs[l.ID]
			if role == kubesource.PortRole {
				ref, refs = source.Request.PortLease, portRefs[l.ID]
			}
			if ref == "" {
				if refs != 0 {
					return fmt.Errorf("unattached source parent has foreign consumers")
				}
				if role == kubesource.StorageRole && l.Artifact != nil && (l.Artifact.ContentObserved || l.Artifact.AllocatedBefore != nil) {
					return fmt.Errorf("unattached source parent carries content history")
				}
				continue
			}
			if refs != 1 {
				return fmt.Errorf("nonunique Kubernetes source relationship")
			}
			if role == kubesource.StorageRole {
				if err = kubesource.ValidatePair(source, l); err != nil {
					return err
				}
			} else if err = kubesource.ValidatePortPair(source, l); err != nil {
				return err
			}
			continue
		}
		if l.Request.StorageLease != "" {
			parent, ok := leases[l.Request.StorageLease]
			if !ok || parent.Request.Key != s.Key(true) || kubesource.ValidatePair(l, parent) != nil {
				return fmt.Errorf("missing or mismatched Kubernetes source parent")
			}
		}
		if l.Request.PortLease != "" {
			parent, ok := leases[l.Request.PortLease]
			if !ok || parent.Request.Key != s.PortKey() || kubesource.ValidatePortPair(l, parent) != nil {
				return fmt.Errorf("missing or mismatched Kubernetes source port parent")
			}
		}
	}
	return nil
}
