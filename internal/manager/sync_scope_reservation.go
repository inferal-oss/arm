package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
)

// syncDedicatedScope has no production constructor in this cut. A future
// opener must bind its descriptors and enrollment before Manager startup;
// ordinary Open leaves this pointer nil and version 3 refuses work.
type syncDedicatedScope struct {
	gate                                                        *syncScopeGate
	project, selected, home                                     *syncfs.Root
	scopeID, mountID, projectRootID, selectedRootID, homeRootID string
	enrollmentDigest, ownershipDigest                           string
	projectID                                                   uint32
	ownerEpoch                                                  uint64
	chargedHard, initialUsed                                    int64
}

// syncScopeReservation is a private transaction participant for a future
// pre-enrolled dedicated opener. These fields are expected values, not native
// quota or original-owner proof. No production constructor or caller exists.
type syncScopeReservation struct {
	ctx                                       context.Context
	key, host, owner, scopeID, mountID        string
	projectRootID, selectedRootID, homeRootID string
	enrollmentDigest, ownershipDigest         string
	side                                      syncreconcile.Side
	projectID                                 uint32
	ownerEpoch                                uint64
	chargedHard, initialUsed                  int64
}

// reserveTx must run in the same transaction that first inserts l into
// leases. An unknown commit cannot be retried with a new key or project.
func (s *syncScopeReservation) reserveTx(ctx context.Context, tx *sql.Tx, l model.Lease) error {
	if s == nil || ctx == nil || tx == nil || ctx.Err() != nil ||
		len(s.key) < 16 || len(s.key) > 128 ||
		(s.side != syncreconcile.Local && s.side != syncreconcile.Remote) ||
		s.host == "" || s.owner == "" || s.scopeID == "" || s.mountID == "" ||
		s.projectRootID == "" || s.selectedRootID == "" || s.homeRootID == "" ||
		!lowerHex64(s.enrollmentDigest) || !lowerHex64(s.ownershipDigest) ||
		s.projectID == 0 || s.ownerEpoch == 0 || s.ownerEpoch > math.MaxInt64 ||
		s.chargedHard <= 0 || s.initialUsed < 0 || s.initialUsed > s.chargedHard ||
		l.ID == "" || l.Revision <= 0 || l.Host != s.host || l.Owner != s.owner ||
		l.Request.Kind != syncPreparationKind || l.Request.Host != s.host ||
		l.Request.Key != mesh.Key(s.host, "sync-preparation\x00"+string(s.side)+"\x00"+s.key) ||
		l.Request.Resources.Disk != s.chargedHard ||
		l.State != "active" {
		return model.Err("sync_evidence_unavailable", "original charged scope reservation is incomplete")
	}
	for _, c := range s.key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return model.Err("sync_evidence_unavailable", "original charged scope key is invalid")
		}
	}
	raw, err := json.Marshal(l)
	if err != nil || len(raw) == 0 || len(raw) > 65536 {
		return model.Err("sync_evidence_unavailable", "original charged lease encoding exceeds bound")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_charged_scopes(key,side,preparation_lease,preparation_revision,preparation_data,host,owner,provider,scope_id,mount_id,project_id,project_root_id,selected_root_id,home_root_id,owner_epoch,enrollment_digest,ownership_digest,charged_hard_bytes,initial_used_bytes,state,row_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.key, string(s.side), l.ID, l.Revision, string(raw), s.host, s.owner, "xfs-project-quota", s.scopeID, s.mountID, s.projectID, s.projectRootID, s.selectedRootID, s.homeRootID, s.ownerEpoch, s.enrollmentDigest, s.ownershipDigest, s.chargedHard, s.initialUsed, "preparing", 1)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO metadata(key,value) VALUES('sync_used','1')`)
	return err
}
