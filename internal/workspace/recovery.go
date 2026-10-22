package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/local"
)

func (s *Store) recoveryParent(v *Records, root Instance, kind string, q RecoveryRequest, current bool) (Operation, Identity, error) {
	parent, ok := operationOf(v, root.ID, q.Original.Principal, q.Original.Key)
	if !ok || parent.RequestHash != q.Original.RequestHash || parent.Revision < q.Original.Revision || q.Workspace != root.ID || q.Generation != root.Generation || q.DirectoryPin != root.Directory.Fingerprint || q.PolicyRevision != root.PolicyRevision || root.State == "retired" || denied(v, root.ID, root.Self.Host) || denied(v, root.ID, root.Directory.Host) {
		return parent, Identity{}, problem("recovery_changed", "original workspace/identity/policy/deny differs")
	}
	var candidate Identity
	if kind == "resume_join" && parent.Kind == "join" && root.Role == "member" {
		if parent.Principal != nativeKey(s.authority) || root.State != "joining" || parent.Phase != "approved" || q.Consent != parent.ReviewHash || q.HostInbox != parent.Review.HostInbox || q.Introducer != parent.Review.Introducer || q.CandidateAuthorization != "" {
			return parent, candidate, problem("recovery_changed", "original candidate join is not recoverable")
		}
		candidate = root.Self
	} else if kind == "resume_admission" && parent.Kind == "admit" && root.Role == "directory" {
		var original AdmissionRequest
		if e := Decode(parent.Request, &original); e != nil {
			return parent, candidate, e
		}
		candidate = original.Candidate
		m, ok := memberOf(v, root.ID, candidate.Host)
		if !ok || !known(m.State, "pending", "active") || q.Consent != original.ConsentHash || q.HostInbox != original.HostInbox || q.Introducer != original.Introducer || terminal(parent.Phase) && parent.Phase != "completed" || q.PermitProof && !validHex(q.CandidateAuthorization, 32) {
			return parent, candidate, problem("recovery_changed", "original admission/member/scope is not recoverable")
		}
		if current {
			if _, e := s.authorize(v, root, *parent.Private.Introducer, true); e != nil {
				return parent, candidate, e
			}
		}
	} else {
		return parent, candidate, problem("invalid_request", "recovery kind differs from original family")
	}
	if q.CandidatePin != candidate.Fingerprint || denied(v, root.ID, candidate.Host) {
		return parent, candidate, problem("recovery_changed", "original candidate pin/deny differs")
	}
	if current {
		if root.Self != s.self {
			return parent, candidate, problem("identity_changed", "current local identity differs from original binding")
		}
		clock, e := clockOf(v, root.ID)
		if e != nil {
			return parent, candidate, e
		}
		if e = s.checkClock(clock.WallFloor); e != nil {
			return parent, candidate, e
		}
		for _, identity := range []Identity{root.Self, root.Directory, candidate} {
			if e = identityCurrent(identity, s.now()); e != nil {
				return parent, candidate, e
			}
		}
	}
	return parent, candidate, nil
}

func familyChildren(v *Records, id string, original OperationRef) ([]Operation, error) {
	children := make([]Operation, MaxRecoveryAttempts)
	count := 0
	for _, op := range v.Operations {
		if op.Workspace != id || !recoveryKind(op.Kind) {
			continue
		}
		q, e := recoveryRequest(op)
		if e != nil {
			return nil, e
		}
		if q.Original.Principal != original.Principal || q.Original.Key != original.Key {
			continue
		}
		if q.Original.RequestHash != original.RequestHash || children[q.Attempt-1].Key != "" {
			return nil, problem("workspace_state_invalid", "recovery family request/fork differs")
		}
		children[q.Attempt-1] = op
		count++
	}
	previous := ""
	for i := 0; i < count; i++ {
		if children[i].Key == "" {
			return nil, problem("workspace_state_invalid", "recovery attempt chain has a gap")
		}
		q, _ := recoveryRequest(children[i])
		if q.Previous != previous || i < count-1 && !terminal(children[i].Phase) {
			return nil, problem("workspace_state_invalid", "recovery predecessor is unresolved or differs")
		}
		previous = children[i].Key
	}
	return children[:count], nil
}

