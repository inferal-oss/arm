package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
)

// SyncKeyStatus is a read-only original-key recovery view. A completed
// operation and a needs-attention lease are deliberately separate facts:
// neither a receipt nor a baseline releases the resource promise.
type SyncKeyStatus struct {
	Key            string                   `json:"key"`
	Side           syncreconcile.Side       `json:"side"`
	Lease          model.Lease              `json:"lease"`
	Volume         string                   `json:"volume"`
	OperationState string                   `json:"operation_state"`
	PlanDigest     string                   `json:"plan_digest"`
	Generation     uint64                   `json:"generation"`
	LocalReceipt   *syncservice.SideReceipt `json:"local_receipt,omitempty"`
	RemoteReceipt  *syncservice.SideReceipt `json:"remote_receipt,omitempty"`
}

// InspectSyncKey never reserves, executes, retries, or settles the original
// key. A server exposing it must authorize the requester for this endpoint;
// the method itself only verifies local durable evidence under Manager.mu.
func (m *Manager) InspectSyncKey(ctx context.Context, key string, side syncreconcile.Side) (SyncKeyStatus, error) {
	var out SyncKeyStatus
	if ctx == nil || ctx.Err() != nil || side != syncreconcile.Local && side != syncreconcile.Remote {
		return out, model.Err("invalid_request", "live context and sync endpoint side required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return out, model.Err("sync_evidence_unavailable", "Manager is closing")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT lease,volume,disk_bytes FROM sync_lease_bindings WHERE key=? AND side=?`, key, side)
	if err != nil {
		return out, err
	}
	var leaseID, volume string
	var disk int64
	count := 0
	for rows.Next() {
		if err = rows.Scan(&leaseID, &volume, &disk); err != nil {
			break
		}
		count++
		if count > 1 {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return out, err
	}
	if closeErr != nil {
		return out, closeErr
	}
	if count != 1 || leaseID == "" || volume == "" || disk <= 0 {
		return out, model.Err("sync_evidence_unavailable", "one exact charged sync binding required")
	}
	// Keep every database wait under the caller's context, and compare the
	// physical request key before decorating a copy with transient usage.
	var raw []byte
	var requestKey sql.NullString
	if err := m.db.QueryRowContext(ctx, `SELECT data,request_key FROM leases WHERE id=?`, leaseID).Scan(&raw, &requestKey); err != nil {
		return out, err
	}
	var lease model.Lease
	if err := json.Unmarshal(raw, &lease); err != nil {
		return out, err
	}
	canonical, err := json.Marshal(lease)
	if err != nil || !bytes.Equal(raw, canonical) || !requestKey.Valid || requestKey.String != lease.Request.Key ||
		lease.Request.OriginKey != key || lease.Request.Resources.Disk != disk || lease.Host != m.host || lease.ID != leaseID ||
		lease.Owner != m.owner || lease.Request.Host != m.host || !consuming(lease.State) {
		return out, model.Err("sync_evidence_unavailable", "sync lease differs from original charged binding")
	}
	lookup, err := (&syncservice.Ledger{DB: m.db, Key: key, Side: side}).Lookup(ctx)
	if err != nil {
		return out, err
	}
	if lookup.Intent.Key != key || lookup.Intent.Side != side {
		return out, fmt.Errorf("%w: original sync operation identity changed", syncservice.ErrUnknown)
	}
	wantResources, ownEndpoint, peerEndpoint := lookup.Intent.LocalResources, lookup.Intent.Binding.Local, lookup.Intent.Binding.Remote
	wantKey, wantKind := mesh.Key(m.host, "sync-source\x00"+key), "execution"
	if side == syncreconcile.Remote {
		wantResources, ownEndpoint, peerEndpoint = lookup.Intent.RemoteResources, lookup.Intent.Binding.Remote, lookup.Intent.Binding.Local
		wantKey, wantKind = mesh.Key(lease.Request.Peer, "ssh-exec\x00"+key), ""
	}
	if lease.Request.Resources != wantResources || lease.Request.Key != wantKey || lease.Request.Kind != wantKind || lease.Request.Reason != lookup.Intent.Reason ||
		ownEndpoint.ID != SyncEndpointID(m.host, m.owner, lease.OwnerUsername) || !sshHex(lease.Request.Peer, 16) ||
		!strings.HasPrefix(peerEndpoint.ID, "arm-sync/v1/"+lease.Request.Peer+"/") {
		return out, model.Err("sync_evidence_unavailable", "sync lease resources or endpoint identity differ from retained intent")
	}
	m.attachUsage(&lease)
	out = SyncKeyStatus{Key: key, Side: side, Lease: lease, Volume: volume, OperationState: lookup.State,
		PlanDigest: lookup.Intent.PlanDigest, Generation: lookup.Intent.Generation,
		LocalReceipt: lookup.LocalReceipt, RemoteReceipt: lookup.RemoteReceipt}
	return out, nil
}
