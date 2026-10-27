//go:build !arm_devtrace

package manager

import (
	"getarm.org/internal/model"
	"getarm.org/internal/pressure"
	"time"
)

func traceCPUWarningEnter(*Manager, cpuWarningSnapshot, time.Time, time.Duration, []model.Lease, pressureElapsedView) {
}
func traceCPUWarningReturn(_ *Manager, _ cpuWarningSnapshot, _ time.Time, _ time.Duration, _ []model.Lease, _ pressureElapsedView, s pressure.Sample, e model.CPUWarningEvidence) (pressure.Sample, model.CPUWarningEvidence) {
	return s, e
}
