package workspace

import (
	"context"
	"database/sql"
)

func (s *Store) Deny(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return s.mutate(ctx, id, "deny", q)
}
func (s *Store) Revoke(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return s.mutate(ctx, id, "revoke", q)
}

// Retire is LOCAL retirement only. Remote directory/member cleanup is neither
// asserted nor performed. Pending/unknown admissions must be explicitly settled.
func (s *Store) Retire(ctx context.Context, id string, q MutationRequest) (Operation, error) {
	return s.mutate(ctx, id, "retire", q)
}
func (s *Store) mutate(ctx context.Context, id, kind string, q MutationRequest) (result Operation, err error) {
	return s.mutateChecked(ctx, id, kind, q, nil)
}
func (s *Store) mutateChecked(ctx context.Context, id, kind string, q MutationRequest, before func(*sql.Tx) error) (result Operation, err error) {
	if !validHex(id, 16) || !validKey(q.Key) || q.Revision < 1 || !reason(q.Reason) || kind != "retire" && !validHex(q.Host, 16) || kind == "retire" && q.Host != "" {
		return result, problem("invalid_request", "exact workspace/revision/key/target and reason required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		if old, ok := operationOf(v, id, nativeKey(s.authority), q.Key); ok {
			if e = sameIntent(old, kind, q); e != nil {
				return e
			}
			result = old.View()
			return nil
		}
		if before != nil {
			if e = before(tx); e != nil {
				return e
			}
		}
		if root.Revision != q.Revision {
			return problem("stale_revision", "workspace revision changed")
		}
		if root.State == "retired" {
			return problem("workspace_retired", "workspace is locally retired")
		}
		now := s.now()
		if now.Before(root.Updated) {
			now = root.Updated
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = now
		op := newOperation(s, root, kind, q.Key, nativeKey(s.authority), q, now)
		op.Phase = "completed"
		var oldMember, newMember *Member
		switch kind {
		case "deny":
			if q.Host == root.Self.Host {
				return problem("invalid_request", "use local retirement instead of denying self")
			}
			if denied(v, id, q.Host) {
				return problem("already_denied", "source already has a retained local deny tombstone")
			}
			n := 0
			for _, d := range v.Denies {
				if d.Workspace == id {
					n++
				}
			}
			if n >= MaxMembers {
				return problem("history_full", "local deny roster is full")
			}
			d := Deny{Schema: Schema, Workspace: id, Host: q.Host, Revision: root.Revision, Key: q.Key, Actor: s.authority, Reason: q.Reason, Created: now}
			if e = put(ctx, tx, `INSERT INTO workspace_denies(workspace,host,data) VALUES(?,?,?)`, d, id, q.Host); e != nil {
				return e
			}
		case "revoke":
			if root.Role != "directory" || q.Host == root.Self.Host {
				return problem("forbidden", "only directory members other than self can be revoked; retire self explicitly")
			}
			m, ok := memberOf(v, id, q.Host)
			if !ok {
				return problem("not_found", "member not found")
			}
			if m.State == "revoked" {
				return problem("already_revoked", "member tombstone retained")
			}
			old := m
			oldMember = &old
			m.State = "revoked"
			m.RevokedAt = &now
			m.Updated = now
			m.Revision, e = next(m.Revision)
			if e != nil {
				return e
			}
			newMember = &m
			if e = writeMember(ctx, tx, m); e != nil {
				return e
			}
			for _, t := range v.Trusted {
				if t.Workspace == id && t.Identity.Host == q.Host {
					t.State = "revoked"
					t.LatestSeen = root.Revision
					if e = writeTrusted(ctx, tx, t); e != nil {
						return e
					}
				}
			}
			if pending, ok := operationOf(v, id, principalKey(m.IntroducedBy), m.AdmissionKey); ok && !terminal(pending.Phase) {
				pending.Phase = "cancelled"
				pending.Revision, e = next(pending.Revision)
				if e != nil {
					return e
				}
				pending.Updated = now
				pending.Error = "candidate revoked locally; no remote cleanup asserted"
				if e = writeOperation(ctx, tx, pending); e != nil {
					return e
				}
			}
		case "retire":
			for _, o := range v.Operations {
				if o.Workspace == id && !terminal(o.Phase) {
					return problem("obligations_unresolved", "pending/unknown workspace operation must settle before local retirement")
				}
			}
			root.State = "retired"
		}
		if e = room(v, id, op); e != nil {
			return e
		}
		if kind == "retire" {
			changed := []Member{}
			trusted := []TrustedMember{}
			for _, m := range v.Members {
				if m.Workspace != id || m.State == "revoked" {
					continue
				}
				m.State = "revoked"
				m.RevokedAt = &now
				m.Updated = now
				m.Revision, e = next(m.Revision)
				if e != nil {
					return e
				}
				if e = writeMember(ctx, tx, m); e != nil {
					return e
				}
				changed = append(changed, m)
			}
			for _, t := range v.Trusted {
				if t.Workspace != id {
					continue
				}
				t.State = "revoked"
				if root.Role == "directory" {
					t.LatestSeen = root.Revision
				}
				if e = writeTrusted(ctx, tx, t); e != nil {
					return e
				}
				trusted = append(trusted, t)
			}
			if e = writeInstance(ctx, tx, root); e != nil {
				return e
			}
			if e = writeOperation(ctx, tx, op); e != nil {
				return e
			}
			if e = writeChange(ctx, tx, Change{Schema: Schema, Workspace: id, Kind: kind, Actor: op.Principal, Key: q.Key, Revision: root.Revision, At: now, Before: &before, After: root, Members: changed, Trusted: trusted, OperationPhase: op.Phase}); e != nil {
				return e
			}
		} else {
			if e = writeInstance(ctx, tx, root); e != nil {
				return e
			}
			if e = writeOperation(ctx, tx, op); e != nil {
				return e
			}
			if e = journal(ctx, tx, kind, op.Principal, q.Key, op.Phase, &before, root, oldMember, newMember); e != nil {
				return e
			}
		}
		result = op.View()
		return nil
	})
	return
}

