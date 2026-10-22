package workspace

import (
	"context"
	"database/sql"
)

// Finalize installs a local membership binding only from this process's fresh,
// authenticated directory snapshot after a matching locally approved join proof.
// It performs no remote mutation and is not automatic on lookup/restart.
func (r *Runtime) Finalize(ctx context.Context, id, key string, revision int64) (Operation, error) {
	return r.finalize(ctx, id, key, revision, nil)
}
func (r *Runtime) finalize(ctx context.Context, id, key string, revision int64, recovery *recoveryFinalize) (result Operation, err error) {
	r.mu.Lock()
	closed := r.closed
	if !closed {
		r.finalizing.Add(1)
	}
	r.mu.Unlock()
	if closed {
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
	var admitted authorizedSnapshot
	var finalizedRoot Instance
	applying := false
	err = r.store.transactionChecked(ctx, false, func(tx *sql.Tx, v *Records) error {
		r.mu.Lock()
		cached, ok := r.active[id]
		closed := r.closed
		r.mu.Unlock()
		if !ok || closed || cached.boot != r.store.boot {
			return problem("membership_unknown", "fresh directory result required to finalize join")
		}
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
		authorized := op.Private.Boot == r.store.boot && op.Private.ProofCount > 0 && validHex(op.Private.LastChallenge, 32)
		var recoveryChild Operation
		if recovery == nil {
			children, err := familyChildren(v, root.ID, OperationRef{Principal: op.Principal, Key: op.Key, RequestHash: op.RequestHash, Revision: op.Revision})
			if err != nil {
				return err
			}
			if len(children) > 0 {
				authorized = false
			}
		}
		if recovery != nil {
			recoveryChild, e = r.store.checkFinalizeRecovery(v, root, op, recovery)
			if e != nil {
				return e
			}
			authorized = true
		}
		if op.Phase != "approved" || op.Revision != revision || !authorized || root.Role != "member" || root.State != "joining" || root.ConsentHash != op.ReviewHash || root.Self != r.store.self || root.SelfGeneration != 0 || denied(v, id, root.Directory.Host) || denied(v, id, root.Self.Host) {
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
		if recovery != nil {
			recoveryChild.Revision, e = next(recoveryChild.Revision)
			if e != nil {
				return e
			}
			recoveryChild.Phase = "completed"
			recoveryChild.Updated = root.Updated
			if e = writeOperation(ctx, tx, recoveryChild); e != nil {
				return e
			}
		}
		if e = journal(ctx, tx, "join_finalized", op.Principal, key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		admitted = cached
		finalizedRoot = root
		applying = true
		return nil
	}, func(v *Records) error {
		// A completed-key readonly observation applies no new membership and
		// keeps its original semantics. NEW finalization must still be current
		// after structural validation and all before_commit delay/fault hooks.
		if !applying {
			return nil
		}
		r.mu.Lock()
		current, ok := r.active[id]
		closed := r.closed
		r.mu.Unlock()
		if closed || r.life.Err() != nil {
			return problem("backend_stopping", "workspace runtime closing before membership finalization")
		}
		if !ok || current != admitted || current.boot != r.store.boot {
			return problem("membership_unknown", "exact process membership changed before finalization commit")
		}
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		if root != finalizedRoot || root.Role != "member" || root.State != "active" || root.Self != r.store.self || root.SelfGeneration != current.generation || denied(v, id, root.Directory.Host) || denied(v, id, root.Self.Host) {
			return problem("admission_changed", "finalized local binding or deny changed before commit")
		}
		clock, e := clockOf(v, id)
		if e != nil {
			return e
		}
		if e = r.store.checkClock(clock.WallFloor); e != nil {
			return e
		}
		now, elapsed := r.store.now(), r.store.clock.Elapsed()
		if elapsed >= current.deadline {
			return problem("membership_stale", "original membership deadline passed before finalization commit")
		}
		found := false
		for _, o := range v.Observations {
			if o.Workspace == id && o.SnapshotHash == current.hash && now.Before(o.Snapshot.ValidUntil) {
				found = true
				break
			}
		}
		if !found {
			return problem("membership_stale", "exact directory observation expired before finalization commit")
		}
		for _, identity := range []Identity{root.Self, root.Directory} {
			if e = identityCurrent(identity, now); e != nil {
				return e
			}
		}
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
