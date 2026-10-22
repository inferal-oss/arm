package workspace

import (
	"context"
	"database/sql"
	"time"

	"github.com/yrashk/arm/internal/local"
)

func (s *Store) PrepareAdmission(ctx context.Context, p Principal, q AdmissionRequest) (Operation, error) {
	result, _, err := s.prepareAdmission(ctx, p, q, nil)
	return result, err
}
func (s *Store) prepareAdmission(ctx context.Context, p Principal, q AdmissionRequest, guard func(*Records, Instance) error) (result Operation, created bool, err error) {
	if !validPrincipal(p) || !validKey(q.Key) || !validHex(q.ConsentHash, 32) || q.PolicyRevision < 1 || !reason(q.Reason) {
		return result, false, problem("invalid_request", "exact source/candidate consent and bounded admission request required")
	}
	if _, e := ValidateIdentity(q.Candidate); e != nil {
		return result, false, e
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		if e := enableRecoverySchema(ctx, tx, v); e != nil {
			return e
		}
		root, e := rootOf(v, p.Workspace)
		if e != nil {
			return e
		}
		introducer, e := s.authorize(v, root, p, true)
		if e != nil {
			return e
		}
		if op, ok := operationOf(v, root.ID, principalKey(p), q.Key); ok {
			if e = sameIntent(op, "admit", q); e != nil {
				return e
			}
			result = op.View()
			return nil
		}
		if guard != nil {
			if e = guard(v, root); e != nil {
				return e
			}
		}
		if q.PolicyRevision != root.PolicyRevision || q.Candidate.Host == root.Self.Host || q.Introducer && p.Host != root.Directory.Host || q.HostInbox && !root.Policy.HostInbox || denied(v, root.ID, q.Candidate.Host) {
			return problem("forbidden", "candidate scope/delegation/policy is not currently permitted")
		}
		if _, ok := memberOf(v, root.ID, q.Candidate.Host); ok {
			return problem("identity_already_recorded", "candidate host has a retained identity/tombstone; explicit identity review is required")
		}
		count, _, _ := counts(v, root.ID)
		total := 0
		for _, m := range v.Members {
			if m.Workspace == root.ID {
				total++
			}
		}
		if count >= root.Policy.MaxMembers || total >= MaxMembers {
			return problem("capacity_exhausted", "active/pending members or retained identity roster is full")
		}
		now := s.now()
		if e = identityCurrent(q.Candidate, now); e != nil {
			return e
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = now
		op := newOperation(s, root, "admit", q.Key, principalKey(p), q, now)
		op.Phase = "approved"
		op.ReviewExpires = minTime(now.Add(5*time.Minute), root.Self.Expires, q.Candidate.Expires)
		op.Private.Nonce = local.ID() + local.ID()
		op.Private.Introducer = &p
		op.Private.IntroducerRevision = introducer.Revision
		op.Private.CandidateRevision = 1
		member := Member{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Identity: q.Candidate, MembershipGeneration: 1, State: "pending", Introducer: q.Introducer, HostInbox: q.HostInbox, IntroducedBy: p, AdmissionKey: q.Key, ConsentHash: q.ConsentHash, Revision: 1, Created: now, Updated: now, Reason: q.Reason}
		if e = room(v, root.ID, op); e != nil {
			return e
		}
		if e = writeMember(ctx, tx, member); e != nil {
			return e
		}
		if e = s.fail("admission_pending"); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "admission_prepared", op.Principal, q.Key, op.Phase, &before, root, nil, &member); e != nil {
			return e
		}
		result = op.View()
		created = true
		return nil
	})
	return
}
func minTime(v ...time.Time) time.Time {
	r := v[0]
	for _, x := range v[1:] {
		if x.Before(r) {
			r = x
		}
	}
	return r
}
func (s *Store) admission(v *Records, p Principal, key string) (Instance, Operation, Member, error) {
	root, e := rootOf(v, p.Workspace)
	if e != nil {
		return root, Operation{}, Member{}, e
	}
	introducer, e := s.authorize(v, root, p, true)
	if e != nil {
		return root, Operation{}, Member{}, e
	}
	op, ok := operationOf(v, root.ID, principalKey(p), key)
	if !ok || op.Kind != "admit" {
		return root, op, Member{}, problem("not_found", "admission key not found")
	}
	var q AdmissionRequest
	if e = Decode(op.Request, &q); e != nil {
		return root, op, Member{}, e
	}
	m, ok := memberOf(v, root.ID, q.Candidate.Host)
	if !ok {
		return root, op, m, problem("workspace_state_invalid", "admission candidate missing")
	}
	if op.Phase == "completed" {
		return root, op, m, nil
	}
	if op.Private.Boot != s.boot || op.Private.Introducer == nil || *op.Private.Introducer != p || op.Private.IntroducerRevision != introducer.Revision || q.PolicyRevision != root.PolicyRevision || m.State != "pending" || m.Revision != op.Private.CandidateRevision || m.Identity != q.Candidate || denied(v, root.ID, m.Identity.Host) || !s.now().Before(op.ReviewExpires) {
		return root, op, m, problem("admission_changed", "candidate/delegation/boot/deadline changed; inspect/cancel exact pending admission")
	}
	if e = identityCurrent(m.Identity, s.now()); e != nil {
		return root, op, m, e
	}
	return root, op, m, nil
}
func (s *Store) BeginProof(ctx context.Context, p Principal, key string) (challenge Challenge, result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, op, member, e := s.admission(v, p, key)
		if e != nil {
			return e
		}
		if op.Phase == "completed" {
			result = op.View()
			return nil
		}
		if op.Phase != "approved" {
			return problem("outcome_unresolved", "proof already started/interrupted; lookup/cancel rather than replay")
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = s.now()
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Phase = "proving"
		op.Updated = root.Updated
		if e = room(v, root.ID, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "admission_proving", op.Principal, key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		challenge = Challenge{Introducer: p, CandidateIntroducer: member.Introducer, HostInbox: member.HostInbox, Workspace: root.ID, Generation: root.Generation, Directory: root.Directory, Candidate: member.Identity, ConsentHash: member.ConsentHash, Nonce: op.Private.Nonce, PolicyRevision: root.PolicyRevision, Expires: op.ReviewExpires}
		result = op.View()
		return nil
	})
	return
}
func (s *Store) CompleteProof(ctx context.Context, p Principal, key string, revision int64, proof Proof) (result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, op, member, e := s.admission(v, p, key)
		if e != nil {
			return e
		}
		if op.Phase == "completed" {
			result = op.View()
			return nil
		}
		if op.Phase != "proving" || op.Revision != revision {
			return problem("stale_revision", "admission proof no longer matches current attempt")
		}
		expected := Proof{Workspace: root.ID, Generation: root.Generation, Source: member.Identity.Host, Target: root.Directory.Host, SourcePin: member.Identity.Fingerprint, TargetPin: root.Directory.Fingerprint, ConsentHash: member.ConsentHash, Nonce: op.Private.Nonce}
		if hash(proof) != hash(expected) {
			return problem("forbidden", "candidate proof did not bind the exact reviewed identities/consent/nonce")
		}
		before, oldMember := root, member
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = s.now()
		member.State = "active"
		member.Revision, e = next(member.Revision)
		if e != nil {
			return e
		}
		member.Updated = root.Updated
		op.Result = &member
		op.Phase = "completed"
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = root.Updated
		if e = room(v, root.ID, op); e != nil {
			return e
		}
		if e = writeMember(ctx, tx, member); e != nil {
			return e
		}
		if e = s.fail("admission_active"); e != nil {
			return e
		}
		if e = writeTrusted(ctx, tx, TrustedMember{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Identity: member.Identity, MembershipGeneration: member.MembershipGeneration, State: "active", FirstSeen: root.Revision, LatestSeen: root.Revision}); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "admission_completed", op.Principal, key, op.Phase, &before, root, &oldMember, &member); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

