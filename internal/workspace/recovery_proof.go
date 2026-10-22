package workspace

import (
	"context"
	"database/sql"
)

func (s *Store) recoveryAttempt(v *Records, id, key string) (Instance, Operation, Operation, Member, RecoveryRequest, error) {
	root, e := rootOf(v, id)
	if e != nil {
		return root, Operation{}, Operation{}, Member{}, RecoveryRequest{}, e
	}
	child, ok := operationOf(v, id, nativeKey(s.authority), key)
	if !ok || child.Kind != "resume_admission" {
		return root, child, Operation{}, Member{}, RecoveryRequest{}, problem("not_found", "admission recovery not found")
	}
	q, e := recoveryRequest(child)
	if e != nil {
		return root, child, Operation{}, Member{}, q, e
	}
	parent, candidate, e := s.recoveryParent(v, root, child.Kind, q, true)
	if e != nil {
		return root, child, parent, Member{}, q, e
	}
	member, _ := memberOf(v, id, candidate.Host)
	if child.Phase == "completed" {
		return root, child, parent, member, q, nil
	}
	children, e := familyChildren(v, id, q.Original)
	if e != nil {
		return root, child, parent, member, q, e
	}
	if len(children) == 0 || children[len(children)-1].Key != key || child.Private.Boot != s.boot || !s.now().Before(child.ReviewExpires) || !q.PermitProof || parent.Phase == "completed" || member.State != "pending" || member.Revision != child.Private.CandidateRevision {
		return root, child, parent, member, q, problem("recovery_changed", "current recovery/boot/review/member differs")
	}
	return root, child, parent, member, q, nil
}

func (s *Store) BeginRecoveryProof(ctx context.Context, id, key string) (challenge Challenge, result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, child, parent, m, q, e := s.recoveryAttempt(v, id, key)
		if e != nil {
			return e
		}
		if child.Phase == "completed" {
			result = child.View()
			return nil
		}
		if child.Phase != "approved" {
			return problem("outcome_unresolved", "recovery proof already started; lookup or cancel this exact child")
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = s.now()
		child.Revision, e = next(child.Revision)
		if e != nil {
			return e
		}
		child.Phase = "proving"
		child.Updated = root.Updated
		challenge = Challenge{Introducer: *parent.Private.Introducer, CandidateIntroducer: m.Introducer, HostInbox: m.HostInbox, Workspace: root.ID, Generation: root.Generation, Directory: root.Directory, Candidate: m.Identity, ConsentHash: m.ConsentHash, Nonce: child.Private.Nonce, PolicyRevision: root.PolicyRevision, Expires: child.ReviewExpires, Recovery: &RecoveryBinding{OriginalKey: parent.Key, AttemptKey: child.Key, CandidateAuthorization: q.CandidateAuthorization}}
		if e = room(v, id, child); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, child); e != nil {
			return e
		}
		if e = journal(ctx, tx, "recovery_proving", child.Principal, child.Key, child.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = child.View()
		return nil
	})
	return
}

func (s *Store) CompleteRecoveryProof(ctx context.Context, id, key string, revision int64, proof Proof) (result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, child, parent, m, q, e := s.recoveryAttempt(v, id, key)
		if e != nil {
			return e
		}
		if child.Phase == "completed" {
			result = child.View()
			return nil
		}
		if child.Phase != "proving" || child.Revision != revision {
			return problem("stale_revision", "recovery proof attempt differs")
		}
		expected := Proof{Workspace: id, Generation: root.Generation, Source: m.Identity.Host, Target: root.Directory.Host, SourcePin: m.Identity.Fingerprint, TargetPin: root.Directory.Fingerprint, ConsentHash: m.ConsentHash, Nonce: child.Private.Nonce, Recovery: &RecoveryBinding{OriginalKey: parent.Key, AttemptKey: child.Key, CandidateAuthorization: q.CandidateAuthorization}}
		if hash(expected) != hash(proof) {
			return problem("forbidden", "recovery proof did not bind exact original consent and fresh child")
		}
		before, old := root, m
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = s.now()
		m.Revision, e = next(m.Revision)
		if e != nil {
			return e
		}
		m.State = "active"
		m.Updated = root.Updated
		parent.Revision, e = next(parent.Revision)
		if e != nil {
			return e
		}
		parent.Phase = "completed"
		parent.Result = &m
		parent.Updated = root.Updated
		parent.Error = ""
		child.Revision, e = next(child.Revision)
		if e != nil {
			return e
		}
		child.Phase = "completed"
		child.Result = &m
		child.Updated = root.Updated
		child.Error = ""
		if e = room(v, id, child); e != nil {
			return e
		}
		for _, op := range []Operation{parent, child} {
			if e = writeOperation(ctx, tx, op); e != nil {
				return e
			}
		}
		if e = writeMember(ctx, tx, m); e != nil {
			return e
		}
		if e = writeTrusted(ctx, tx, TrustedMember{Schema: Schema, Workspace: id, Generation: root.Generation, Identity: m.Identity, MembershipGeneration: m.MembershipGeneration, State: "active", FirstSeen: root.Revision, LatestSeen: root.Revision}); e != nil {
			return e
		}
		if e = s.fail("recovery_active"); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = journal(ctx, tx, "recovery_completed", child.Principal, child.Key, child.Phase, &before, root, &old, &m); e != nil {
			return e
		}
		result = child.View()
		return nil
	})
	return
}

func (s *Store) RecordRecoveryFailure(ctx context.Context, id, key string, revision int64) error {
	return s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		op, ok := operationOf(v, id, nativeKey(s.authority), key)
		if !ok || !recoveryKind(op.Kind) {
			return problem("not_found", "recovery child not found")
		}
		if op.Revision != revision || op.Phase != "proving" || op.Private.Boot != s.boot {
			return problem("stale_revision", "recovery changed before failure receipt")
		}
		var e error
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Phase = "unknown"
		op.Error = "candidate proof outcome unknown; inspect exact recovery key"
		op.Updated = s.now()
		return writeOperation(ctx, tx, op)
	})
}

// candidateRecovery chooses a fresh child permit, not an old boot's approval.
func (s *Store) candidateRecovery(v *Records, root Instance, parent Operation, c Challenge) (Operation, error) {
	if c.Recovery == nil || !validKey(c.Recovery.OriginalKey) || !validKey(c.Recovery.AttemptKey) || !validHex(c.Recovery.CandidateAuthorization, 32) {
		return Operation{}, problem("forbidden", "exact recovery challenge binding required")
	}
	children, e := familyChildren(v, root.ID, OperationRef{Principal: parent.Principal, Key: parent.Key, RequestHash: parent.RequestHash, Revision: parent.Revision})
	if e != nil {
		return Operation{}, e
	}
	if len(children) == 0 {
		return Operation{}, problem("forbidden", "candidate has no fresh recovery consent")
	}
	child := children[len(children)-1]
	q, e := recoveryRequest(child)
	if e != nil {
		return child, e
	}
	if child.Kind != "resume_join" || child.Phase != "approved" || child.Private.Boot != s.boot || !s.now().Before(child.ReviewExpires) || child.ReviewHash != c.Recovery.CandidateAuthorization || !q.PermitProof {
		return child, problem("forbidden", "candidate recovery consent/boot/deadline differs")
	}
	if _, _, e = s.recoveryParent(v, root, child.Kind, q, true); e != nil {
		return child, e
	}
	return child, nil
}
