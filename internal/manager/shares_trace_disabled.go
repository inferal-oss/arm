//go:build !arm_devtrace

package manager

import (
	"context"
	"database/sql"
)

// Production builds contain no trace registry, output sink or activation path.
func (*Manager) traceShareLocked(string, string)                   {}
func (*Manager) traceShareNative(string, string)                   {}
func (*Manager) traceShareBarrier(context.Context, string, string) {}
func (*Manager) traceShareBeforeCommit(*sql.Tx)                    {}
