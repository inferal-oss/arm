//go:build !arm_devtrace

package manager

import (
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/pressure"
	"time"
)

func traceCPUWarningEnter(*Manager, cpuWarningSnapshot, time.Time, time.Duration, []model.Lease) {}
func traceCPUWarningReturn(_ *Manager, _ cpuWarningSnapshot, _ time.Time, _ time.Duration, _ []model.Lease, s pressure.Sample, e model.CPUWarningEvidence) (pressure.Sample, model.CPUWarningEvidence) {
	return s, e
}