// PrepareRecovery records only a fresh review; it never sends or retries proof.
func (s *Store) PrepareRecovery(ctx context.Context, kind string, q RecoveryRequest) (result Operation, err error) {
	if !recoveryKind(kind) || !validRecoveryRequest(q) {
		return result, problem("invalid_request", "exact bounded recovery family request required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, q.Workspace)
		if e != nil {
			return e
		}
		if old, found := operationOf(v, root.ID, nativeKey(s.authority), q.Key); found {
			if e = sameIntent(old, kind, q); e != nil {
				return e
			}
			result = old.View()
			return nil
		}
		if e = enableRecoverySchema(ctx, tx, v); e != nil {
			return e
		}
		parent, candidate, e := s.recoveryParent(v, root, kind, q, true)
		if e != nil {
			return e
		}
		if parent.Phase == "completed" && q.PermitProof {
			return problem("already_completed", "read-only reconciliation is required before any further proof")
		}
		if parent.Revision != q.Original.Revision {
			return problem("stale_revision", "original family changed before recovery preparation")
		}
		children, e := familyChildren(v, root.ID, q.Original)
		if e != nil {
			return e
		}
		if len(children) >= MaxRecoveryAttempts || q.Attempt != len(children)+1 {
			return problem("recovery_limit", "recovery attempt budget exhausted or sequence differs")
		}
		previous := ""
		if len(children) > 0 {
			last := children[len(children)-1]
			if !terminal(last.Phase) {
				return problem("operation_busy", "previous recovery remains unresolved; inspect/cancel it explicitly")
			}
			previous = last.Key
		}
		if q.Previous != previous {
			return problem("stale_revision", "recovery predecessor changed")
		}
		now := s.now()
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = now
		op := newOperation(s, root, kind, q.Key, nativeKey(s.authority), q, now)
		op.Review = &Review{Name: root.Name, RequestHash: op.RequestHash, Schema: Schema, Kind: kind, Workspace: root.ID, Generation: root.Generation, Source: root.Self, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Candidate: &candidate, Introducer: q.Introducer, HostInbox: q.HostInbox, Reason: q.Reason, Expires: minTime(now.Add(5*time.Minute), root.Self.Expires, root.Directory.Expires, candidate.Expires)}
		op.ReviewHash, op.ReviewExpires = hash(op.Review), op.Review.Expires
		if kind == "resume_admission" {
			op.Private.Nonce = local.ID() + local.ID()
			op.Private.Introducer = parent.Private.Introducer
			op.Private.IntroducerRevision = parent.Private.IntroducerRevision
			member, _ := memberOf(v, root.ID, candidate.Host)
			op.Private.CandidateRevision = member.Revision
		}
		if e = room(v, root.ID, op); e != nil {
			return e
		}
		// Every old attempt's final CAS becomes stale. Historical worker boot
		// and nonce are preserved; only this new child can be approved to run.
		parent.Revision, e = next(parent.Revision)
		if e != nil {
			return e
		}
		parent.Updated = now
		if e = writeOperation(ctx, tx, parent); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = journal(ctx, tx, "recovery_prepared", op.Principal, op.Key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

func (s *Store) DecideRecovery(ctx context.Context, id, key string, revision int64, review string, approve bool) (Operation, error) {
	return s.setRecoveryDecision(ctx, id, key, revision, review, approve, false)
}
func (s *Store) CancelRecovery(ctx context.Context, id, key string, revision int64) (Operation, error) {
	return s.setRecoveryDecision(ctx, id, key, revision, "", false, true)
}
func (s *Store) setRecoveryDecision(ctx context.Context, id, key string, revision int64, review string, approve, cancel bool) (result Operation, err error) {
	if !validHex(id, 16) || !validKey(key) || revision < 1 || !cancel && !validHex(review, 32) {
		return result, problem("invalid_request", "exact recovery key/revision/review required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		op, ok := operationOf(v, id, nativeKey(s.authority), key)
		if !ok || !recoveryKind(op.Kind) {
			return problem("not_found", "recovery child not found")
		}
		if !cancel && op.ReviewHash != review {
			return problem("review_changed", "recovery review differs")
		}
		if terminal(op.Phase) || approve && op.Phase == "approved" {
			if approve && known(op.Phase, "approved", "completed") || !approve && known(op.Phase, "declined", "cancelled") {
				result = op.View()
				return nil
			}
			return problem("decision_conflict", "recorded recovery decision differs")
		}
		if op.Revision != revision || !cancel && op.Phase != "awaiting_review" {
			return problem("stale_revision", "recovery child changed")
		}
		q, e := recoveryRequest(op)
		if e != nil {
			return e
		}
		if approve {
			if _, _, e = s.recoveryParent(v, root, op.Kind, q, true); e != nil {
				return e
			}
			if op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
				return problem("review_expired", "recovery review expired/restarted; decline and prepare a fresh child")
			}
		}
		before := root
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = s.now()
		if root.Updated.Before(before.Updated) {
			root.Updated = before.Updated
		}
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = root.Updated
		op.Phase = "declined"
		if approve {
			op.Phase = "approved"
		} else if cancel {
			op.Phase = "cancelled"
		}
		if e = room(v, id, op); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = journal(ctx, tx, "recovery_decision", op.Principal, key, op.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

func validateRecoveryFamilies(v Records, a Authority) error {
	for _, op := range v.Operations {
		if !recoveryKind(op.Kind) {
			continue
		}
		q, e := recoveryRequest(op)
		if e != nil {
			return e
		}
		root, e := rootOf(&v, op.Workspace)
		if e != nil {
			return e
		}
		parent, ok := operationOf(&v, op.Workspace, q.Original.Principal, q.Original.Key)
		if !ok || parent.RequestHash != q.Original.RequestHash || parent.Revision <= q.Original.Revision || op.Principal != nativeKey(a) || root.Generation != q.Generation || root.Directory.Fingerprint != q.DirectoryPin || root.PolicyRevision != q.PolicyRevision {
			return fmt.Errorf("recovery child has no exact original family")
		}
		if _, e = familyChildren(&v, root.ID, q.Original); e != nil {
			return e
		}
		var candidate Identity
		if op.Kind == "resume_join" {
			if parent.Kind != "join" || q.Consent != parent.ReviewHash || q.Introducer != parent.Review.Introducer || q.HostInbox != parent.Review.HostInbox || q.CandidateAuthorization != "" {
				return fmt.Errorf("recovery join original consent/scope differs")
			}
			candidate = root.Self
		} else {
			var original AdmissionRequest
			if parent.Kind != "admit" || Decode(parent.Request, &original) != nil || q.Consent != original.ConsentHash || q.Introducer != original.Introducer || q.HostInbox != original.HostInbox || q.PermitProof && !validHex(q.CandidateAuthorization, 32) {
				return fmt.Errorf("recovery admission original consent/scope differs")
			}
			candidate = original.Candidate
		}
		opBound, changeBound := familyRecordBounds(root, parent, candidate)
		if recordBytes(op) > opBound {
			return fmt.Errorf("recovery operation exceeds its reserved evidence bound")
		}
		for _, change := range v.Changes {
			if change.Workspace == op.Workspace && change.Key == op.Key && recordBytes(change) > changeBound {
				return fmt.Errorf("recovery change exceeds reserved evidence bound")
			}
		}
		if op.Kind == "resume_admission" {
			if !validHex(op.Private.Nonce, 32) || op.Private.Introducer == nil || parent.Private.Introducer == nil || *op.Private.Introducer != *parent.Private.Introducer || op.Private.IntroducerRevision < 1 || op.Private.CandidateRevision < 1 {
				return fmt.Errorf("recovery admission worker lineage differs")
			}
		} else if op.Private.Nonce != "" || op.Private.Introducer != nil || op.Private.IntroducerRevision != 0 || op.Private.CandidateRevision != 0 {
			return fmt.Errorf("candidate recovery carries directory worker authority")
		}
		if op.Review == nil || op.Review.Candidate == nil || *op.Review.Candidate != candidate || op.Review.Source != root.Self || op.Review.Directory != root.Directory || op.Review.Policy != root.Policy || op.Review.Generation != root.Generation || op.Review.PolicyRevision != root.PolicyRevision || op.Review.Introducer != q.Introducer || op.Review.HostInbox != q.HostInbox || op.Review.Reason != q.Reason || q.CandidatePin != candidate.Fingerprint {
			return fmt.Errorf("recovery review differs from original identity/policy")
		}
	}
	return nil
}