// CancelAdmission withdraws an exact pending DIRECTORY-side candidate; the proof
// is not an external execution, and a late callback is fenced by state/revision.
func (s *Store) CancelAdmission(ctx context.Context, id, sourceHost, key string, revision int64) (result Operation, err error) {
	if !validHex(sourceHost, 16) || !validKey(key) || revision < 1 {
		return result, problem("invalid_request", "exact source/key/revision required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		var op Operation
		found := false
		for _, x := range v.Operations {
			if x.Workspace == id && x.Key == key && x.Kind == "admit" && x.Private.Introducer != nil && x.Private.Introducer.Host == sourceHost {
				op = x
				found = true
				break
			}
		}
		if !found {
			return problem("not_found", "pending admission not found")
		}
		if op.Phase == "cancelled" {
			result = op.View()
			return nil
		}
		if op.Revision != revision || terminal(op.Phase) {
			return problem("stale_revision", "admission changed or already completed; use explicit member revoke")
		}
		var q AdmissionRequest
		if e = Decode(op.Request, &q); e != nil {
			return e
		}
		m, ok := memberOf(v, id, q.Candidate.Host)
		if !ok || m.State != "pending" {
			return problem("admission_changed", "candidate no longer pending")
		}
		before, old := root, m
		now := s.now()
		if now.Before(root.Updated) {
			now = root.Updated
		}
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = now
		m.State = "revoked"
		m.RevokedAt = &now
		m.Updated = now
		m.Revision, e = next(m.Revision)
		if e != nil {
			return e
		}
		op.Phase = "cancelled"
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = now
		op.Error = "candidate cancelled locally; no remote cleanup asserted"
		if e = room(v, id, op); e != nil {
			return e
		}
		if e = writeMember(ctx, tx, m); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "admission_cancelled", nativeKey(s.authority), key, op.Phase, &before, root, &old, &m); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

// Settled is a conservative local evidence predicate, never proof of remote
// cleanup and never a replacement for strict database reset/fence validation.
func Settled(v Records) bool {
	if !v.Initialized {
		return true
	}
	for _, r := range v.Instances {
		if r.State != "retired" {
			return false
		}
	}
	for _, o := range v.Operations {
		if !terminal(o.Phase) {
			return false
		}
	}
	for _, m := range v.Members {
		if m.State != "revoked" {
			return false
		}
	}
	return true
}
