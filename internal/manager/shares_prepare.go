package manager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/sharelease"
	"github.com/yrashk/arm/internal/storage"
	"github.com/yrashk/arm/internal/usage"
)

// A persisted Worker fingerprint cannot reconstruct this runtime-only handle.
// stop only requests cancellation; charges remain until the native worker joins.
type shareWorker struct {
	initial  context.Context
	started  chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	stop     chan struct{}
	stopped  sync.Once
	done     chan struct{}
	provider *storage.Provider
	artifact model.Artifact
}

func (w *shareWorker) requestStop() { w.stopped.Do(func() { close(w.stop) }) }

// PrepareShare performs only fixed-size native storage/process verification
// before admission. Complete traversal/hash/encoding begins AFTER the ordinary
// lease, pin, intent and finalization reservations commit atomically.
func (m *Manager) PrepareShare(ctx context.Context, q sharelease.PrepareRequest) (sharelease.Publication, error) {
	var empty sharelease.Publication
	if err := q.Validate(); err != nil {
		return empty, model.Err("invalid_request", err.Error())
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	m.mu.Lock()
	q.Budget = m.canonicalBudgetSelector(q.Budget)
	if old, found, err := m.shareRetry(q); found || err != nil {
		m.mu.Unlock()
		return old, err
	}
	source, err := m.shareStorageSource(q)
	provider := m.storage
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	control, finish, err := m.beginShareControl(ctx)
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	m.mu.Unlock()
	defer finish()
	ctx = control
	if err != nil {
		return empty, err
	}
	// Runtime authority is captured outside Manager.mu. No wire path is opened.
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
	workerID := sharelease.Worker{PID: native.PID, PGID: native.PGID, UID: native.UID, Start: native.Start, Namespace: native.Namespace}
	if err = workerID.Validate(source.Owner); err != nil {
		return empty, err
	}
	m.mu.Lock()
	if old, found, e := m.shareRetry(q); found || e != nil {
		m.mu.Unlock()
		return old, e
	}
	current, err := m.shareStorageSource(q)
	if err != nil || provider != m.storage || current.Revision != source.Revision || !reflect.DeepEqual(current.Artifact, source.Artifact) {
		m.mu.Unlock()
		if err != nil {
			return empty, err
		}
		return empty, model.Err("revision_conflict", "storage changed during native verification; review its current revision")
	}
	admission := shareAdmission{preparation: &sharePrepareAdmission{ctx: ctx, request: q, source: source, provider: provider, worker: workerID}}
	request := model.Request{Host: m.host, Kind: "share-publication", Key: shareLeaseKey(m.host, q.Key), StorageLease: q.Storage, Resources: q.Resources, TTL: q.TTL, Reason: q.Reason, Budget: q.Budget}
	_, err = m.acquire(request, nil, admission)
	if err != nil {
		m.mu.Unlock()
		return empty, err
	}
	if admission.preparation.change == nil {
		m.mu.Unlock()
		return empty, model.Err("share_evidence_unavailable", "ordinary key exists without this share admission; no native work started")
	}
	publication := admission.preparation.change.next
	duration := min(q.TTL, time.Until(publication.Expires))
	if duration < 0 {
		duration = 0
	}
	lifetime, cancel := context.WithTimeout(context.Background(), duration)
	worker := &shareWorker{ctx: lifetime, cancel: cancel, initial: ctx, started: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}), provider: provider, artifact: *source.Artifact}
	m.shares.workers[publication.ID] = worker
	m.traceShareLocked("admit", publication.ID)
	m.wg.Go(func() { m.runSharePreparation(publication.ID, worker) })
	m.mu.Unlock()
	select {
	case <-worker.started:
		return m.InspectShare(publication.ID)
	case <-ctx.Done():
		worker.requestStop()
		return clonePublication(publication), model.Err("share_outcome_unknown", "preparation intent may have committed; inspect its exact key before any replacement request")
	}
}
func (m *Manager) InspectShare(id string) (sharelease.Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.sharePolicyError(); err != nil {
		return sharelease.Publication{}, err
	}
	p, ok := m.shares.state.Publications[id]
	if !ok {
		return p, model.Err("share_not_found", "publication not found")
	}
	return clonePublication(p), nil
}
func (m *Manager) LookupSharePreparation(key string) (sharelease.Publication, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.sharePolicyError(); err != nil {
		return sharelease.Publication{}, false, err
	}
	if len(key) < 1 || len(key) > 128 || !utf8.ValidString(key) {
		return sharelease.Publication{}, false, model.Err("invalid_request", "bounded preparation key required")
	}
	for _, p := range m.shares.state.Publications {
		if p.Request.Key == key {
			return clonePublication(p), true, nil
		}
	}
	return sharelease.Publication{}, false, nil
}

