//go:build !arm_devtrace

package agents

// Native-entry observations exist only in dev builds.
func traceNativeDelivery(Contact) {}
