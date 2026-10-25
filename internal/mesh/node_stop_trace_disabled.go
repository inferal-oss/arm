//go:build !arm_devtrace

package mesh

func traceNodeStopGrantRefused(*Store, string, error) {}
