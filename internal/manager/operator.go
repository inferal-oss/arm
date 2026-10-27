package manager

import "getarm.org/internal/model"

func (m *Manager) LeaseAction(request model.LeaseAction) (model.Lease, error) {
	if request.ID == "" || request.Revision <= 0 {
		return model.Lease{}, model.Err("invalid_request", "lease ID and a positive reviewed revision are required")
	}
	if request.Action != "renew" && request.TTL != 0 {
		return model.Lease{}, model.Err("invalid_request", "only renewal accepts ttl_ns")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return model.Lease{}, model.Err("backend_stopping", "backend is shutting down")
	}
	l, err := m.get(request.ID)
	if err != nil {
		return l, err
	}
	if l.Revision != request.Revision {
		return l, model.Err("revision_conflict", "lease changed; inspect its current state before confirming another action")
	}
	if request.Consumers != nil {
		if request.Action != "release" && request.Action != "cancel" {
			return l, model.Err("invalid_request", "consumer review applies only to release or cancellation")
		}
		ls, err := m.list()
		if err != nil {
			return l, err
		}
		expected := make(map[string]int, len(request.Consumers))
		for _, reviewed := range request.Consumers {
			if reviewed.ID == "" || reviewed.Revision <= 0 || expected[reviewed.ID] != 0 {
				return l, model.Err("invalid_request", "reviewed consumers require distinct IDs and positive revisions")
			}
			expected[reviewed.ID] = reviewed.Revision
		}
		for _, consumer := range ls {
			if (consumer.Request.StorageLease == l.ID || consumer.Request.PortLease == l.ID) && (consuming(consumer.State) || consumer.State == "queued") {
				if expected[consumer.ID] != consumer.Revision {
					return l, model.Err("revision_conflict", "attached leases changed; refresh and review the affected work before confirming")
				}
				delete(expected, consumer.ID)
			}
		}
		if len(expected) != 0 {
			return l, model.Err("revision_conflict", "attached leases changed; refresh and review the affected work before confirming")
		}
	}
	switch request.Action {
	case "release":
		return m.end(l.ID, "released")
	case "cancel":
		return m.end(l.ID, "cancelled")
	case "renew":
		return m.renew(l.ID, request.TTL)
	case "reconcile":
		return m.reconcile(l.ID)
	case "storage_cleanup":
		return m.storageCleanup(l.ID)
	case "storage_handoff":
		return m.storageHandoff(l.ID)
	default:
		return l, model.Err("invalid_request", "unsupported lease action")
	}
}
