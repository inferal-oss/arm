//go:build !arm_devtrace

package nodestop

func traceBoundary(*Boundary, string, *Plan, error) {}
func traceClosure(*Closure, *Closed, error)         {}
