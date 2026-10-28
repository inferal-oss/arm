package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

const forwardUnixDiskPromise int64 = 4 << 20
const forwardUnixCleanupWindow = time.Hour

// ForwardUnixPair is an original, in-process pre-bind handle. It grants no
// pathname, directory FD or socket authority. This cut has no native binder.
type ForwardUnixPair struct {
	child   *ForwardUnixChild
	source  string
	storage string
	data    forwardUnixPairData
	aborted bool // protected by Manager.mu
}

// AbortBeforeBind discharges only a positively read-back planned tuple. This
// source cut has no binder; later binders must persist bind_intent before the
// syscall, and this method refuses every other phase. Directory cleanup stays
// with the ordinary storage lease and its original artifact checks.
func (p *ForwardUnixPair) AbortBeforeBind(ctx context.Context) error {
	if p == nil || p.child == nil || p.child.parent == nil || p.child.parent.manager == nil || ctx == nil || ctx.Err() != nil {
		return model.Err("lease_inactive", "original planned pair required")
	}
	c, h := p.child, p.child.parent
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.aborted {
		return nil
	}
	if c.pair != p || c.released || h.unixChild != c || m.forwardListeners[h.id] != h || h.unixFault != "" || p.data.Phase != "planned" || p.source != h.id || p.storage != p.data.StorageID {
		return model.Err("listener_owner_changed", "original planned pair changed")
	}
	encoded, err := json.Marshal(p.data)
	if err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sourceRow, sourceRaw, sourceKey, err := readForwardUnixPairLease(ctx, tx, p.source)
	if err != nil {
		return err
	}
	if sourceKey != h.key || sourceRow.ID != h.id || sourceRow.Revision != h.result.Revision || sourceRow.Host != h.result.Host || sourceRow.Owner != h.result.Owner || sourceRow.Request.StorageLease != p.storage || !reflect.DeepEqual(sourceRow.Request, h.request) || !reflect.DeepEqual(sourceRow.Clock, h.result.Clock) || !reflect.DeepEqual(sourceRow.Expires, h.result.Expires) || !reflect.DeepEqual(sourceRow.Ports, h.result.Ports) || !consuming(sourceRow.State) {
		return model.Err("listener_owner_changed", "source changed before planned pair abort")
	}
	storageRow, _, storageKey, err := readForwardUnixPairLease(ctx, tx, p.storage)
	if err != nil {
		return err
	}
	if storageRow.ID != p.storage || storageKey != p.data.StorageKey || storageRow.Host != sourceRow.Host || storageRow.Owner != sourceRow.Owner || storageRow.Artifact == nil || storageRow.Artifact.Identity == nil || *storageRow.Artifact.Identity != p.data.StorageArtifact || storageRow.Request.Resources.Disk != p.data.DiskBytes || storageRow.Request.Kind != "storage" || storageRow.Clock == nil || *storageRow.Clock != p.data.StorageClock || storageRow.State != "active" && storageRow.State != "needs_attention" {
		return model.Err("storage_unavailable", "storage changed before planned pair abort")
	}
	if m.storage == nil || m.storage.Identity() != p.data.StorageRoot {
		return model.Err("storage_unavailable", "original storage root unavailable for no-bind proof")
	}
	proof, err := m.storage.ProveEmptyDirectory(ctx, *storageRow.Artifact, p.data.SocketName)
	if err != nil || proof.Identity != p.data.StorageArtifact || proof.Allocated < 0 || proof.Allocated > p.data.DiskBytes {
		return forwardUnixPairUnavailable()
	}
	terminal := p.data
	terminal.Phase, terminal.Revision = "terminal", p.data.Revision+1
	terminal.NoBind = &forwardUnixNoBindProof{Directory: proof.Identity, Allocated: proof.Allocated, At: time.Now().UTC()}
	terminalRaw, err := json.Marshal(terminal)
	if err != nil || len(terminalRaw) > 4096 {
		return forwardUnixPairUnavailable()
	}
	updatedPair, err := tx.ExecContext(ctx, `UPDATE forward_unix_pairs SET phase='terminal',data=? WHERE source_id=? AND storage_id=? AND phase='planned' AND source_key=? AND storage_key=? AND socket_name=? AND disk_bytes=? AND data=?`, string(terminalRaw), p.source, p.storage, p.data.SourceKey, p.data.StorageKey, p.data.SocketName, p.data.DiskBytes, string(encoded))
	if err != nil {
		return err
	}
	n, err := updatedPair.RowsAffected()
	if err != nil || n != 1 {
		return model.Err("forward_unix_pair_unavailable", "exact planned pair no longer recorded")
	}
	detached := sourceRow
	detached.Request.StorageLease = ""
	detached.Revision++
	updatedRaw, err := json.Marshal(detached)
	if err != nil {
		return err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE leases SET data=? WHERE id=? AND request_key=? AND data=?`, string(updatedRaw), p.source, h.key, string(sourceRaw))
	if err != nil {
		return err
	}
	n, err = updated.RowsAffected()
	if err != nil || n != 1 {
		return model.Err("listener_owner_changed", "source changed during planned pair abort")
	}
	if err = event(tx, "forward.unix_pair_nobind_aborted", p.source, terminal); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		h.unixFault = "forward Unix no-bind abort acknowledgement unavailable"
		c.cancel()
		h.CloseOriginal()
		return err
	}
	h.request, h.result = detached.Request, detached
	c.pair = nil
	p.data = terminal
	p.aborted = true
	_, err = m.end(p.storage, "cancelled")
	return err
}

type forwardUnixPairData struct {
	Schema          int                        `json:"schema"`
	SourceID        string                     `json:"source_id"`
	SourceKey       string                     `json:"source_key"`
	SourceBinding   model.ForwardSourceBinding `json:"source_binding"`
	SourceClock     leaseclock.Deadline        `json:"source_clock"`
	SourceRevision  int                        `json:"source_revision"`
	StorageID       string                     `json:"storage_id"`
	StorageKey      string                     `json:"storage_key"`
	StorageClock    leaseclock.Deadline        `json:"storage_clock"`
	StorageRevision int                        `json:"storage_revision"`
	StorageArtifact model.FileIdentity         `json:"storage_artifact"`
	StorageRoot     model.FileIdentity         `json:"storage_root"`
	DiskBytes       int64                      `json:"disk_bytes"`
	SocketName      string                     `json:"socket_name"`
	Phase           string                     `json:"phase"`
	Revision        int                        `json:"revision"`
	NoBind          *forwardUnixNoBindProof    `json:"no_bind,omitempty"`
}

type forwardUnixNoBindProof struct {
	Directory model.FileIdentity `json:"directory"`
	Allocated int64              `json:"allocated_bytes"`
	At        time.Time          `json:"at"`
}

// IssueStoragePair charges a dedicated positive Disk lease, then atomically
// attaches it to this exact original source and records a planned pair. No
// socket operation follows from this method. A failed or unknown commit never
// publishes the handle; uncertain outcome retains both charges.
func (c *ForwardUnixChild) IssueStoragePair(ctx context.Context) (pair *ForwardUnixPair, resultErr error) {
	if c == nil || c.parent == nil || c.parent.manager == nil || ctx == nil || ctx.Err() != nil {
		return nil, model.Err("lease_inactive", "live original Unix child required")
	}
	h, m := c.parent, c.parent.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.released || c.pairIssued || h.unixChild != c || c.ctx.Err() != nil {
		return nil, model.Err("lease_inactive", "original Unix child pair authority unavailable")
	}
	select {
	case <-c.joined:
		return nil, model.Err("listener_not_joined", "original Unix child already joined")
	default:
	}
	remaining, err := h.remainingLocked(ctx)
	if err != nil {
		return nil, err
	}
	if h.request.ForwardSource == nil || h.request.StorageLease != "" || h.result.Request.StorageLease != "" {
		return nil, model.Err("listener_owner_changed", "source already has a storage attachment")
	}
	c.pairIssued = true // never retry an ambiguous directory or pair attempt
	key := "forward-unix-storage:" + h.id
	var prior int
	err = m.db.QueryRow(`SELECT 1 FROM leases WHERE request_key=? LIMIT 1`, key).Scan(&prior)
	if err == nil {
		return nil, model.Err("idempotency_conflict", "forward Unix storage key was already recorded")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	storage, err := m.acquire(model.Request{
		Key: key, Kind: "storage", Project: "mesh-forward",
		Reason:    "Forward Unix source directory for " + h.id,
		Resources: model.Resources{Disk: forwardUnixDiskPromise},
		TTL:       remaining + forwardUnixCleanupWindow,
	}, nil)
	if err != nil {
		return nil, err // an uncertain ordinary storage admission remains inspectable
	}
	uncertain := false
	defer func() {
		if resultErr != nil && !uncertain {
			_, endErr := m.end(storage.ID, "forward_unix_pair_not_issued")
			resultErr = errors.Join(resultErr, endErr)
		}
	}()
	if _, err = h.remainingLocked(ctx); err != nil {
		return nil, err
	}
	select {
	case <-c.joined:
		return nil, model.Err("listener_not_joined", "original Unix child joined during storage admission")
	default:
	}
	currentStorage, err := m.get(storage.ID)
	if err != nil {
		return nil, err
	}
	storageRemaining, err := m.leaseRemaining(currentStorage)
	if err != nil || storageRemaining < remaining+forwardUnixCleanupWindow/2 || currentStorage.State != "active" || currentStorage.ID != storage.ID || currentStorage.Request.Key != key || currentStorage.Request.Kind != "storage" || currentStorage.Request.Resources != (model.Resources{Disk: forwardUnixDiskPromise}) || currentStorage.Host != h.result.Host || currentStorage.Owner != h.result.Owner || currentStorage.Artifact == nil || currentStorage.Artifact.Identity == nil || currentStorage.Artifact.Phase != "owned" || m.storage == nil {
		return nil, model.Err("storage_unavailable", "exact dedicated forward Unix storage entitlement unavailable")
	}
	if err = m.storage.Check(*currentStorage.Artifact); err != nil {
		return nil, model.Err("storage_unavailable", err.Error())
	}
	if h.result.Clock == nil || currentStorage.Clock == nil {
		return nil, model.Err("lease_inactive", "native deadline provenance unavailable")
	}
	name := "forward-" + local.ID() + ".sock"
	data := forwardUnixPairData{
		Schema: 1, SourceID: h.id, SourceKey: h.key,
		SourceBinding: *h.request.ForwardSource, SourceClock: *h.result.Clock,
		SourceRevision: h.result.Revision + 1,
		StorageID:      storage.ID, StorageKey: key, StorageClock: *currentStorage.Clock,
		StorageRevision: currentStorage.Revision,
		StorageArtifact: *currentStorage.Artifact.Identity, StorageRoot: m.storage.Identity(),
		DiskBytes: forwardUnixDiskPromise, SocketName: name, Phase: "planned", Revision: 1,
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > 4096 {
		return nil, model.Err("forward_unix_pair_unavailable", "bounded pair record unavailable")
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	sourceRow, sourceRaw, sourceKey, err := readForwardUnixPairLease(ctx, tx, h.id)
	if err != nil {
		return nil, err
	}
	storageRow, _, storageKey, err := readForwardUnixPairLease(ctx, tx, storage.ID)
	if err != nil {
		return nil, err
	}
	if sourceKey != h.key || storageKey != key || sourceRow.ID != h.id || sourceRow.State != "active" || sourceRow.Revision != h.result.Revision || sourceRow.Host != h.result.Host || sourceRow.Owner != h.result.Owner || sourceRow.PID != 0 || sourceRow.Process != nil || !reflect.DeepEqual(sourceRow.Request, h.request) || !reflect.DeepEqual(sourceRow.Clock, h.result.Clock) || !reflect.DeepEqual(sourceRow.Expires, h.result.Expires) || storageRow.ID != storage.ID || storageRow.Revision != currentStorage.Revision || !reflect.DeepEqual(storageRow.Request, currentStorage.Request) || !reflect.DeepEqual(storageRow.Artifact, currentStorage.Artifact) || !reflect.DeepEqual(storageRow.Clock, currentStorage.Clock) {
		return nil, model.Err("listener_owner_changed", "original pair lease rows changed before commit")
	}
	attached := sourceRow
	attached.Request.StorageLease = storage.ID
	attached.Revision++
	attachedRaw, err := json.Marshal(attached)
	if err != nil {
		return nil, err
	}
	update, err := tx.ExecContext(ctx, `UPDATE leases SET data=? WHERE id=? AND request_key=? AND data=?`, string(attachedRaw), h.id, h.key, string(sourceRaw))
	if err != nil {
		return nil, err
	}
	changed, err := update.RowsAffected()
	if err != nil || changed != 1 {
		return nil, model.Err("listener_owner_changed", "original source compare-and-attach failed")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO forward_unix_pairs(source_id,storage_id,phase,source_key,storage_key,socket_name,disk_bytes,data) VALUES(?,?,?,?,?,?,?,?)`, h.id, storage.ID, "planned", h.key, key, name, forwardUnixDiskPromise, string(encoded)); err != nil {
		return nil, err
	}
	if err = event(tx, "forward.unix_pair_planned", h.id, data); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		uncertain = true
		h.unixFault = "forward Unix pair commit acknowledgement unavailable"
		c.cancel()
		h.CloseOriginal()
		return nil, err
	}
	h.request, h.result = attached.Request, attached
	pair = &ForwardUnixPair{child: c, source: h.id, storage: storage.ID, data: data}
	c.pair = pair
	return pair, nil
}
