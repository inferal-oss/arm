package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/storage"
)

func (m *Manager) loadStorage() {
	var raw string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='storage_root'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	var identity model.FileIdentity
	if err == nil {
		err = json.Unmarshal([]byte(raw), &identity)
	}
	if err == nil {
		m.storage, err = storage.Open(filepath.Join(m.home, "storage"), &identity)
	}
	m.storageErr = err
}
func (m *Manager) ensureStorage() error {
	if m.storageErr != nil {
		return m.storageErr
	}
	if m.storage != nil {
		return nil
	}
	p, err := storage.Open(filepath.Join(m.home, "storage"), nil)
	if err != nil {
		m.storageErr = err
		return err
	}
	identity, err := json.Marshal(p.Identity())
	if err == nil {
		_, err = m.db.Exec(`INSERT INTO metadata(key,value) VALUES('storage_root',?)`, string(identity))
	}
	if err != nil {
		p.Close()
		m.storageErr = err
		return err
	}
	m.storage = p
	return nil
}

func (m *Manager) provisionStorage(l model.Lease) (model.Lease, error) {
	if reason := m.startDeadlineProblem(l); reason != nil {
		return m.rejectUnstarted(l, reason)
	}
	if err := m.ensureStorage(); err != nil {
		return m.storageFailed(l, err)
	}
	l.ManagedDir = m.storage.Path(l.ID)
	l.Artifact = &model.Artifact{Name: l.ID, Path: l.ManagedDir, Phase: "creating", Policy: "exclusive-managed-directory; unknown reclamation remains charged", Updated: time.Now().UTC()}
	l.Revision++
	// The intention is durable before mkdir. A directory found after a crash
	// without its recorded identity is never adopted or deleted automatically.
	if err := m.put(l, "storage.creating"); err != nil {
		return l, err
	}
	id, err := m.storage.Create(l.ID)
	if err != nil {
		return m.storageFailed(l, err)
	}
	l.Artifact.Identity = &id
	l.Artifact.Phase = "owned"
	if reason := m.startDeadlineProblem(l); reason != nil {
		return m.rejectUnstarted(l, reason)
	}
	l.State = "active"
	l.Capabilities = append(append([]string{}, l.Capabilities...), "managed-storage", "explicit-storage-consumers")
	l.Revision++
	return l, m.put(l, "storage.created")
}
func (m *Manager) storageFailed(l model.Lease, err error) (model.Lease, error) {
	l.Failure = err.Error()
	l.State = "needs_attention"
	if l.Outcome == "" {
		l.Outcome = "storage_unresolved"
	}
	l.Revision++
	if l.Artifact != nil {
		l.Artifact.LastError = err.Error()
		l.Artifact.Updated = time.Now().UTC()
	}
	if putErr := m.put(l, "storage.needs_attention"); putErr != nil {
		return l, putErr
	}
	return l, nil
}
func (m *Manager) storageForRun(r model.Request) (model.Lease, error) {
	l, err := m.get(r.StorageLease)
	if err != nil {
		return l, err
	}

	if l.Request.Kind != "storage" || l.Owner != m.owner || l.Host != m.host || l.State != "active" || l.Expires == nil || time.Now().Add(r.TTL).After(*l.Expires) || l.Artifact == nil || l.Artifact.Phase != "owned" {
		return l, model.Err("storage_unavailable", "storage must be active, owned by this host/user, and outlast the command's TTL")
	}
	if m.storage == nil {
		return l, model.Err("storage_unavailable", "managed storage provider is unavailable")
	}
	if export, err := m.storageExportBusy(l.ID); err != nil {
		return l, err
	} else if export != "" {
		return l, model.Err("operation_busy", "storage export "+export+" must settle, reconcile or be handed off before attaching new work")
	}
	if err := m.storage.Check(*l.Artifact); err != nil {
		return l, model.Err("storage_unavailable", err.Error())
	}
	return l, nil
}
func storageConsumers(ls []model.Lease, id string) []string {
	consumers := []string{}
	for _, l := range ls {
		if l.Request.StorageLease == id && (consuming(l.State) || l.State == "queued") {
			consumers = append(consumers, l.ID)
		}
	}
	return consumers
}
func (m *Manager) endStorage(l model.Lease, outcome string) (model.Lease, error) {

	if l.State == "queued" {
		now := time.Now().UTC()
		l.State = "terminal"
		l.Outcome = outcome
		l.Ended = &now
		l.Revision++
		return l, m.put(l, "lease."+outcome)
	}
	if export, err := m.storageExportBusy(l.ID); err != nil {
		return l, err
	} else if export != "" && outcome != "expired" {
		return l, model.Err("operation_busy", "storage export "+export+" must settle, reconcile or be handed off before release")
	}
	l.State = "ending"
	l.Outcome = outcome
	l.Revision++
	if err := m.put(l, "storage.ending"); err != nil {
		return l, err
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	for _, id := range storageConsumers(ls, l.ID) {
		// Only attached ARM-supervised executions can be terminated here.
		// A recovered or otherwise uncertain consumer stays charged and blocks
		// the storage cleanup; no persisted PID authorizes a signal.
		_, _ = m.end(id, "revoked")
	}
	m.scheduleStorage(l.ID)
	return l, nil
}
func (m *Manager) scheduleStorage(id string) {
	if m.closing || m.storageJobs[id] {
		return
	}
	m.storageJobs[id] = true
	m.wg.Go(func() {
		defer func() { m.mu.Lock(); delete(m.storageJobs, id); m.mu.Unlock() }()
		m.cleanupStorage(id)
	})
}

func (m *Manager) cleanupStorage(id string) {
	m.mu.Lock()
	l, err := m.get(id)
	if err != nil || l.State != "ending" {
		m.mu.Unlock()
		return
	}

	if export, e := m.storageExportBusy(id); e != nil || export != "" {
		if e != nil {
			_, _ = m.storageFailed(l, e)
		} else if !m.storageJobs[exportJobKey(id)] {
			_, _ = m.storageFailed(l, fmt.Errorf("export %s is unresolved; reconcile it or explicitly hand off uncertain output before cleanup", export))
		}
		m.mu.Unlock()
		return
	}
	ls, err := m.list()
	if err != nil {
		m.mu.Unlock()
		return
	}
	consumers := storageConsumers(ls, id)
	if len(consumers) > 0 {
		for _, c := range ls {
			if c.Request.StorageLease == id && c.State == "needs_attention" {
				_, _ = m.storageFailed(l, fmt.Errorf("consumer %s needs reconciliation", c.ID))
				break
			}
		}
		m.mu.Unlock()
		return
	}
	p := m.storage
	if p == nil || l.Artifact == nil || l.Artifact.Identity == nil {
		_, _ = m.storageFailed(l, fmt.Errorf("storage ownership or provider is unavailable; no deletion attempted"))
		m.mu.Unlock()
		return
	}
	a := *l.Artifact
	m.mu.Unlock()
	// The preflight and the post-quarantine step each perform an independently
	// bounded activity check. One shared three-second deadline could expire
	// solely because two healthy native queries ran back to back.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if a.Phase != "removed" {
		var survey storage.Survey
		survey, err = p.Survey(ctx, a)
		if err == nil && len(survey.Active) > 0 {
			err = fmt.Errorf("active open files or working directories: %v", survey.Active)
		}
		if err == nil && a.AllocatedBefore == nil {
			a.AllocatedBefore = &survey.Allocated
			a.ContentObserved = survey.Entries > 0
			m.mu.Lock()
			l.Artifact = &a
			l.Revision++
			err = m.put(l, "storage.cleanup_planned")
			m.mu.Unlock()
		}
		if err == nil && a.Phase != "removed" {
			err = p.Quarantine(a)
			if err == nil {
				a.Phase = "quarantined"
				a.Path = p.Path(".reclaim-" + a.Name)
				a.Updated = time.Now().UTC()
				m.mu.Lock()
				l.Artifact = &a
				l.Revision++
				err = m.put(l, "storage.quarantined")
				m.mu.Unlock()
			}
		}
		if err == nil && a.Phase != "removed" {
			var count int
			var done bool
			count, done, err = p.Step(ctx, a, 128, func() error {
				if a.ContentObserved {
					return nil
				}
				// A file may appear after the initial survey. Record uncertainty
				// before unlinking even an empty or sparse file or a directory.
				a.ContentObserved = true
				m.mu.Lock()
				defer m.mu.Unlock()
				l.Artifact = &a
				l.Revision++
				return m.put(l, "storage.content_observed")
			})
			a.DeletedEntries += count
			if done {
				a.Phase = "removed"
				a.NamespaceRemoved = true
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l.Artifact = &a
	l.Artifact.Updated = time.Now().UTC()
	l.Revision++
	if err != nil {
		_, _ = m.storageFailed(l, err)
		return
	}
	if a.Phase == "removed" {
		if a.AllocatedBefore == nil || *a.AllocatedBefore > 0 || a.ContentObserved {
			_, _ = m.storageFailed(l, fmt.Errorf("directory entries removed; exclusive block reclamation remains unverified (clones, snapshots and external links may retain storage); use explicit handoff to assume the remaining responsibility"))
			return
		}
		now := time.Now().UTC()
		l.State = "terminal"
		l.Ended = &now
	}
	_ = m.put(l, "storage.cleanup_progress")
}

func (m *Manager) StorageList() ([]model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.list()
	if err != nil {
		return nil, err
	}
	result := []model.Lease{}
	for _, l := range ls {
		if l.Request.Kind == "storage" {
			result = append(result, l)
		}
	}
	return result, nil
}
func (m *Manager) StoragePreview(ctx context.Context, id string) (model.StoragePreview, error) {
	m.mu.Lock()
	l, err := m.get(id)
	if err != nil {
		m.mu.Unlock()
		return model.StoragePreview{}, err
	}
	if l.Request.Kind != "storage" {
		m.mu.Unlock()
		return model.StoragePreview{}, model.Err("invalid_request", "not a storage lease")
	}
	ls, err := m.list()
	p := m.storage
	preview := model.StoragePreview{Lease: l, Consumers: storageConsumers(ls, id), ActivePIDs: []int{}, Detail: "Read-only preview; allocated blocks do not establish exclusive or reclaimable bytes"}

	if export, e := m.storageExportBusy(id); e != nil || export != "" {
		if e != nil {
			m.mu.Unlock()
			return preview, e
		}
		preview.Detail += "; storage export " + export + " must settle or receive explicit recovery/handoff before cleanup"
		m.mu.Unlock()
		return preview, nil
	}
	if m.storageJobs[id] {
		preview.Detail += "; cleanup is in progress"
		m.mu.Unlock()
		return preview, nil
	}
	m.mu.Unlock()
	if err != nil {
		return preview, err
	}
	if p == nil || l.Artifact == nil || l.Artifact.Identity == nil {
		preview.Detail += "; verified ownership unavailable"
		return preview, nil
	}
	if l.Artifact.Phase == "removed" || l.Artifact.Phase == "handed_off" {
		preview.Detail += "; no managed directory cleanup remains"
		return preview, nil
	}
	survey, err := p.Survey(ctx, *l.Artifact)
	if err != nil {
		preview.Detail += "; " + err.Error()
		return preview, nil
	}
	preview.Entries = survey.Entries
	preview.Allocated = survey.Allocated
	preview.ActivePIDs = survey.Active
	preview.CanClean = len(preview.Consumers) == 0 && len(survey.Active) == 0
	return preview, nil
}
func (m *Manager) StorageCleanup(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storageCleanup(id)
}
func (m *Manager) storageCleanup(id string) (model.Lease, error) {
	l, err := m.get(id)
	if err != nil {
		return l, err
	}
	if l.Request.Kind != "storage" {
		return l, model.Err("invalid_request", "not a storage lease")
	}
	if l.State == "terminal" || l.State == "ending" {
		return l, nil
	}
	if l.State == "needs_attention" {
		l.State = "active"
	}
	outcome := l.Outcome
	if outcome == "" {
		outcome = "released"
	}
	return m.endStorage(l, outcome)
}
func (m *Manager) StorageHandoff(id string) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storageHandoff(id)
}
func (m *Manager) storageHandoff(id string) (model.Lease, error) {
	l, err := m.get(id)
	if err != nil {
		return l, err
	}
	if l.Request.Kind != "storage" {
		return l, model.Err("invalid_request", "not a storage lease")
	}

	if l.State == "terminal" {
		return l, nil
	}
	if m.storageJobs[id] {
		return l, model.Err("operation_busy", "wait for the current cleanup step to finish before handoff")
	}
	if export, err := m.storageExportBusy(id); err != nil {
		return l, err
	} else if export != "" {
		return l, model.Err("operation_busy", "resolve or hand off storage export "+export+" before handing off its source allocation")
	}
	ls, err := m.list()
	if err != nil {
		return l, err
	}
	if len(storageConsumers(ls, id)) > 0 {
		return l, model.Err("storage_in_use", "finish or cancel attached consumers before handoff")
	}
	// An explicit operator handoff ends ARM's cleanup promise, not a claim that
	// disk blocks became free. Keep paths, identity and prior errors as evidence.
	if l.Artifact != nil {
		l.Artifact.Phase = "handed_off"
		l.Artifact.Policy = "user-retained; ARM has no further cleanup authority"
		l.Artifact.Updated = time.Now().UTC()
	}
	now := time.Now().UTC()
	l.State = "terminal"
	l.Outcome = "handed_off"
	l.Ended = &now
	l.Revision++
	return l, m.put(l, "storage.handed_off")
}
