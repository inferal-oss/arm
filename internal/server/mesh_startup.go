package server

import (
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/mesh"
)

func saveConfiguredMeshStartupPolicy(home string, settings daemonconfig.Settings) error {
	policy := mesh.StartupPolicy{Schema: 1, Resources: settings.Resources, Listen: settings.Listen}
	// Ordinary launch records retain their established format. Named locations
	// need the full inspected hash so fresh/replayed startup cannot drop or replace
	// the pool selection while retaining only the old scalar ceilings.
	if len(settings.StoragePools) > 0 {
		hash, err := daemonconfig.SnapshotSHA256(settings)
		if err != nil {
			return err
		}
		policy.SettingsSHA256 = hash
	}
	return mesh.SaveStartupPolicy(home, policy)
}
