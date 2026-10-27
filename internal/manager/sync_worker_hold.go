package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

// SyncWorkerHold is minted only after the exact destination SSH fence, sync
// intent, lease and budget bindings commit. It is a one-use in-process worker
// owner; a stored SSH descriptor cannot reconstruct it after restart.
type SyncWorkerHold struct {
	manager      *Manager
	lease        string
	leaseRecord  []byte
	intentRecord []byte
	runtime      *sshRuntime
	intent       syncservice.Intent
	prepared     *syncservice.Preparation
	root         *syncfs.Root
	parent       *syncfs.Root
	volume       string
	memory       int64
	disk         int64

	mu      sync.Mutex
	started bool
	done    chan struct{}
	result  error
}

func newSyncWorkerHold(m *Manager, l model.Lease, s *syncAdmission, runtime *sshRuntime) *SyncWorkerHold {
	leaseRecord, _ := json.Marshal(l)
	intentRecord, _ := json.Marshal(s.intent)
	return &SyncWorkerHold{leaseRecord: leaseRecord, intentRecord: intentRecord, manager: m, lease: l.ID, runtime: runtime, intent: s.intent, prepared: s.prepared, root: s.root, parent: s.parent, volume: s.volume, memory: l.Request.Resources.Memory, disk: l.Request.Resources.Disk, done: make(chan struct{})}
}

func (h *SyncWorkerHold) Root() *syncfs.Root                    { return h.root }
func (h *SyncWorkerHold) WorkParent() *syncfs.Root              { return h.parent }
func (h *SyncWorkerHold) Preparation() *syncservice.Preparation { return h.prepared }
func (h *SyncWorkerHold) Ledger() *syncservice.Ledger {
	return &syncservice.Ledger{DB: h.manager.db, Key: h.intent.Key, Side: syncreconcile.Remote}
}
func (h *SyncWorkerHold) MemoryBytes() int64 { return h.memory }
func (h *SyncWorkerHold) DiskBytes() int64   { return h.disk }

func (h *SyncWorkerHold) CheckActive(ctx context.Context) error {
	if h == nil || h.manager == nil || h.runtime == nil || ctx == nil || ctx.Err() != nil {
		return model.Err("sync_worker_unavailable", "live admitted worker context required")
	}
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return h.checkActiveLocked(ctx)
}

// Manager.mu is held. Bind both checks and launch to the complete durable
// predecessor, including same-revision replacements and the volume promise.
func (h *SyncWorkerHold) checkActiveLocked(ctx context.Context) error {
	if err := h.runtime.check(); err != nil {
		return err
	}
	if h.manager.syncWorkers[h.lease] != h || h.manager.closing || h.manager.draining {
		return model.Err("sync_worker_unavailable", "original sync worker owner changed or backend is stopping")
	}
	return h.checkBindingLocked(ctx)
}

func (h *SyncWorkerHold) checkBindingLocked(ctx context.Context) error {
	var raw string
	if err := h.manager.db.QueryRowContext(ctx, `SELECT data FROM leases WHERE id=?`, h.lease).Scan(&raw); err != nil || len(h.leaseRecord) == 0 || !bytes.Equal([]byte(raw), h.leaseRecord) {
		return model.Err("sync_worker_unavailable", "exact charged sync lease changed")
	}
	var key, side, volume string
	var disk int64
	if err := h.manager.db.QueryRowContext(ctx, `SELECT key,side,volume,disk_bytes FROM sync_lease_bindings WHERE lease=?`, h.lease).Scan(&key, &side, &volume, &disk); err != nil || key != h.intent.Key || side != string(h.intent.Side) || volume != h.volume || disk != h.disk {
		return model.Err("sync_worker_unavailable", "exact charged sync volume binding changed")
	}
	return nil
}

func (h *SyncWorkerHold) CheckIntent(ctx context.Context, intent syncservice.Intent) error {
	if err := h.CheckActive(ctx); err != nil {
		return err
	}
	record, err := json.Marshal(intent)
	if err != nil || !bytes.Equal(record, h.intentRecord) || intent.Side != syncreconcile.Remote || intent.WorkerCommandSHA256 != sshDigest([]byte(syncWorkerCommand)) {
		return model.Err("sync_worker_unavailable", "worker intent differs from exact committed admission")
	}
	lookup, err := h.Ledger().Lookup(ctx)
	retained, encodeErr := json.Marshal(lookup.Intent)
	if err != nil || encodeErr != nil || !bytes.Equal(retained, h.intentRecord) || lookup.State != "reserved" {
		return model.Err("sync_evidence_unavailable", "retained sync intent changed before worker")
	}
	return nil
}

