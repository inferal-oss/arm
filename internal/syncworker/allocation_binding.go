package syncworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
)

// Cached identity never caches execution authority. Each checkpoint checks the
// original hold again and compares bounded current durable rows byte for byte.
type allocationBinding struct {
	root, parent                         *syncfs.Root
	ledger                               *syncservice.Ledger
	db                                   *sql.DB
	hold                                 admittedHold
	session                              admittedSession
	control                              io.ReadWriteCloser
	key                                  string
	side                                 syncreconcile.Side
	record                               []byte
	digest, pair, rootID, parentID, plan string
	generation                           uint64
	disk, reservation                    int64
	work                                 map[string]allocationWorkspace
}
type allocationWorkspace struct {
	work                    *syncfs.Workspace
	base, stage, quarantine *syncfs.Root
	reservation             syncfs.Reservation
	record                  syncservice.WorkspaceRecord
}

func sameAllocationCapability(a, b any) bool {
	typ := reflect.TypeOf(a)
	return typ != nil && typ == reflect.TypeOf(b) && typ.Comparable() && a == b
}
func (e *endpoint) bindAllocation(ctx context.Context, intent syncservice.Intent, need int64) error {
	if e.allocation != nil || e.Ledger == nil || e.Ledger.DB == nil || e.Hold == nil || e.Session == nil || e.Root == nil || e.WorkParent == nil {
		return syncservice.ErrUnknown
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	root, err := e.Root.NativeID()
	if err != nil {
		return err
	}
	parent, err := e.WorkParent.NativeID()
	if err != nil {
		return err
	}
	pair, err := json.Marshal(intent.Binding)
	if err != nil {
		return err
	}
	disk := intent.LocalResources.Disk
	if e.Side == syncreconcile.Remote {
		disk = intent.RemoteResources.Disk
	}
	if disk <= 0 || need <= 0 || need > disk || e.Hold.DiskBytes() != disk {
		return errors.New("original admitted sync disk promise differs")
	}
	binding := &allocationBinding{root: e.Root, parent: e.WorkParent, ledger: e.Ledger, db: e.Ledger.DB, hold: e.Hold, session: e.Session, control: e.Session.Control(), key: intent.Key, side: e.Side, record: raw, digest: allocationDigest(raw), pair: allocationDigest(pair), rootID: root, parentID: parent, plan: intent.PlanDigest, generation: intent.Generation, disk: disk, reservation: need, work: make(map[string]allocationWorkspace)}
	e.allocation = binding
	return binding.check(ctx, e)
}
func (b *allocationBinding) check(ctx context.Context, e *endpoint) error {
	if b == nil || ctx == nil || ctx.Err() != nil || e.Root != b.root || e.WorkParent != b.parent || e.Ledger != b.ledger || e.Ledger.DB != b.db || e.Ledger.Key != b.key || e.Side != b.side || e.Ledger.Side != b.side || !sameAllocationCapability(e.Hold, b.hold) || !sameAllocationCapability(e.Session, b.session) || !sameAllocationCapability(e.Session.Control(), b.control) {
		return syncservice.ErrUnknown
	}
	if err := b.hold.CheckActive(ctx); err != nil {
		return err
	}
	if b.hold.DiskBytes() != b.disk {
		return syncservice.ErrUnknown
	}
	for root, id := range map[*syncfs.Root]string{b.root: b.rootID, b.parent: b.parentID} {
		if err := root.Check(); err != nil {
			return err
		}
		actual, err := root.NativeID()
		if err != nil || actual != id {
			return syncfs.ErrChanged
		}
	}
	var raw []byte
	var digest, state, pair string
	var generation int64
	err := b.db.QueryRowContext(ctx, `SELECT
 CASE WHEN typeof(record)='blob' THEN substr(record,1,?) ELSE NULL END,
 CASE WHEN typeof(record_sha256)='text' THEN substr(record_sha256,1,65) ELSE '' END,
 CASE WHEN typeof(state)='text' THEN substr(state,1,17) ELSE '' END,
 CASE WHEN typeof(pair)='text' THEN substr(pair,1,65) ELSE '' END,
 CASE WHEN typeof(generation)='integer' THEN generation ELSE -1 END
 FROM sync_operations WHERE key=? AND side=?`, len(b.record)+1, b.key, b.side).Scan(&raw, &digest, &state, &pair, &generation)
	if err != nil {
		return syncservice.ErrUnknown
	}
	if !bytes.Equal(raw, b.record) || digest != b.digest || pair != b.pair || generation < 0 || uint64(generation) != b.generation || (state != "reserved" && state != "applying") {
		return syncservice.ErrUnknown
	}
	return nil
}
func (b *allocationBinding) remember(w *syncfs.Workspace, role string) error {
	if b == nil || w == nil || len(b.work) >= 3 || b.work[role].work != nil {
		return syncservice.ErrUnknown
	}
	want := syncfs.Reservation{Key: b.key, Side: b.side, PlanDigest: b.plan, Generation: b.generation, DiskBytes: b.reservation}
	if w.Reservation != want || w.Parent != b.parent {
		return syncservice.ErrUnknown
	}
	if role != "receiver" && role != "source-"+string(b.side)+"-to-local" && role != "source-"+string(b.side)+"-to-remote" {
		return syncservice.ErrUnknown
	}
	id, err := w.Base.NativeID()
	if err != nil {
		return err
	}
	b.work[role] = allocationWorkspace{work: w, base: w.Base, stage: w.Stage, quarantine: w.Quarantine, reservation: want, record: syncservice.WorkspaceRecord{Role: role, Path: w.Path(), RootID: b.rootID, ParentID: b.parentID, BaseID: id, DiskBytes: b.reservation}}
	return nil
}
func (b *allocationBinding) durableSet(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `SELECT
 CASE WHEN typeof(role)='text' THEN substr(CAST(role AS BLOB),1,129) ELSE '' END,
 CASE WHEN typeof(path)='text' THEN substr(CAST(path AS BLOB),1,4097) ELSE '' END,
 CASE WHEN typeof(root_id)='text' THEN substr(CAST(root_id AS BLOB),1,82) ELSE '' END,
 CASE WHEN typeof(parent_id)='text' THEN substr(CAST(parent_id AS BLOB),1,82) ELSE '' END,
 CASE WHEN typeof(base_id)='text' THEN substr(CAST(base_id AS BLOB),1,82) ELSE '' END,
 CASE WHEN typeof(disk_bytes)='integer' THEN disk_bytes ELSE -1 END
 FROM sync_workspaces WHERE key=? AND side=? ORDER BY role LIMIT 4`, b.key, b.side)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var r syncservice.WorkspaceRecord
		if err := rows.Scan(&r.Role, &r.Path, &r.RootID, &r.ParentID, &r.BaseID, &r.DiskBytes); err != nil {
			return syncservice.ErrUnknown
		}
		original, ok := b.work[r.Role]
		if !ok || seen[r.Role] || r != original.record {
			return syncservice.ErrUnknown
		}
		seen[r.Role] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(seen) != len(b.work) {
		return syncservice.ErrUnknown
	}
	return nil
}
func (w allocationWorkspace) check() error {
	if w.work == nil || w.work.Base != w.base || w.work.Stage != w.stage || w.work.Quarantine != w.quarantine || w.work.Reservation != w.reservation || w.work.Path() != w.record.Path {
		return syncfs.ErrChanged
	}
	for _, root := range []*syncfs.Root{w.base, w.stage, w.quarantine} {
		if root == nil {
			return syncfs.ErrChanged
		}
		if err := root.Check(); err != nil {
			return err
		}
	}
	return nil
}

func allocationDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
