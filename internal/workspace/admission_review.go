package workspace

import (
	"context"
	"time"
)

// NativeAdmissionRequest is an uncredentialed public candidate consent plus the
// authenticated local owner's exact command. The principal is derived locally.
type NativeAdmissionRequest struct {
	Workspace string  `json:"workspace"`
	Key       string  `json:"key"`
	Consent   Consent `json:"candidate_consent"`
	Reason    string  `json:"reason"`
}
type AdmissionReview struct {
	Request   NativeAdmissionRequest `json:"request"`
	Source    Identity               `json:"source"`
	Directory Identity               `json:"directory"`
	Revision  int64                  `json:"workspace_revision"`
	Policy    Policy                 `json:"policy"`
	Expires   time.Time              `json:"expires_at"`
	Hash      string                 `json:"review_sha256"`
}

func (v AdmissionReview) digest() string { v.Hash = ""; return hash(v) }
func (s *Store) nativeAdmissionReview(v *Records, root Instance, q NativeAdmissionRequest) (AdmissionReview, error) {
	if !validKey(q.Key) || !reason(q.Reason) || q.Workspace != root.ID || root.Role != "directory" || root.Directory != s.self || root.Self != s.self {
		return AdmissionReview{}, problem("forbidden", "native admission must use this exact directory authority")
	}
	if e := q.Consent.Validate(s.now()); e != nil {
		return AdmissionReview{}, e
	}
	c := q.Consent
	if c.Workspace != root.ID || c.Generation != root.Generation || c.Directory != root.Directory || c.PolicyRevision != root.PolicyRevision || c.Recovery != nil {
		return AdmissionReview{}, problem("review_changed", "candidate consent differs from current directory; recovery needs its linked recovery workflow")
	}
	p := Principal{Workspace: root.ID, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: root.SelfGeneration, Fingerprint: root.Self.Fingerprint}
	if _, e := s.authorize(v, root, p, true); e != nil {
		return AdmissionReview{}, e
	}
	if c.Introducer && root.Policy.AdmissionMode != "delegated_introducers" || c.HostInbox && !root.Policy.HostInbox || denied(v, root.ID, c.Candidate.Host) {
		return AdmissionReview{}, problem("forbidden", "candidate delegation or inbox is not permitted")
	}
	r := AdmissionReview{Request: q, Source: root.Self, Directory: root.Directory, Revision: root.Revision, Policy: root.Policy, Expires: c.Expires}
	r.Hash = r.digest()
	return r, nil
}
func (s *Store) ReviewNativeAdmission(ctx context.Context, q NativeAdmissionRequest) (AdmissionReview, error) {
	v, e := s.Read(ctx)
	if e != nil {
		return AdmissionReview{}, e
	}
	root, e := rootOf(&v, q.Workspace)
	if e != nil {
		return AdmissionReview{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nativeAdmissionReview(&v, root, q)
}

// PrepareNativeAdmission checks the reviewed complete content INSIDE the same
// transaction as the pending admission. A known exact key returns its existing
// operation with created=false and must never trigger an automatic proof replay.
func (s *Store) PrepareNativeAdmission(ctx context.Context, q NativeAdmissionRequest, review string) (Operation, bool, error) {
	if !validHex(review, 32) {
		return Operation{}, false, problem("invalid_request", "exact admission review hash required")
	}
	v, e := s.Read(ctx)
	if e != nil {
		return Operation{}, false, e
	}
	root, e := rootOf(&v, q.Workspace)
	if e != nil {
		return Operation{}, false, e
	}
	p := Principal{Workspace: root.ID, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: root.SelfGeneration, Fingerprint: root.Self.Fingerprint}
	c := q.Consent
	request := AdmissionRequest{Key: q.Key, Candidate: c.Candidate, ConsentHash: c.Consent, Introducer: c.Introducer, HostInbox: c.HostInbox, PolicyRevision: c.PolicyRevision, Reason: q.Reason}
	return s.prepareAdmission(ctx, p, request, func(records *Records, current Instance) error {
		r, e := s.nativeAdmissionReview(records, current, q)
		if e != nil {
			return e
		}
		if r.Hash != review {
			return problem("stale_review", "directory, policy, candidate consent or request changed; review again")
		}
		return nil
	})
}
