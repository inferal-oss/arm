package workspace

import (
	"context"
	"database/sql"
)

// Finalize installs a local membership binding only from this process's fresh,
// authenticated directory snapshot after a matching locally approved join proof.
// It performs no remote mutation and is not automatic on lookup/restart.
func (r *Runtime) Finalize(ctx context.Context, id, key string, revision int64) (result Operation, err error) {
	r.mu.Lock()
	cached, ok := r.active[id]
	closed := r.closed
	if ok && !closed && cached.boot == r.store.boot {
		r.finalizing.Add(1)
	}
	r.mu.Unlock()
	if !ok || closed || cached.boot != r.store.boot {
		return result, problem("membership_unknown", "fresh directory result required to finalize join")
	}
	defer r.finalizing.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.life, cancel)
	defer stop()
	defer cancel()
	if r.life.Err() != nil {
		return result, problem("backend_stopping", "workspace runtime closing")
	}
	err = r.store.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		op, ok := operationOf(v, id, nativeKey(r.store.authority), key)
		if !ok || op.Kind != "join" {
			return problem("not_found", "join intent not found")
		}
		if op.Phase == "completed" {
			result = op.View()
			return nil
		}
		if op.Phase != "approved" || op.Revision != revision || op.Private.Boot != r.store.boot || op.Private.ProofCount < 1 || !validHex(op.Private.LastChallenge, 32) || root.Role != "member" || root.State != "joining" || root.ConsentHash != op.ReviewHash || root.Self != r.store.self || root.SelfGeneration != 0 || denied(v, id, root.Directory.Host) || denied(v, id, root.Self.Host) {
			return problem("admission_changed", "local exact join/proof/identity changed")
		}
		clock, e := clockOf(v, id)
		if e != nil {
			return e
		}
		if e = r.store.checkClock(clock.WallFloor); e != nil {
			return e
		}
		if r.store.clock.Elapsed() >= cached.deadline {
			return problem("membership_stale", "fresh membership deadline passed")
		}
		var snap Snapshot
		for _, o := range v.Observations {
			if o.Workspace == id && o.SnapshotHash == cached.hash {
				snap = o.Snapshot
				break
			}
		}
		if snap.Hash == "" || !r.store.now().Before(snap.ValidUntil) {
			return problem("membership_stale", "current exact directory result required")
		}
		card, ok := cardOf(snap, root.Self.Host)
		if !ok || card.State != "active" || card.Identity != root.Self || card.MembershipGeneration != cached.generation || card.Introducer != op.Review.Introducer || card.HostInbox != op.Review.HostInbox {
			return problem("admission_changed", "directory admitted different identity/endpoint/delegation")
		}
		for _, i := range []Identity{root.Self, root.Directory} {
			if e = identityCurrent(i, r.store.now()); e != nil {
				return e
			}
		}
		before := root
		root.State = "active"
		root.SelfGeneration = card.MembershipGeneration
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = r.store.now()
		op.Phase = "completed"
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = root.Updated
		if e = room(v, id, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "join_finalized", op.Principal, key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

// CancelJoin withdraws the local pending binding, without asserting that a
// directory admission on another authority was revoked. Explicit local retire
// is still required before this ledger can be considered settled.
func (s *Store) CancelJoin(ctx context.Context, id, key string, revision int64) (result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		op, ok := operationOf(v, id, nativeKey(s.authority), key)
		if !ok || op.Kind != "join" {
			return problem("not_found", "join intent not found")
		}
		if op.Phase == "cancelled" {
			result = op.View()
			return nil
		}
		if root.State != "joining" || op.Phase != "approved" || op.Revision != revision {
			return problem("stale_revision", "join changed; active bindings require explicit retirement")
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.State = "needs_attention"
		now := s.now()
		if now.Before(root.Updated) {
			now = root.Updated
		}
		root.Updated = now
		op.Phase = "cancelled"
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = now
		op.Error = "local join withdrawn; remote directory membership may remain active"
		if e = room(v, id, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "decision", op.Principal, key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}
