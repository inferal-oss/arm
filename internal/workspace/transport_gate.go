package workspace

import (
	"context"
	"database/sql"
	"time"
)

const (
	TransportProof     = "join_proof"
	TransportDirectory = "directory"
	TransportStatus    = "admission_status"
)

// TransportPermit is a short, in-process gate result for a closed operation.
// It is never serialized as a transferable token or direct-peer/resource grant.
// Callers must still authenticate exact certificates and recheck the gate before
// processing the inner request and during a bounded stream.
type TransportPermit struct {
	Workspace, Generation, Source, Target, Class, Consent string
	Local, Remote                                         Identity
	Principal                                             Principal
	Expires                                               time.Time
}

func (s *Store) joinTransportApproval(v *Records, root Instance, proof bool) (time.Time, error) {
	for _, original := range v.Operations {
		if original.Workspace != root.ID || original.Kind != "join" || original.Principal != nativeKey(s.authority) || original.Phase != "approved" || original.ReviewHash != root.ConsentHash {
			continue
		}
		children, e := familyChildren(v, root.ID, OperationRef{Principal: original.Principal, Key: original.Key, RequestHash: original.RequestHash, Revision: original.Revision})
		if e != nil {
			return time.Time{}, e
		}
		op := original
		if len(children) > 0 {
			op = children[len(children)-1]
			q, e := recoveryRequest(op)
			if e != nil {
				return time.Time{}, e
			}
			if op.Kind != "resume_join" || proof && !q.PermitProof {
				return time.Time{}, problem("forbidden", "candidate recovery does not permit requested stream")
			}
		}
		if op.Phase != "approved" || op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
			return time.Time{}, problem("review_expired", "fresh current-boot candidate review required")
		}
		return op.ReviewExpires, nil
	}
	return time.Time{}, problem("forbidden", "exact approved local candidate intent missing")
}

func (s *Store) proofTransportApproval(v *Records, root Instance, m Member) (time.Time, error) {
	parent, ok := operationOf(v, root.ID, principalKey(m.IntroducedBy), m.AdmissionKey)
	if !ok || parent.Kind != "admit" || m.State != "pending" {
		return time.Time{}, problem("forbidden", "exact pending directory admission missing")
	}
	if _, e := s.authorize(v, root, m.IntroducedBy, true); e != nil {
		return time.Time{}, e
	}
	children, e := familyChildren(v, root.ID, OperationRef{Principal: parent.Principal, Key: parent.Key, RequestHash: parent.RequestHash, Revision: parent.Revision})
	if e != nil {
		return time.Time{}, e
	}
	op := parent
	if len(children) > 0 {
		op = children[len(children)-1]
		q, e := recoveryRequest(op)
		if e != nil {
			return time.Time{}, e
		}
		if op.Kind != "resume_admission" || !q.PermitProof {
			return time.Time{}, problem("forbidden", "current recovery does not permit candidate proof")
		}
	}
	if op.Phase != "proving" || op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
		return time.Time{}, problem("forbidden", "candidate stream has no durable current proof dispatch")
	}
	return op.ReviewExpires, nil
}

// AuthorizeTransport derives identities/principals from the current complete
// ledger. Request host fields select records; they never authenticate a caller.
// Only the private server adapter can apply this result to its verified outer
// pair and its normal pinned inner TLS connection. No public RPC exposes it.
func (s *Store) AuthorizeTransport(ctx context.Context, id, generation, source, target, class, consent string) (permit TransportPermit, err error) {
	if !validHex(id, 16) || !validHex(generation, 16) || !validHex(source, 16) || !validHex(target, 16) || source == target || !known(class, TransportProof, TransportDirectory, TransportStatus) || class == TransportDirectory && consent != "" || class != TransportDirectory && !validHex(consent, 32) {
		return permit, problem("invalid_request", "exact closed workspace transport selectors required")
	}
	err = s.transaction(ctx, false, func(_ *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		if root.Generation != generation || root.Self != s.self || !known(root.State, "joining", "active") || denied(v, id, root.Self.Host) || denied(v, id, root.Directory.Host) || root.Self.Host != source && root.Self.Host != target {
			return problem("forbidden", "workspace local authority/state/deny differs")
		}
		clock, e := clockOf(v, id)
		if e != nil {
			return e
		}
		if e = s.checkClock(clock.WallFloor); e != nil {
			return e
		}
		permit = TransportPermit{Workspace: id, Generation: generation, Source: source, Target: target, Class: class, Consent: consent, Local: root.Self, Expires: minTime(s.now().Add(root.Policy.StreamTTL), root.Self.Expires)}
		if root.Role == "member" {
			if class == TransportProof {
				if target != root.Self.Host || source != root.Directory.Host || root.State != "joining" || consent != root.ConsentHash {
					return problem("forbidden", "proof differs from local candidate binding")
				}
				expiry, e := s.joinTransportApproval(v, root, true)
				if e != nil {
					return e
				}
				permit.Expires = minTime(permit.Expires, expiry)
			} else {
				if source != root.Self.Host || target != root.Directory.Host || class == TransportStatus && consent != root.ConsentHash {
					return problem("forbidden", "directory read differs from local candidate/member binding")
				}
				if root.State == "joining" {
					expiry, e := s.joinTransportApproval(v, root, false)
					if e != nil {
						return e
					}
					permit.Expires = minTime(permit.Expires, expiry)
				} else if class == TransportStatus {
					return problem("forbidden", "own admission recovery is only for a joining member")
				}
				membership := root.SelfGeneration
				if membership == 0 {
					membership = 1
				}
				permit.Principal = Principal{Workspace: id, Generation: generation, Host: root.Self.Host, MembershipGeneration: membership, Fingerprint: root.Self.Fingerprint}
			}
			permit.Remote = root.Directory
		} else if root.Role == "directory" && root.State == "active" {
			remote := source
			if class == TransportProof {
				if source != root.Self.Host {
					return problem("forbidden", "directory candidate proof direction differs")
				}
				remote = target
			} else if target != root.Self.Host {
				return problem("forbidden", "directory read direction differs")
			}
			member, ok := memberOf(v, id, remote)
			if !ok || denied(v, id, remote) {
				return problem("forbidden", "workspace candidate/member is not recorded or is denied")
			}
			permit.Remote = member.Identity
			switch class {
			case TransportProof:
				if member.ConsentHash != consent {
					return problem("forbidden", "candidate proof consent differs")
				}
				expiry, e := s.proofTransportApproval(v, root, member)
				if e != nil {
					return e
				}
				permit.Expires = minTime(permit.Expires, expiry)
			case TransportDirectory:
				principal := Principal{Workspace: id, Generation: generation, Host: member.Identity.Host, MembershipGeneration: member.MembershipGeneration, Fingerprint: member.Identity.Fingerprint}
				if _, e = s.authorize(v, root, principal, false); e != nil {
					return e
				}
				permit.Principal = principal
			case TransportStatus:
				if member.ConsentHash != consent || !known(member.State, "pending", "active", "revoked") {
					return problem("forbidden", "own admission status binding differs")
				}
			}
		} else {
			return problem("forbidden", "unsupported workspace role/direction")
		}
		for _, identity := range []Identity{permit.Local, permit.Remote} {
			if e = identityCurrent(identity, s.now()); e != nil {
				return e
			}
			permit.Expires = minTime(permit.Expires, identity.Expires)
		}
		if !s.now().Before(permit.Expires) {
			return problem("review_expired", "workspace stream authorization expired")
		}
		return nil
	})
	return
}
