//go:build arm_devtrace

package server

import "github.com/yrashk/arm/internal/mesh"

func traceUninstallNativeBoundary(action, home string, observation NodeStopObservation, resultErr error) {
	mesh.DevUninstallObservedBoundary(action, home, observation, resultErr)
}
