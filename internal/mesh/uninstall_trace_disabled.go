//go:build !arm_devtrace

package mesh

import "os"

func traceUninstall(string, UninstallPlan, *UninstallReceipt, error)          {}
func traceUninstallAllocation(UninstallPlan, UninstallReceipt, *os.File)      {}
func traceUninstallOwnership(UninstallPlan, InstallOwnership, error)          {}
func traceUninstallAbsence(UninstallPlan, bool, error)                        {}
func traceUninstallSource(string, UninstallPlan, string, Coordination, error) {}

func traceUninstallAuthorization(string, UninstallPlan, bool, Config, error) {}
