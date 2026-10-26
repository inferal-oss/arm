//go:build !arm_devtrace

package manager

import "github.com/yrashk/arm/internal/model"

func traceCPUBudget(*Manager, model.CPUBudget)             {}
func traceCPUAdmission(*Manager, model.CPUBudget, float64) {}
func traceCPURenewal(*Manager, model.CPUBudget)            {}
