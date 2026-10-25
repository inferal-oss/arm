//go:build !arm_devtrace

package manager

import "database/sql"

func (m *Manager) traceSnapshotLocked(string, string)                     {}
func (m *Manager) traceSnapshotTransactionLocked(*sql.Tx, string, string) {}

func (m *Manager) traceSnapshotCut(string, string, snapshotTraceCut) {}

func (m *Manager) traceSnapshotMethod(string, snapshotTraceCut) {}
