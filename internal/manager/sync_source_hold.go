package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
	"github.com/yrashk/arm/internal/syncservice"
	"golang.org/x/crypto/ssh"
)

// SyncSourceChannel is returned once by the authenticated source transport.
// CloseTransport must unblock original channel IO without waiting for the
// protocol runner. Join must wait for its original carrier and SSH workers; it
// cannot reconstruct an old connection from a retained request.
type SyncSourceChannel struct {
	Channel        ssh.Channel
	Requests       <-chan *ssh.Request
	CloseTransport func() error
	Join           func() error
}

// SyncSourceOpener starts the original authenticated SSH session only after
// source intent and resource admission commit. It must honor cancellation and
// return a channel whose Join covers the original transport workers.
type SyncSourceOpener func(context.Context) (SyncSourceChannel, error)

// SyncSourceHold is the one-use Manager owner of a source sync operation.
// Its opener, roots, lease, intent, and lifetime are bound before Start.
type SyncSourceHold struct {
	manager      *Manager
	lease        string
	leaseRecord  []byte
	intentRecord []byte
	intent       syncservice.Intent
	root         *syncfs.Root
	parent       *syncfs.Root
	volume       string
	memory       int64
	disk         int64
	ctx          context.Context
	cancelCtx    context.CancelFunc
	open         SyncSourceOpener

	mu      sync.Mutex
	started bool
	done    chan struct{}
	result  error
}

