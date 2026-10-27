package server

import (
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
)

func saveMeshStartupPolicy(home, address string, capacity model.Resources) error {
	return mesh.SaveStartupPolicy(home, mesh.StartupPolicy{Schema: 1, Resources: capacity, Listen: address})
}
