//go:build arm_devtrace

package manager

import (
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

// traceNodeStop is always called under the actual Manager.mu, after the real
// write/lookup cut and before another manager transition can modify its rows.
func (m *Manager) traceNodeStop(kind string) {
	if !nodestop.DevEnabled(m.home) {
		return
	}
	leases, err := m.list()
	out := struct {
		Ledger nodestop.DevLedger `json:"ledger"`
		Leases []model.Lease      `json:"leases"`
		Clock  nodestop.DevClock  `json:"clock"`
		Error  string             `json:"error"`
	}{Ledger: nodestop.DevReadLedger(m.db), Leases: leases, Clock: m.nodeStopBoundary.DevClockSnapshot()}
	if err != nil {
		out.Error = err.Error()
	}
	nodestop.DevEmit(m.home, "manager."+kind, out)
}
func (m *Manager) DevNodeStopSnapshot(kind string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traceNodeStop(kind)
}
func (m *Manager) DevNodeStopClock(tick int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeStopBoundary.DevClockControl(tick, err)
}

func (m *Manager) DevRepeatNodeStopPlan(p nodestop.Plan) (nodestop.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodeStopBoundary.Prepare(p.Key, p.Actor, p.DesiredRevision, p.Policy, p.Reason, p.Impact, p.Observed)
}