// RecordProofFailure cannot overwrite cancellation/newer state. It records
// uncertainty, never membership; no new policy-audit slot is consumed by an I/O
// diagnostic. The original durable proving journal and private nonce remain.
func (s *Store) RecordProofFailure(ctx context.Context, p Principal, key string, revision int64) error {
	return s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		op, ok := operationOf(v, p.Workspace, principalKey(p), key)
		if !ok {
			return problem("not_found", "admission not found")
		}
		if op.Revision != revision || op.Phase != "proving" || op.Private.Boot != s.boot {
			return problem("stale_revision", "admission changed while proof failed")
		}
		var e error
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Phase = "unknown"
		op.Error = "candidate proof unavailable; inspect exact pending admission"
		now := s.now()
		if now.Before(op.Updated) {
			now = op.Updated
		}
		op.Updated = now
		return writeOperation(ctx, tx, op)
	})
}

// ProveJoin is called only by a candidate-specific pinned TLS adapter. The
// supplied authenticated directory is transport-derived, not request metadata.
// This internal API is unrouted; a public JSON source identity is never proof.
func (s *Store) ProveJoin(ctx context.Context, directory Identity, c Challenge) (proof Proof, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, c.Workspace)
		if e != nil {
			return e
		}
		if root.Role != "member" || root.State != "joining" || root.Self != s.self || root.Generation != c.Generation || directory != root.Directory || c.Directory != root.Directory || c.Candidate != root.Self || c.ConsentHash != root.ConsentHash || c.PolicyRevision != root.PolicyRevision || !validHex(c.Nonce, 32) || !validPrincipal(c.Introducer) || c.Introducer.Workspace != root.ID || c.Introducer.Generation != root.Generation || c.Expires.IsZero() || !s.now().Before(c.Expires) || c.Expires.After(s.now().Add(5*time.Minute)) || denied(v, root.ID, directory.Host) {
			return problem("forbidden", "candidate proof differs from exact locally approved join")
		}
		clock, e := clockOf(v, root.ID)
		if e != nil {
			return e
		}
		if e = s.checkClock(clock.WallFloor); e != nil {
			return e
		}
		if e = identityCurrent(root.Self, s.now()); e != nil {
			return e
		}
		if e = identityCurrent(directory, s.now()); e != nil {
			return e
		}
		for _, op := range v.Operations {
			if op.Workspace != root.ID || op.Kind != "join" || op.Phase != "approved" || op.ReviewHash != root.ConsentHash {
				continue
			}
			var q JoinRequest
			if e = Decode(op.Request, &q); e != nil {
				return e
			}
			if q.Introducer != c.Introducer.Host || q.RequestIntroducer != c.CandidateIntroducer || op.Review.HostInbox != c.HostInbox {
				return problem("forbidden", "join delegation/endpoint review changed")
			}
			if c.Recovery != nil {
				op, e = s.candidateRecovery(v, root, op, c)
				if e != nil {
					return e
				}
			} else {
				children, err := familyChildren(v, root.ID, OperationRef{Principal: op.Principal, Key: op.Key, RequestHash: op.RequestHash, Revision: op.Revision})
				if err != nil {
					return err
				}
				if len(children) > 0 || op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
					return problem("forbidden", "fresh recovery required instead of original join approval")
				}
			}
			if op.Private.LastChallenge != hash(c) {
				if op.Private.ProofCount >= 4 {
					return problem("too_many_requests", "bounded candidate proof attempts exhausted")
				}
				op.Private.ProofCount++
				op.Private.LastChallenge = hash(c)
				op.Revision, e = next(op.Revision)
				if e != nil {
					return e
				}
				op.Updated = s.now()
				if e = writeOperation(ctx, tx, op); e != nil {
					return e
				}
			}
			proof = Proof{Recovery: c.Recovery, Workspace: root.ID, Generation: root.Generation, Source: root.Self.Host, Target: directory.Host, SourcePin: root.Self.Fingerprint, TargetPin: directory.Fingerprint, ConsentHash: c.ConsentHash, Nonce: c.Nonce}
			return nil
		}
		return problem("forbidden", "approved candidate join missing")
	})
	return
}