func (h *SyncWorkerHold) ChargeVolume(ctx context.Context, rootID string, bytes int64) error {
	if err := h.CheckActive(ctx); err != nil {
		return err
	}
	if bytes <= 0 || bytes > h.disk || rootID != h.intent.Binding.Remote.RootID {
		return model.Err("volume_unavailable", "worker demand exceeds exact charged root or disk promise")
	}
	actualID, err := h.root.NativeID()
	if err != nil || actualID != rootID {
		return syncfs.ErrChanged
	}
	volume, err := h.manager.syncVolume(h.root, h.parent)
	if err != nil || volume != h.volume {
		return model.Err("volume_unavailable", "worker filesystem differs from charged volume")
	}
	return nil
}

// Start owns the original worker and keeps its lease charged even after a
// successful receipt exchange. Settlement needs separate exact workspace
// cleanup evidence; worker completion alone never releases the promise.
func (h *SyncWorkerHold) Start(run func(context.Context, ssh.Channel) error) error {
	if h == nil || h.manager == nil || h.runtime == nil || run == nil {
		return model.Err("invalid_request", "reviewed sync runner required")
	}
	// Serialize the WaitGroup registration against Manager.Close. A prior
	// CheckActive alone would leave an Add/Wait race after it released mu.
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	if err := h.checkActiveLocked(h.runtime.ctx); err != nil {
		return err
	}
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return model.Err("idempotency_conflict", "sync worker already started")
	}
	h.started = true
	h.mu.Unlock()
	h.manager.wg.Go(func() {
		err := errors.Join(run(h.runtime.ctx, h.runtime.channel), h.runtime.ctx.Err())
		h.manager.mu.Lock()
		if bindErr := h.checkBindingLocked(context.Background()); bindErr != nil {
			err = errors.Join(err, bindErr)
		} else if l, getErr := h.manager.get(h.lease); getErr != nil {
			err = errors.Join(err, getErr)
		} else if raw, encodeErr := json.Marshal(l); encodeErr != nil || !bytes.Equal(raw, h.leaseRecord) {
			// End may already have retained the promise. A changed row never
			// authorizes overwriting its successor or claiming successful ack.
			err = errors.Join(err, model.Err("sync_evidence_unavailable", "sync lease predecessor changed at worker completion"))
		} else {
			outcome := "sync_worker_complete"
			if err != nil {
				outcome = "sync_worker_unknown"
			}
			err = errors.Join(err, h.manager.retainSyncLease(&l, outcome))
		}
		h.manager.mu.Unlock()
		h.mu.Lock()
		h.result = err
		h.mu.Unlock()
		close(h.done)
	})
	return nil
}

func (h *SyncWorkerHold) cancel() { h.runtime.cancel() }

func (h *SyncWorkerHold) Wait(ctx context.Context) (sshservice.Exit, error) {
	if ctx == nil {
		return sshservice.Exit{}, model.Err("invalid_request", "wait context required")
	}
	select {
	case <-h.done:
		h.mu.Lock()
		err := h.result
		h.mu.Unlock()
		if err != nil {
			return sshservice.Exit{Code: 1}, nil
		}
		return sshservice.Exit{Code: 0}, nil
	case <-h.runtime.ctx.Done():
		// Serve closes and joins the original connection before JoinTransport
		// waits for a worker blocked on inbound SSH channel IO.
		return sshservice.Exit{}, h.runtime.ctx.Err()
	case <-ctx.Done():
		return sshservice.Exit{}, ctx.Err()
	}
}
func (h *SyncWorkerHold) Signal(_ context.Context, signal string) error {
	if signal != "INT" && signal != "TERM" {
		return model.Err("invalid_request", "sync worker supports cancellation only")
	}
	h.cancel()
	return nil
}
func (*SyncWorkerHold) Resize(context.Context, sshservice.Window) error {
	return model.Err("invalid_request", "sync worker has no terminal")
}
func (h *SyncWorkerHold) JoinTransport() {
	h.cancel()
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started {
		<-h.done
	}
	h.runtime.joinTransport()
	h.manager.mu.Lock()
	if l, err := h.manager.get(h.lease); err == nil {
		if raw, encodeErr := json.Marshal(l); encodeErr == nil && bytes.Equal(raw, h.leaseRecord) {
			_ = h.manager.retainSyncLease(&l, "sync_transport_closed")
		}
	}
	if h.manager.syncWorkers[h.lease] == h {
		delete(h.manager.syncWorkers, h.lease)
	}
	h.manager.mu.Unlock()
}

var _ sshservice.Process = (*SyncWorkerHold)(nil)
var _ syncservice.DecodeAdmission = (*SyncWorkerHold)(nil)
