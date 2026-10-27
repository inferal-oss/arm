package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func (m *Manager) Done() <-chan struct{} { return m.stop }

func (m *Manager) ShutdownActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.draining && !m.closing
}

func (m *Manager) shutdownRecord() (model.ShutdownReport, error) {
	var report model.ShutdownReport
	var b []byte
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='shutdown'`).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return report, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &report)
	}
	return report, err
}

func (m *Manager) saveShutdown(report model.ShutdownReport, kind string) error {
	b, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveShutdownTx(tx, report, kind, b); err != nil {
		return err
	}
	return tx.Commit()
}

func saveShutdownTx(tx *sql.Tx, report model.ShutdownReport, kind string, encoded []byte) error {
	if _, err := tx.Exec(`INSERT INTO metadata(key,value) VALUES('shutdown',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(encoded)); err != nil {
		return err
	}
	return event(tx, kind, report.ID, report)
}

// Restart creates a fresh authority lifetime. Never silently carry a previous
// process's timer forward or imply its lost process supervision survived.
func (m *Manager) recoverShutdown() error {
	r, err := m.shutdownRecord()
	if err != nil {
		return err
	}
	if r.Phase == "draining" || r.Phase == "forcing" || r.Phase == "blocked" {
		r.Phase = "interrupted"
		r.Detail = "Previous shutdown was interrupted by backend restart; normal recovery guards still apply and admission has resumed."
		return m.saveShutdown(r, "shutdown.interrupted")
	}
	if r.Phase == "ready" {
		r.Phase = "completed"
		r.Detail = "Previous shutdown settled its lease obligations; a new backend authority is accepting work."
		return m.saveShutdown(r, "shutdown.completed")
	}
	return nil
}

func (m *Manager) BeginShutdown(request model.ShutdownRequest) (model.ShutdownReport, error) {
	m.pressureLaunchMu.Lock()
	defer m.pressureLaunchMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var r model.ShutdownReport
	if request.Force && request.Wait || request.Timeout < 0 || request.Timeout > 0 && !request.Wait {
		return r, model.Err("invalid_request", "force and wait are exclusive; a positive timeout requires wait")
	}
	if m.closing {
		return r, model.Err("backend_stopping", "backend is shutting down")
	}
	if m.draining {
		var err error
		r, err = m.shutdownRecord()
		if err != nil {
			return r, err
		}
		if request.Timeout > 0 && r.Phase == "draining" && request.Timeout != r.Timeout {
			return r, model.Err("shutdown_in_progress", "shutdown is already draining with its original deadline; reconnect without --timeout or use --force to release now")
		}
		if request.Force && r.Phase != "ready" && (!r.ForceApplied || r.Phase == "blocked") {
			r.Phase = "forcing"
			r.ForceApplied = false
			r.Deadline = nil
			r.Detail = "Operator requested immediate lease release."
			err = m.saveShutdown(r, "shutdown.force_requested")
			if err == nil {
				m.shutdownDeadline = time.Time{}
			}
		}
		return r, err
	}
	ls, err := m.list()
	if err != nil {
		return r, err
	}
	if !request.Force && !request.Wait {
		for _, l := range ls {
			if outstanding(l) {
				return r, model.Err("active_leases", "release leases and finish supervised work before stopping, or use --wait or --force")
			}
		}
	}
	r, m.shutdownDeadline = m.newShutdownRecord(request, ls)
	// Persist the intention before cancellation; a restart explicitly journals
	// interruption instead of pretending this process-owned drain survived.
	if err = m.saveShutdown(r, "shutdown.started"); err != nil {
		return r, err
	}
	m.draining = true
	m.cancelShutdownQueue(&r)
	if err = m.saveShutdown(r, "shutdown.queue_cancelled"); err != nil {
		r.Detail = "Shutdown started; initial queue cancellation reporting failed. The backend continues and retries queued cancellations; inspect this shutdown's status."
		_ = m.saveShutdown(r, "shutdown.initialization_incomplete")
		return r, model.Err("shutdown_in_progress", "shutdown "+r.ID+" started but initial queue cancellation reporting failed; backend continuation remains active")
	}
	return r, nil
}

