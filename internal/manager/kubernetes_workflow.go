package manager

import (
	"getarm.org/internal/kubejournal"
	"getarm.org/internal/kubesource"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

// PrepareKubernetesWorkflow is an unused native entrypoint. The compatibility
// fence ACK precedes the first protected workflow file, including install's
// initially unchanged s profile. No server/CLI/browser adapter calls it yet.
func (m *Manager) PrepareKubernetesWorkflow(record kubejournal.Record) (*mesh.CoordinationGuard, error) {
	if record.Validate() != nil || record.SourceHost != m.host || record.Owner != m.owner {
		return nil, model.Err("invalid_request", "workflow owner differs from the original Manager")
	}
	m.mu.Lock()
	if m.closing || m.draining {
		m.mu.Unlock()
		return nil, model.Err("backend_stopping", "workflow admission closed")
	}
	tx, err := m.db.Begin()
	if err == nil {
		err = ensureKubernetesSourceContractTx(tx, kubesource.Scope{Stage: kubesource.Start})
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return mesh.ClaimKubernetesWorkflow(m.home, record)
}
