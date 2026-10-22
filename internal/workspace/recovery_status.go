package workspace

import (
	"context"
	"database/sql"
)

// OwnAdmissionStatus must be called only with a TLS-derived authenticated
// candidate, never an identity supplied by native/browser/mesh request JSON.
// It reveals only that exact candidate's existing consent-bound family.
func (s *Store) OwnAdmissionStatus(ctx context.Context, candidate Identity, q AdmissionStatusRequest) (result AdmissionStatus, err error) {
	if !validHex(q.Workspace, 16) || !validHex(q.Generation, 16) || !validHex(q.Source, 16) || !validHex(q.Consent, 32) || !validHex(q.Nonce, 32) || candidate.Host != q.Source {
		return result, problem("invalid_request", "exact own-candidate status request required")
	}
	err = s.transaction(ctx, false, func(_ *sql.Tx, v *Records) error {
		root, e := rootOf(v, q.Workspace)
		if e != nil {
			return e
		}
		if root.Role != "directory" || root.State != "active" || root.Generation != q.Generation || root.Self != s.self || denied(v, root.ID, candidate.Host) {
			return problem("forbidden", "own-family directory status unavailable")
		}
		clock, e := clockOf(v, root.ID)
		if e != nil {
			return e
		}
		if e = s.checkClock(clock.WallFloor); e != nil {
			return e
		}
		for _, i := range []Identity{root.Self, candidate} {
			if e = identityCurrent(i, s.now()); e != nil {
				return e
			}
		}
		m, ok := memberOf(v, root.ID, candidate.Host)
		if !ok || m.Identity != candidate || m.ConsentHash != q.Consent {
			return problem("not_found", "no matching own-candidate admission")
		}
		parent, ok := operationOf(v, root.ID, principalKey(m.IntroducedBy), m.AdmissionKey)
		if !ok || parent.Kind != "admit" {
			return problem("workspace_state_invalid", "own admission origin missing")
		}
		result = AdmissionStatus{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Source: root.Directory.Host, Target: candidate.Host, Nonce: q.Nonce, OriginalPrincipal: parent.Principal, OriginalKey: parent.Key, OriginalRequestHash: parent.RequestHash, Consent: m.ConsentHash, DirectoryPin: root.Directory.Fingerprint, CandidatePin: m.Identity.Fingerprint, Membership: m.MembershipGeneration, Introducer: m.Introducer, HostInbox: m.HostInbox, State: m.State, DirectoryRevision: root.Revision, Observed: s.now()}
		if m.State != "active" {
			return nil
		}
		if parent.Phase != "completed" {
			return problem("workspace_state_invalid", "active own admission has no completed origin")
		}
		completing := parent
		children, e := familyChildren(v, root.ID, OperationRef{Principal: parent.Principal, Key: parent.Key, RequestHash: parent.RequestHash, Revision: parent.Revision})
		if e != nil {
			return e
		}
		for _, child := range children {
			q, e := recoveryRequest(child)
			if e != nil {
				return e
			}
			if child.Phase == "completed" && child.Result != nil && q.PermitProof {
				completing = child
			}
		}
		challenge := Challenge{Introducer: m.IntroducedBy, CandidateIntroducer: m.Introducer, HostInbox: m.HostInbox, Workspace: root.ID, Generation: root.Generation, Directory: root.Directory, Candidate: m.Identity, ConsentHash: m.ConsentHash, Nonce: completing.Private.Nonce, PolicyRevision: root.PolicyRevision, Expires: completing.ReviewExpires}
		if completing.Kind == "resume_admission" {
			recovery, e := recoveryRequest(completing)
			if e != nil {
				return e
			}
			challenge.Recovery = &RecoveryBinding{OriginalKey: parent.Key, AttemptKey: completing.Key, CandidateAuthorization: recovery.CandidateAuthorization}
		}
		result.CompletingAttempt = completing.Key
		result.ChallengeHash = hash(challenge)
		return nil
	})
	return
}

// ReconcileAdmissionRecovery records a separately reviewed read-only resolution
// when this directory already committed the exact original member. It never
// generates a challenge, changes a member or repeats candidate transport.
func (s *Store) ReconcileAdmissionRecovery(ctx context.Context, id, key string, revision int64) (result Operation, err error) {
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		child, ok := operationOf(v, id, nativeKey(s.authority), key)
		if !ok || child.Kind != "resume_admission" {
			return problem("not_found", "admission recovery not found")
		}
		if child.Phase == "completed" {
			result = child.View()
			return nil
		}
		q, e := recoveryRequest(child)
		if e != nil {
			return e
		}
		parent, candidate, e := s.recoveryParent(v, root, child.Kind, q, true)
		if e != nil {
			return e
		}
		m, ok := memberOf(v, id, candidate.Host)
		if q.PermitProof || child.Revision != revision || child.Phase != "approved" || child.Private.Boot != s.boot || !s.now().Before(child.ReviewExpires) || parent.Phase != "completed" || parent.Result == nil || !ok || m.State != "active" || hash(*parent.Result) != hash(m) {
			return problem("recovery_changed", "fresh read-only recovery and exact completed admission required")
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
		child.Phase = "completed"
		child.Result = parent.Result
		child.Updated = root.Updated
		if e = room(v, id, child); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, child); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = journal(ctx, tx, "recovery_reconciled", child.Principal, child.Key, child.Phase, &before, root, nil, nil); e != nil {
			return e
		}
		result = child.View()
		return nil
	})
	return
}