// newShutdownRecord only constructs the original ordinary drain intent. Caller
// holds Manager.mu; no cancellation/provider/native effect happens here.
func (m *Manager) newShutdownRecord(request model.ShutdownRequest, ls []model.Lease) (model.ShutdownReport, time.Time) {
	r := model.ShutdownReport{ID: local.ID(), Actor: m.owner, ActorUsername: m.ownerUsername, Phase: "draining", Started: time.Now().UTC(), CancelledQueued: []model.LeaseResult{}, Results: []model.LeaseResult{}, Remaining: []model.LeaseResult{}, Detail: "Admission and renewal are paused; existing work can finish or expire. Queued requests are cancelled."}
	deadline := time.Time{}
	if request.Timeout > 0 {
		r.Timeout = request.Timeout
		deadline = time.Now().Add(request.Timeout)
		wallDeadline := deadline.UTC()
		r.Deadline = &wallDeadline
	}
	if request.Force {
		r.Phase = "forcing"
		r.Detail = "Releasing leases through their normal providers; unresolved resources remain charged."
	}
	for _, l := range ls {
		if l.State == "queued" && l.Host == m.host && l.Owner == m.owner {
			r.CancelledQueued = append(r.CancelledQueued, leaseResult(l, nil))
		}
	}
	return r, deadline
}

// Scope is saved before any queued request is changed. Partial failures can be
// retried by the backend even if the initiating request has already failed.
func (m *Manager) cancelShutdownQueue(r *model.ShutdownReport) bool {
	changed := false
	for i, item := range r.CancelledQueued {
		l, err := m.get(item.ID)
		if err == nil && l.State == "queued" && l.Host == m.host && l.Owner == m.owner {
			l, err = m.end(l.ID, "cancelled")
		}
		updated := leaseResult(l, err)
		updated.ID = item.ID
		before, _ := json.Marshal(item)
		after, _ := json.Marshal(updated)
		if string(before) != string(after) {
			r.CancelledQueued[i] = updated
			changed = true
		}
	}
	return changed
}

func (m *Manager) ShutdownStatus() (model.ShutdownReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.shutdownRecord()
	if err != nil {
		return r, err
	}
	return m.refreshShutdown(r)
}

func (m *Manager) refreshShutdown(r model.ShutdownReport) (model.ShutdownReport, error) {
	ls, err := m.list()
	if err != nil {
		return r, err
	}
	r.Remaining = []model.LeaseResult{}
	for _, l := range ls {
		if outstanding(l) {
			r.Remaining = append(r.Remaining, leaseResult(l, nil))
		}
	}
	for _, items := range [][]model.LeaseResult{r.CancelledQueued, r.Results} {
		for i := range items {
			if current, err := m.get(items[i].ID); err == nil {
				previousError := items[i].Error
				items[i] = leaseResult(current, nil)
				items[i].Error = previousError
			}
		}
	}
	return r, nil
}

// AdvanceShutdown is driven by a backend-owned worker, independent of any
// requesting WebSocket or CLI lifetime. It never signals a saved/recovered PID.
func (m *Manager) AdvanceShutdown() (model.ShutdownReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodeStopUnknown {
		return model.ShutdownReport{}, model.Err("node_stop_outcome_unknown", "stop commit requires exact original-key reconciliation; no provider effects dispatched")
	}
	r, err := m.shutdownRecord()
	if err != nil || !m.draining || m.closing {
		return r, err
	}
	previous := r.Phase
	previousForce := r.ForceApplied
	queueChanged := m.cancelShutdownQueue(&r)
	if r.Phase == "draining" && !m.shutdownDeadline.IsZero() && !time.Now().Before(m.shutdownDeadline) {
		r.Phase = "forcing"
		r.Detail = "Wait timeout elapsed; releasing remaining leases through their normal providers."
	}
	if r.Phase == "forcing" && !r.ForceApplied {
		bulk, err := m.bulkEnd(model.BulkEndRequest{Action: "release", Key: "shutdown-" + r.ID})
		if err != nil {
			return r, err
		}
		r.Results = bulk.Results
		r.ForceApplied = true
	}
	r, err = m.refreshShutdown(r)
	if err != nil {
		return r, err
	}
	if len(r.Remaining) == 0 {
		r.Phase = "ready"
		r.Detail = "All lease obligations settled; backend is stopping."
	} else if r.ForceApplied {
		failed := map[string]bool{}
		for _, result := range r.Results {
			if result.Error != nil {
				failed[result.ID] = true
			}
		}
		r.Phase = "blocked"
		for _, item := range r.Remaining {
			if item.State != "needs_attention" && !failed[item.ID] {
				r.Phase = "forcing"
				break
			}
		}
		if r.Phase == "blocked" {
			r.Detail = "Some releases failed or remain unverified and charged. Inspect the per-lease results, reconcile or hand off retained resources, then retry arm stop --force if needed. Backend inspection remains available."
		}
	}
	if r.Phase != previous || r.ForceApplied != previousForce || queueChanged {
		err = m.saveShutdown(r, "shutdown."+r.Phase)
	}
	return r, err
}