// AdmitSyncSource reserves the original source key before its transport is
// opened. Failure after the reservation retains that exact charged key for
// inspection; it never opens a replacement session or retries another key.
func (m *Manager) AdmitSyncSource(ctx context.Context, i syncservice.Intent, rows []syncservice.ManifestRow, root, parent *syncfs.Root, control model.SSHControlCommitment, open SyncSourceOpener) (model.Lease, *SyncSourceHold, error) {
	if ctx == nil || ctx.Err() != nil || open == nil {
		return model.Lease{}, nil, model.Err("invalid_request", "live source context and authenticated SSH opener required")
	}
	l, err := m.ReserveSyncSource(ctx, i, rows, root, parent, control)
	if err != nil {
		if l.ID != "" {
			m.mu.Lock()
			l, err = m.failSyncSourceLocked(l, err)
			m.mu.Unlock()
		}
		return l, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fail := func(err error) (model.Lease, *SyncSourceHold, error) {
		l, err = m.failSyncSourceLocked(l, err)
		return l, nil, err
	}
	if m.closing || m.draining || l.State != "active" || l.Expires == nil || m.syncSourceWorkers[l.ID] != nil {
		return fail(model.Err("sync_worker_unavailable", "original source reservation is no longer startable"))
	}
	var raw string
	if err := m.db.QueryRowContext(ctx, `SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil {
		return fail(err)
	}
	original, err := json.Marshal(l)
	if err != nil || !bytes.Equal([]byte(raw), original) {
		return fail(model.Err("sync_evidence_unavailable", "source reservation changed before original owner binding"))
	}
	var key, side, volume string
	var disk int64
	if err := m.db.QueryRowContext(ctx, `SELECT key,side,volume,disk_bytes FROM sync_lease_bindings WHERE lease=?`, l.ID).Scan(&key, &side, &volume, &disk); err != nil || key != i.Key || side != string(syncreconcile.Local) || disk != l.Request.Resources.Disk {
		return fail(model.Err("sync_evidence_unavailable", "original source lease and volume binding differ"))
	}
	actualVolume, err := m.syncVolume(root, parent)
	if err != nil || actualVolume != volume {
		return fail(model.Err("volume_unavailable", "source root differs from charged volume"))
	}
	intentRecord, err := json.Marshal(i)
	if err != nil {
		return fail(err)
	}
	remaining, clockErr := m.leaseRemaining(l)
	if clockErr != nil || remaining <= 0 {
		return fail(model.Err("lease_inactive", "elapsed entitlement unavailable"))
	}
	workerCtx, cancel := context.WithTimeout(ctx, remaining)
	h := &SyncSourceHold{manager: m, lease: l.ID, leaseRecord: []byte(raw), intentRecord: intentRecord, intent: i, root: root, parent: parent, volume: volume, memory: l.Request.Resources.Memory, disk: disk, ctx: workerCtx, cancelCtx: cancel, open: open, done: make(chan struct{})}
	m.syncSourceWorkers[l.ID] = h
	return l, h, nil
}

// Manager.mu is held. Failed setup may retain only its own exact predecessor;
// a same-revision replacement is neither adoptable nor ours to overwrite.
func (m *Manager) failSyncSourceLocked(l model.Lease, cause error) (model.Lease, error) {
	original, err := json.Marshal(l)
	if err != nil {
		return l, errors.Join(cause, err)
	}
	var raw string
	if err := m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, l.ID).Scan(&raw); err != nil {
		return l, errors.Join(cause, err)
	}
	if !bytes.Equal([]byte(raw), original) {
		return l, errors.Join(cause, model.Err("sync_evidence_unavailable", "changed source successor preserved after setup failure"))
	}
	retained, err := m.end(l.ID, "sync_source_setup_unknown")
	return retained, errors.Join(cause, err)
}

func (h *SyncSourceHold) Root() *syncfs.Root       { return h.root }
func (h *SyncSourceHold) WorkParent() *syncfs.Root { return h.parent }
func (h *SyncSourceHold) Ledger() *syncservice.Ledger {
	return &syncservice.Ledger{DB: h.manager.db, Key: h.intent.Key, Side: syncreconcile.Local}
}
func (h *SyncSourceHold) MemoryBytes() int64 { return h.memory }
func (h *SyncSourceHold) DiskBytes() int64   { return h.disk }

func (h *SyncSourceHold) CheckActive(ctx context.Context) error {
	if h == nil || h.manager == nil || ctx == nil || ctx.Err() != nil || h.ctx.Err() != nil {
		return model.Err("sync_worker_unavailable", "live original source worker required")
	}
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	return h.checkActiveLocked(ctx)
}

// Manager.mu is held. A same-revision row replacement cannot lend authority.
func (h *SyncSourceHold) checkActiveLocked(ctx context.Context) error {
	lease, err := h.manager.get(h.lease)
	if err != nil || !h.manager.leaseLive(lease) {
		return model.Err("sync_worker_unavailable", "elapsed sync entitlement unavailable")
	}
	if h.ctx == nil || h.ctx.Err() != nil || h.manager.closing || h.manager.draining || h.manager.syncSourceWorkers[h.lease] != h {
		return model.Err("sync_worker_unavailable", "original source worker owner changed")
	}
	return h.checkBindingLocked(ctx)
}

func (h *SyncSourceHold) checkBindingLocked(ctx context.Context) error {
	var raw string
	if err := h.manager.db.QueryRowContext(ctx, `SELECT data FROM leases WHERE id=?`, h.lease).Scan(&raw); err != nil || !bytes.Equal([]byte(raw), h.leaseRecord) {
		return model.Err("sync_worker_unavailable", "exact source lease changed")
	}
	var key, side, volume string
	var disk int64
	if err := h.manager.db.QueryRowContext(ctx, `SELECT key,side,volume,disk_bytes FROM sync_lease_bindings WHERE lease=?`, h.lease).Scan(&key, &side, &volume, &disk); err != nil || key != h.intent.Key || side != string(syncreconcile.Local) || volume != h.volume || disk != h.disk {
		return model.Err("sync_worker_unavailable", "exact source volume binding changed")
	}
	return nil
}

func (h *SyncSourceHold) CheckIntent(ctx context.Context, intent syncservice.Intent) error {
	if err := h.CheckActive(ctx); err != nil {
		return err
	}
	record, err := json.Marshal(intent)
	if err != nil || !bytes.Equal(record, h.intentRecord) || intent.Side != syncreconcile.Local || intent.WorkerCommandSHA256 != sshDigest([]byte(syncWorkerCommand)) {
		return model.Err("sync_worker_unavailable", "source intent differs from original admission")
	}
	lookup, err := h.Ledger().Lookup(ctx)
	retained, encodeErr := json.Marshal(lookup.Intent)
	if err != nil || encodeErr != nil || !bytes.Equal(retained, h.intentRecord) || lookup.State != "reserved" {
		return model.Err("sync_evidence_unavailable", "retained source intent changed before worker")
	}
	return nil
}

func (h *SyncSourceHold) ChargeVolume(ctx context.Context, rootID string, bytes int64) error {
	if err := h.CheckActive(ctx); err != nil {
		return err
	}
	if bytes <= 0 || bytes > h.disk || rootID != h.intent.Binding.Local.RootID {
		return model.Err("volume_unavailable", "source demand exceeds exact charged root or disk promise")
	}
	id, err := h.root.NativeID()
	if err != nil || id != rootID {
		return syncfs.ErrChanged
	}
	volume, err := h.manager.syncVolume(h.root, h.parent)
	if err != nil || volume != h.volume {
		return model.Err("volume_unavailable", "source filesystem differs from charged volume")
	}
	return nil
}

// Start owns both the original authenticated channel and the worker. The
// callback receives only that channel; it cannot replace the admitted opener.
func (h *SyncSourceHold) Start(run func(context.Context, ssh.Channel, <-chan *ssh.Request) error) error {
	if h == nil || h.manager == nil || h.ctx == nil || h.open == nil || run == nil {
		return model.Err("invalid_request", "reviewed source runner required")
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := h.checkActiveLocked(h.ctx); err != nil {
		return err
	}
	h.mu.Lock()
	if h.started {
		h.mu.Unlock()
		return model.Err("idempotency_conflict", "source sync worker already started")
	}
	h.started = true
	h.mu.Unlock()
	// Freeze witnesses once. A late callback never reads a successor channel,
	// opener or context from mutable holder fields.
	originalCtx, open := h.ctx, h.open
	m.wg.Go(func() {
		owned, err := open(originalCtx)
		var closeErr error
		var closeOnce sync.Once
		closeOriginal := func() {
			closeOnce.Do(func() {
				// Transport closure must precede Channel.Close: the latter is
				// only an SSH request and can itself block behind a stalled peer.
				if owned.CloseTransport != nil {
					// The authenticated peer or protocol may already have closed
					// this exact carrier. Its original Join is still mandatory.
					if e := owned.CloseTransport(); e != nil && !errors.Is(e, net.ErrClosed) {
						closeErr = errors.Join(closeErr, e)
					}
				}
				if owned.Channel != nil {
					if e := owned.Channel.Close(); e != nil && !errors.Is(e, io.EOF) {
						closeErr = errors.Join(closeErr, e)
					}
				}
			})
		}
		callbackDone := make(chan struct{})
		stopClose := context.AfterFunc(originalCtx, func() { defer close(callbackDone); closeOriginal() })
		if err == nil {
			if owned.Channel == nil || owned.Requests == nil || owned.CloseTransport == nil || owned.Join == nil {
				err = model.Err("sync_worker_unavailable", "authenticated source channel and join witness required")
			} else {
				m.mu.Lock()
				err = h.checkActiveLocked(originalCtx)
				if h.ctx != originalCtx {
					err = model.Err("sync_worker_unavailable", "original source context changed while opening")
				}
				m.mu.Unlock()
				if err == nil {
					if runErr := run(originalCtx, owned.Channel, owned.Requests); runErr != nil {
						err = fmt.Errorf("source sync protocol: %w", runErr)
					}
				}
			}
		}
		closeOriginal()
		if stopClose() {
			close(callbackDone)
		}
		<-callbackDone // actual original callback completion, never nil=>success
		err = errors.Join(err, closeErr)
		if owned.Join != nil {
			if joinErr := owned.Join(); joinErr != nil {
				err = errors.Join(err, fmt.Errorf("source SSH transport join: %w", joinErr))
			}
		}
		err = errors.Join(err, originalCtx.Err())
		m.mu.Lock()
		if bindErr := h.checkBindingLocked(context.Background()); bindErr != nil {
			err = errors.Join(err, bindErr)
		} else if l, getErr := m.get(h.lease); getErr != nil {
			err = errors.Join(err, getErr)
		} else if record, encodeErr := json.Marshal(l); encodeErr != nil || !bytes.Equal(record, h.leaseRecord) {
			err = errors.Join(err, model.Err("sync_evidence_unavailable", "source lease predecessor changed at worker completion"))
		} else {
			outcome := "sync_source_complete"
			if err != nil {
				outcome = "sync_source_unknown"
			}
			err = errors.Join(err, m.retainSyncLease(&l, outcome))
		}
		m.mu.Unlock()
		h.mu.Lock()
		h.result = err
		h.mu.Unlock()
		close(h.done)
	})
	return nil
}

func (h *SyncSourceHold) cancel() {
	h.cancelCtx()
}

func (h *SyncSourceHold) Wait(ctx context.Context) error {
	if ctx == nil {
		return model.Err("invalid_request", "wait context required")
	}
	select {
	case <-h.done:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.result
	case <-ctx.Done():
		return ctx.Err()
	case <-h.ctx.Done():
		return h.ctx.Err()
	}
}

func (h *SyncSourceHold) JoinTransport() {
	h.cancel()
	h.mu.Lock()
	started := h.started
	h.mu.Unlock()
	if started {
		<-h.done
	}
	h.manager.mu.Lock()
	if l, err := h.manager.get(h.lease); err == nil {
		if raw, encodeErr := json.Marshal(l); encodeErr == nil && bytes.Equal(raw, h.leaseRecord) {
			_ = h.manager.retainSyncLease(&l, "sync_source_transport_closed")
		}
	}
	if h.manager.syncSourceWorkers[h.lease] == h {
		delete(h.manager.syncSourceWorkers, h.lease)
	}
	h.manager.mu.Unlock()
}

var _ syncservice.DecodeAdmission = (*SyncSourceHold)(nil)
