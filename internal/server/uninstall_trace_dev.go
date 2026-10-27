//go:build arm_devtrace

package server

import "getarm.org/internal/mesh"

func traceUninstallNativeBoundary(action, home string, observation NodeStopObservation, resultErr error) {
	mesh.DevUninstallObservedBoundary(action, home, observation, resultErr)
}