type sharePreparedResult struct {
	parts   []sharelease.ManifestPart
	summary sharelease.ManifestSummary
	err     error
}

func (m *Manager) runSharePreparation(id string, w *shareWorker) {
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-w.stop:
			w.cancel()
		case <-w.ctx.Done():
		}
	}()
	defer func() {
		w.cancel()
		<-watcherDone
		m.mu.Lock()
		delete(m.shares.workers, id)
		m.mu.Unlock()
		close(w.done)
	}()
	m.traceShareBarrier(w.ctx, id, "worker_start")
	m.mu.Lock()
	p, ok := m.shares.state.Publications[id]
	if !ok || m.shares.failure != nil {
		m.mu.Unlock()
		close(w.started)
		return
	}
	lease, err := m.get(p.Lease)
	if err == nil && p.State == "preparing" && !m.closing && w.ctx.Err() == nil && w.initial.Err() == nil && time.Now().Before(p.Expires) {
		_, err = m.shareStorageSourceForWorker(p, w)
		if err == nil {
			p.WorkerState = "running"
			p.Revision++
			p.JournalSlots--
			p.Updated = shareMaxTime(p.Updated, time.Now().UTC())
			p.Reason = "Admitted preparation worker begins the complete immutable manifest"
			lease.Revision++
			err = m.putSharePublication(p, lease, "worker_started", nil)
		}
	} else if err == nil {
		err = context.Canceled
	}
	if err != nil {
		m.mu.Unlock()
		close(w.started)
		m.traceShareNative(id, "native_skipped")
		m.traceShareBarrier(w.ctx, id, "settle")
		m.finishSharePreparation(id, w, sharePreparedResult{err: err})
		return
	}
	binding := sharefsBinding(p)
	beforeNative := m.shares.beforeNativeBuild
	m.mu.Unlock()
	// The caller's pre-admission context is checked once at this actual work gate;
	// once started, this detached leased operation follows its own TTL/stop scope.
	if err = w.initial.Err(); err != nil {
		close(w.started)
		m.traceShareNative(id, "native_skipped")
		m.traceShareBarrier(w.ctx, id, "settle")
		m.finishSharePreparation(id, w, sharePreparedResult{err: err})
		return
	}
	close(w.started)
	// A separate supervisor receives AND joins the native worker before writing
	// joined/terminal evidence or releasing any ordinary CPU/memory claim.
	result := make(chan sharePreparedResult, 1)
	nativeDone := make(chan struct{})
	go func() {
		defer close(nativeDone)
		m.traceShareBarrier(w.ctx, id, "native_begin")
		m.traceShareNative(id, "native_begin")
		if beforeNative != nil {
			beforeNative(w.ctx)
		}
		manifest, err := m.shares.engine.Build(w.ctx, w.provider, w.artifact, binding)
		if err != nil {
			result <- sharePreparedResult{err: err}
			return
		}
		parts, summary, err := sharelease.EncodeManifest(manifest)
		result <- sharePreparedResult{parts, summary, err}
	}()
	prepared := <-result
	<-nativeDone
	m.traceShareNative(id, "native_join")
	m.traceShareBarrier(w.ctx, id, "settle")
	m.finishSharePreparation(id, w, prepared)
}
func (m *Manager) shareStorageSourceForWorker(p sharelease.Publication, w *shareWorker) (model.Lease, error) {
	l, err := m.get(p.Request.Storage)
	if err != nil {
		return l, err
	}
	if m.storage != w.provider || l.State != "active" || l.Revision != p.Request.StorageRevision || l.Artifact == nil || l.Artifact.Identity == nil || *l.Artifact.Identity != p.StorageIdentity || l.Artifact.Phase != "owned" || l.Expires == nil || l.Expires.Before(p.Expires) {
		return l, model.Err("storage_unavailable", "preparation storage changed or its lifetime no longer covers publication")
	}
	return l, nil
}
func (m *Manager) finishSharePreparation(id string, w *shareWorker, result sharePreparedResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shares.failure != nil {
		return
	}
	p, ok := m.shares.state.Publications[id]
	if !ok || m.shares.workers[id] != w {
		return
	}
	lease, err := m.get(p.Lease)
	if err != nil {
		m.shares.failure = err
		return
	}
	if result.err == nil {
		if _, err = m.shareStorageSourceForWorker(p, w); err != nil {
			result.err = err
		}
	}
	cancelled := w.ctx.Err() != nil || m.closing || p.State == "ending" || !time.Now().Before(p.Expires)
	at := shareMaxTime(p.Updated, time.Now().UTC())
	p.Updated = at
	p.Revision++
	p.WorkerState = "joined"
	lease.Revision++
	transition := "prepared"
	if result.err != nil || cancelled {
		p.State = "failed"
		transition = "failed"
		lease.Outcome = "failed"
		if cancelled || errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			p.State = "cancelled"
			transition = "cancelled"
			lease.Outcome = "cancelled"
			if lease.Request.TTL > 0 && !time.Now().Before(p.Expires) {
				lease.Outcome = "expired"
			}
		}
		// Retain the explicit prior release/expiry/revoke decision.
		if old, err := m.get(p.Lease); err == nil && old.Outcome != "" {
			lease.Outcome = old.Outcome
		}
		p.Failure = shareDiagnostic(result.err)
		p.Reason = "Preparation worker joined without publishing data"
		p.Retired = &at
		p.JournalSlots = 0
		lease.State = "terminal"
		lease.Ended = &at
		result.parts = nil
	} else {
		p.State = "prepared"
		p.Summary = &result.summary
		p.Prepared = &at
		p.JournalSlots--
		p.Reason = "Complete bounded manifest prepared privately; explicit publication approval is required"
		lease.State = "active"
	}
	if err = m.putSharePublication(p, lease, transition, result.parts); err != nil {
		m.shares.failure = err
		return
	}
	if p.Retired != nil {
		m.resumeStorageAfterShare(p.Request.Storage)
	}
}
func shareDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToValidUTF8(err.Error(), "�")
	s = strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '?'
		}
		return r
	}, s)
	// Bound actual canonical JSON bytes as well as input UTF-8. A path/error
	// containing '<', '&', quotes or controls may expand substantially in JSON;
	// an admitted worker must always retain room for its final diagnostic.
	truncated := false
	for {
		raw, _ := json.Marshal(s)
		if len(raw) <= 3600 {
			break
		}
		s = s[:len(s)/2]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		truncated = true
	}
	if truncated {
		s += " [diagnostic truncated]"
	}

	return s
}

func sharefsBinding(p sharelease.Publication) sharefs.Binding {
	owner, _ := strconv.ParseUint(p.Owner, 10, 32)
	return sharefs.Binding{Authority: p.Authority, Owner: uint32(owner), Storage: p.Request.Storage, StorageRevision: int64(p.Request.StorageRevision), Share: p.ID, Generation: p.Generation}
}
func (m *Manager) resumeStorageAfterShare(id string) {
	if m.closing {
		return
	}
	l, err := m.get(id)
	if err == nil && l.State == "ending" {
		m.scheduleStorage(id)
	}
}
