package usage

import (
	"fmt"
	"math"
	"regexp"
)

// Initial positive support is deliberately limited to the native ARM64 Darwin
// 25/XNU12377 release family whose shared recount paths were reviewed. This is
// a family compatibility check, not attestation of a running kernel binary.
// See apple-oss-distributions/xnu tag xnu-12377.1.9, osfmk/kern/{processor,
// recount,task,clock}.c and osfmk/arm/rtclock.c.
var cpuDarwinRelease = regexp.MustCompile(`^25\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)
var cpuDarwinKernel = regexp.MustCompile(`^Darwin Kernel Version (25\.[0-9]{1,6}\.[0-9]{1,6}): [^\r\n]{1,160}; root:xnu-12377\.[0-9]{1,8}\.[0-9]{1,8}~[0-9]{1,8}/RELEASE_ARM64_[A-Z0-9_]{1,32}$`)

func darwinCPUAccountingFamily(arch, release, version string, translated int32) bool {
	if arch != "arm64" || translated != 0 || len(version) > 512 || !cpuDarwinRelease.MatchString(release) {
		return false
	}
	m := cpuDarwinKernel.FindStringSubmatch(version)
	return len(m) == 2 && m[1] == release
}
func cpuMachQuantum(numer, denom uint32) (uint64, error) {
	if numer == 0 || denom == 0 || uint64(denom) > math.MaxUint64/1_000_000_000 {
		return 0, fmt.Errorf("native CPU timebase unavailable")
	}
	product := uint64(denom) * 1_000_000_000
	if product%uint64(numer) != 0 {
		return 0, fmt.Errorf("native CPU frequency conversion is not exact")
	}
	frequency := product / uint64(numer)
	if frequency == 0 || frequency > math.MaxUint32 || frequency/100 == 0 {
		return 0, fmt.Errorf("native CPU clock quantum unsupported")
	}
	return frequency / 100, nil
}
