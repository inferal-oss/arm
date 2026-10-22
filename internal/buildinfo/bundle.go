package buildinfo

import (
	"fmt"
	"strconv"
)

// BundleTag is injected only by the distribution builder. It is integrity/build
// metadata, not a release signature. Ordinary builds intentionally leave it empty.
var BundleTag string

func ExpectedBundleTag(protocol int) string {
	return "arm-bundle-v1:" + Version + ":" + strconv.Itoa(protocol)
}
func ValidateBundleTag(protocol int) error {
	if BundleTag != "" && BundleTag != ExpectedBundleTag(protocol) {
		return fmt.Errorf("bundle build metadata differs from compiled ARM version/protocol")
	}
	return nil
}
