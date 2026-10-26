//go:build !arm_devtrace

package sshservice

import "context"

func historyCleanupObserve(string, context.Context, string, SourceHistoryOperation, *historyWorker, int) {
}
