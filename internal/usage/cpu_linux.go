package usage

import (
	"context"
	"fmt"

	"github.com/inferal-oss/arm/internal/model"
)

// Linux /proc task CPU counters can be adjusted to scheduler runtime while
// host cpustat remains tick-accounted. No universal quantization bound has been
// established; ordinary headroom remains supported, but managed credit is zero.
func captureCPUAnchor(model.ProcessIdentity) (string, error) {
	return "", fmt.Errorf("Linux process/host CPU accounting alignment is not verified")
}
func collectCPU(_ context.Context, cut *CPUCut, _ []CPUOwner) {
	cut.Method = "linux-managed-credit-unsupported"
	cut.Detail = "Linux process/host CPU accounting alignment is not verified; managed CPU credit remains zero"
}
func CPUClockQuantum() (uint64, string, error) {
	return 0, "", fmt.Errorf("Linux process/host CPU accounting alignment is not verified")
}
