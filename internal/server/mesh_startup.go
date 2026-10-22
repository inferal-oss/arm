package server

import (
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

func saveMeshStartupPolicy(home, address string, capacity model.Resources) error {
	return mesh.SaveStartupPolicy(home, mesh.StartupPolicy{Schema: 1, Resources: capacity, Listen: address})
}
