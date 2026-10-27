package server

import (
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func saveMeshStartupPolicy(home, address string, capacity model.Resources) error {
	return mesh.SaveStartupPolicy(home, mesh.StartupPolicy{Schema: 1, Resources: capacity, Listen: address})
}
