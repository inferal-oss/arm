package manager

import (
	"context"
	"os"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
	"github.com/yrashk/arm/internal/usage"
)

// publishSnapshot is a fixed-size phase/lifetime check under mu. It performs no
// complete manifest decode or native I/O. One generation has one publish intent.
func (m *Manager) publishSnapshot(q sharelease.PublishRequest) (sharelease.Publication, bool, error) {
	if err := m.sharePolicyError(); err != nil {
		return sharelease.Publication{}, false, err
	}
	for _, p := range m.shares.state.Publications {
		if p.Publish != nil && p.Publish.Key == q.Key {
			if *p.Publish != q {
				return clonePublication(p), true, model.Err("idempotency_conflict", "publication key records a different exact request")
			}
			return clonePublication(p), true, nil
		}
	}
	p, ok := m.shares.state.Publications[q.Share]
	if !ok {
		return clonePublication(p), false, model.Err("share_not_found", "publication not found")
	}
	if p.Publish != nil {
		return clonePublication(p), false, model.Err("idempotency_conflict", "generation already has its single publication decision; inspect its original key")
	}
	if m.closing || m.draining {
		return clonePublication(p), false, model.Err("backend_stopping", "publication unavailable while backend stops/drains")
	}
	if p.Revision != q.Revision || p.Generation != q.Generation || p.Summary == nil || p.Summary.SHA256 != q.PreparedSHA256 {
		return clonePublication(p), false, model.Err("revision_conflict", "publication approval differs from exact prepared generation/revision/hash")
	}
	if p.State != "prepared" || p.WorkerState != "joined" || m.shares.workers[p.ID] != nil || !time.Now().Before(p.Expires) {
		return clonePublication(p), false, model.Err("operation_busy", "publication requires joined prepared work and its current fixed lifetime")
	}
	if err := m.sharePublicationConsumersEmpty(p); err != nil {
		return clonePublication(p), false, err
	}
	l, err := m.get(p.Lease)
	if err == nil {
		err = m.checkPublicationLease(p, l)
	}
	return clonePublication(p), false, err
}

// PublishShare admits an in-process full filesystem verification against the
// continuing ordinary lease. It starts only after its immutable intent commits.
// This method exposes no listener, data service, transport or consumer authority.
func (m *Manager) PublishShare(ctx context.Context, q sharelease.PublishRequest) (sharelease.Publication, error) {
	var empty sharelease.Publication
	if err := q.Validate(); err != nil {
		return empty, model.Err("invalid_request", err.Error())
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	p, retry, err := m.publishSnapshot(q)
	if retry || err != nil {
		m.mu.Unlock()
		return p, err
	}
	provider := m.storage
	source, err := m.get(p.Request.Storage)
	if err != nil || provider == nil || source.Artifact == nil {
		m.mu.Unlock()
		return empty, model.Err("storage_unavailable", "exact backing storage unavailable")
	}
	control, finish, err := m.beginShareControl(ctx)
	m.mu.Unlock()
	if err != nil {
		return empty, err
	}
	defer finish()
	ctx = control
	// Fixed-size native authority and current account fingerprint, outside mu.
	file, err := provider.OpenArtifactReadOnly(ctx, *source.Artifact)
	if err != nil {
		return empty, model.Err("storage_unavailable", err.Error())
	}
	if err = file.Close(); err != nil {
		return empty, err
	}
	native, err := usage.Capture(os.Getpid())
	if err != nil {
		return empty, model.Err("observation_unavailable", err.Error())
	}
	fingerprint := sharelease.Worker{PID: native.PID, PGID: native.PGID, UID: native.UID, Start: native.Start, Namespace: native.Namespace}
	if err = fingerprint.Validate(p.Owner); err != nil {
		return empty, err
	}
	m.mu.Lock()
	current, retry, err := m.publishSnapshot(q)
	if retry || err != nil {
		m.mu.Unlock()
		return current, err
	}
	currentSource, err := m.get(p.Request.Storage)
	if err != nil || provider != m.storage || currentSource.Revision != source.Revision || !reflect.DeepEqual(currentSource.Artifact, source.Artifact) {
		m.mu.Unlock()
		return empty, model.Err("revision_conflict", "backing storage changed during publication preflight")
	}
	if err = ctx.Err(); err != nil {
		m.mu.Unlock()
		return empty, err
	}
	p = current
	p.Publish = &q
	p.Worker = fingerprint
	p.WorkerPhase = "verification"
	p.WorkerState = "reserved"
	p.State = "verifying"
	p.Revision++
	p.JournalSlots--
	p.Updated = shareMaxTime(p.Updated, time.Now().UTC())
	p.Reason = q.Reason
	lease, err := m.get(p.Lease)
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	lease.Revision++
	// Continue the exact fixed reservation and pin without release/reacquire.
	if err = m.putSharePublication(p, lease, "verify_reserved", nil); err != nil {
		m.mu.Unlock()
		return empty, err
	}
	duration := max(time.Duration(0), time.Until(p.Expires))
	lifetime, cancel := context.WithTimeout(context.Background(), duration)
	w := &shareWorker{ctx: lifetime, cancel: cancel, initial: ctx, started: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}), provider: provider, artifact: *source.Artifact}
	m.shares.workers[p.ID] = w
	m.tracePublicationLocked("verify_intent", p.ID)
	m.wg.Go(func() { m.runSharePreparation(p.ID, w) })
	m.mu.Unlock()
	select {
	case <-w.started:
		return m.InspectShare(p.ID)
	case <-ctx.Done():
		w.requestStop()
		return clonePublication(p), model.Err("share_outcome_unknown", "verification intent may have committed; inspect its exact key before any replacement request")
	}
}

func (m *Manager) LookupSharePublication(key string) (sharelease.Publication, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.sharePolicyError(); err != nil {
		return sharelease.Publication{}, false, err
	}
	if len(key) < 1 || len(key) > 128 || !utf8.ValidString(key) {
		return sharelease.Publication{}, false, model.Err("invalid_request", "bounded publication key required")
	}
	// Reuse exact bounded key validation; no fake native request is admitted.
	for _, p := range m.shares.state.Publications {
		if p.Publish != nil && p.Publish.Key == key {
			return clonePublication(p), true, nil
		}
	}
	return sharelease.Publication{}, false, nil
}

// This stage has no consumer admission or runtime session reconstruction. Any
// retained consumer/fence blocks retirement, including a settled-looking row.
// Only positively empty validated consumer evidence establishes this boundary.
func (m *Manager) sharePublicationConsumersEmpty(p sharelease.Publication) error {
	if err := m.sharePolicyError(); err != nil {
		return err
	}
	for _, c := range m.shares.state.Consumers {
		if c.Admission.Control.Share == p.ID {
			return model.Err("needs_attention", "publication has consumer history; its joined-session retirement boundary is not enabled")
		}
	}
	for _, f := range m.shares.state.Fences {
		if f.Control.Share == p.ID {
			return model.Err("needs_attention", "publication has consumer fences; retirement requires the consumer boundary")
		}
	}
	return nil
}
