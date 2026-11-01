package manager

// Routing metadata only. This blocks legacy lifecycle/accounting bypasses while
// an original resident owner is retained; it never grants native authority.
func (m *Manager) hasResidentOwner() bool {
	return m != nil && (m.resident != nil || m.residentWorker != nil || m.residentService != nil || m.residentWorkerService != nil)
}
