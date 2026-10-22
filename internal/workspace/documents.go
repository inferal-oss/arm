package workspace

import (
	"context"
	"time"
)

// Invitation is a public, uncredentialed description for an explicit local
// owner review. Its digest detects changed review content, not a signature or
// authorization token. It contains no addresses, private key or member roster.
type Invitation struct {
	Schema         int       `json:"schema_version"`
	Workspace      string    `json:"workspace"`
	Generation     string    `json:"generation"`
	Name           string    `json:"name"`
	Directory      Identity  `json:"directory"`
	Policy         Policy    `json:"policy"`
	PolicyRevision int64     `json:"policy_revision"`
	Expires        time.Time `json:"expires_at"`
	Digest         string    `json:"document_sha256"`
}
type RecoveryConsent struct {
	Key         string `json:"key"`
	RequestHash string `json:"request_sha256"`
	ReviewHash  string `json:"review_sha256"`
}

// Consent is a candidate owner's reviewed request description. Only a real
// pinned candidate proof can turn it into directory membership.
type Consent struct {
	Schema         int              `json:"schema_version"`
	Workspace      string           `json:"workspace"`
	Generation     string           `json:"generation"`
	Directory      Identity         `json:"directory"`
	Candidate      Identity         `json:"candidate"`
	Consent        string           `json:"consent_sha256"`
	PolicyRevision int64            `json:"policy_revision"`
	Introducer     bool             `json:"introducer"`
	HostInbox      bool             `json:"host_inbox_enabled"`
	Expires        time.Time        `json:"expires_at"`
	Recovery       *RecoveryConsent `json:"recovery,omitempty"`
	Digest         string           `json:"document_sha256"`
}

func (v Invitation) digest() string { v.Digest = ""; return hash(v) }
func (v Consent) digest() string    { v.Digest = ""; return hash(v) }
func (v Invitation) Validate(now time.Time) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || !text(v.Name, 128) || v.PolicyRevision < 1 || !v.Expires.After(now) || v.Expires.After(now.Add(5*time.Minute+MaxClockSkew)) || v.Digest != v.digest() {
		return problem("invalid_request", "invalid or expired exact workspace invitation")
	}
	if e := ValidatePolicy(v.Policy); e != nil {
		return e
	}
	return identityCurrent(v.Directory, now)
}
func (v Consent) Validate(now time.Time) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || !validHex(v.Consent, 32) || v.PolicyRevision < 1 || v.Candidate.Host == v.Directory.Host || !v.Expires.After(now) || v.Expires.After(now.Add(5*time.Minute+MaxClockSkew)) || v.Digest != v.digest() {
		return problem("invalid_request", "invalid or expired exact candidate consent")
	}
	if v.Recovery != nil && (!validKey(v.Recovery.Key) || !validHex(v.Recovery.RequestHash, 32) || !validHex(v.Recovery.ReviewHash, 32)) {
		return problem("invalid_request", "invalid candidate recovery review binding")
	}
	for _, identity := range []Identity{v.Directory, v.Candidate} {
		if e := identityCurrent(identity, now); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Invitation(ctx context.Context, id string) (Invitation, error) {
	records, e := s.Read(ctx)
	if e != nil {
		return Invitation{}, e
	}
	root, e := rootOf(&records, id)
	if e != nil {
		return Invitation{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e = s.authorize(&records, root, Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: root.SelfGeneration, Fingerprint: root.Self.Fingerprint}, true); e != nil {
		return Invitation{}, e
	}
	v := Invitation{Schema: Schema, Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Expires: minTime(s.now().Add(5*time.Minute), root.Directory.Expires)}
	v.Digest = v.digest()
	return v, nil
}
func (s *Store) Consent(ctx context.Context, id, key string) (Consent, error) {
	records, e := s.Read(ctx)
	if e != nil {
		return Consent{}, e
	}
	root, e := rootOf(&records, id)
	if e != nil {
		return Consent{}, e
	}
	op, ok := operationOf(&records, id, nativeKey(s.authority), key)
	if !ok || !known(op.Kind, "join", "resume_join") {
		return Consent{}, problem("not_found", "candidate join/recovery intent not found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clock, e := clockOf(&records, id)
	if e != nil {
		return Consent{}, e
	}
	if e = s.checkClock(clock.WallFloor); e != nil {
		return Consent{}, e
	}
	if denied(&records, id, root.Directory.Host) || denied(&records, id, root.Self.Host) {
		return Consent{}, problem("forbidden", "workspace authority is denied")
	}
	if root.Role != "member" || root.State != "joining" || root.Self != s.self || op.Phase != "approved" || op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
		return Consent{}, problem("review_expired", "fresh current-boot approved candidate consent required")
	}
	v := Consent{Schema: Schema, Workspace: id, Generation: root.Generation, Directory: root.Directory, Candidate: root.Self, Consent: root.ConsentHash, PolicyRevision: root.PolicyRevision, Introducer: op.Review.Introducer, HostInbox: op.Review.HostInbox, Expires: op.ReviewExpires}
	if op.Kind == "resume_join" {
		q, e := recoveryRequest(op)
		if e != nil {
			return v, e
		}
		if !q.PermitProof {
			return v, problem("forbidden", "read-only candidate recovery does not authorize another proof")
		}
		v.Recovery = &RecoveryConsent{Key: op.Key, RequestHash: op.RequestHash, ReviewHash: op.ReviewHash}
	} else {
		children, e := familyChildren(&records, id, OperationRef{Principal: op.Principal, Key: op.Key, RequestHash: op.RequestHash, Revision: op.Revision})
		if e != nil {
			return v, e
		}
		if len(children) > 0 {
			return v, problem("review_expired", "export the fresh recovery child consent instead of original approval")
		}
	}
	v.Digest = v.digest()
	if e = v.Validate(s.now()); e != nil {
		return Consent{}, e
	}
	return v, nil
}
