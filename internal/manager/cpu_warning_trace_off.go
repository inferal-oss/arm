//go:build !arm_devtrace

package manager

import (
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/pressure"
	"time"
)

func traceCPUWarningEnter(*Manager, cpuWarningSnapshot, time.Time, time.Duration, []model.Lease) {}
func traceCPUWarningReturn(_ *Manager, _ cpuWarningSnapshot, _ time.Time, _ time.Duration, _ []model.Lease, s pressure.Sample, e model.CPUWarningEvidence) (pressure.Sample, model.CPUWarningEvidence) {
	return s, e
}
