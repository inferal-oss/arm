package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/inferal-oss/arm/internal/local"
)

func newOperation(s *Store, root Instance, kind, key, principal string, request any, now time.Time) Operation {
	raw, _ := json.Marshal(request)
	return Operation{Schema: Schema, Workspace: root.ID, Principal: principal, Key: key, Kind: kind, RequestHash: hash(request), Request: raw, Revision: 1, Phase: "awaiting_review", Private: &OperationPrivate{Boot: s.boot}, Created: now, Updated: now}
}
func journal(ctx context.Context, tx *sql.Tx, kind, actor, key, phase string, before *Instance, after Instance, memberBefore, memberAfter *Member) error {
	return writeChange(ctx, tx, Change{Schema: Schema, Workspace: after.ID, Kind: kind, Actor: actor, Key: key, Revision: after.Revision, At: after.Updated, Before: before, After: after, MemberBefore: memberBefore, MemberAfter: memberAfter, OperationPhase: phase})
}
func sameIntent(op Operation, kind string, request any) error {
	if op.Kind != kind || op.RequestHash != hash(request) {
		return problem("idempotency_conflict", "workspace key belongs to another exact request")
	}
	return nil
}
func (s *Store) PrepareCreate(ctx context.Context, q CreateRequest) (result Operation, err error) {
	if !validKey(q.Key) || !text(q.Name, 128) || !reason(q.Reason) {
		return result, problem("invalid_request", "provide a bounded key/name/reason")
	}
	if e := ValidatePolicy(q.Policy); e != nil {
		return result, problem("invalid_request", e.Error())
	}
	err = s.transaction(ctx, true, func(tx *sql.Tx, v *Records) error {
		for _, op := range v.Operations {
			if op.Kind == "create" && op.Principal == nativeKey(s.authority) && op.Key == q.Key {
				if e := sameIntent(op, "create", q); e != nil {
					return e
				}
				result = op.View()
				return nil
			}
		}
		if len(v.Instances) >= MaxWorkspaces {
			return problem("capacity_exhausted", "workspace roster is full")
		}
		if e := s.checkClock(time.Time{}); e != nil {
			return e
		}
		now := s.now()
		if e := identityCurrent(s.self, now); e != nil {
			return e
		}
		root := Instance{Schema: Schema, Authority: s.authority, ID: local.ID(), Generation: local.ID(), Name: q.Name, Role: "directory", Directory: s.self, Self: s.self, State: "prepared", Revision: 1, PolicyRevision: 1, Policy: q.Policy, SelfGeneration: 1, Created: now, Updated: now}
		op := newOperation(s, root, "create", q.Key, nativeKey(s.authority), q, now)
		op.Review = &Review{Schema: Schema, Name: q.Name, RequestHash: op.RequestHash, Kind: "create", Workspace: root.ID, Generation: root.Generation, Source: s.self, Directory: s.self, Policy: q.Policy, PolicyRevision: 1, Introducer: true, HostInbox: q.Policy.HostInbox, Reason: q.Reason, Expires: now.Add(5 * time.Minute)}
		op.ReviewHash = hash(op.Review)
		op.ReviewExpires = op.Review.Expires
		if e := writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e := s.fail("create_root"); e != nil {
			return e
		}
		if e := writeClock(ctx, tx, ClockRecord{Schema: Schema, Workspace: root.ID, Generation: root.Generation, WallFloor: now, LastRevision: 1}); e != nil {
			return e
		}
		if e := writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e := journal(ctx, tx, "create_prepared", op.Principal, q.Key, op.Phase, nil, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}
func (s *Store) PrepareJoin(ctx context.Context, q JoinRequest) (result Operation, err error) {
	if !validKey(q.Key) || !validHex(q.Workspace, 16) || !validHex(q.Generation, 16) || !text(q.Name, 128) || q.Directory.Host == s.authority.Host || q.PolicyRevision < 1 || !validHex(q.Introducer, 16) || !reason(q.Reason) {
		return result, problem("invalid_request", "exact workspace/directory/introducer identity and bounded request required")
	}
	if e := ValidatePolicy(q.Policy); e != nil {
		return result, e
	}
	if _, e := ValidateIdentity(q.Directory); e != nil {
		return result, e
	}
	err = s.transaction(ctx, true, func(tx *sql.Tx, v *Records) error {
		if old, ok := operationOf(v, q.Workspace, nativeKey(s.authority), q.Key); ok {
			if e := sameIntent(old, "join", q); e != nil {
				return e
			}
			result = old.View()
			return nil
		}
		if _, e := rootOf(v, q.Workspace); e == nil {
			return problem("workspace_exists", "workspace already has a local binding; inspect it rather than replace its epoch/pin")
		}
		if len(v.Instances) >= MaxWorkspaces {
			return problem("capacity_exhausted", "workspace roster is full")
		}
		if e := s.checkClock(time.Time{}); e != nil {
			return e
		}
		now := s.now()
		if e := identityCurrent(s.self, now); e != nil {
			return e
		}
		if e := identityCurrent(q.Directory, now); e != nil {
			return e
		}
		root := Instance{Schema: Schema, Authority: s.authority, ID: q.Workspace, Generation: q.Generation, Name: q.Name, Role: "member", Directory: q.Directory, Self: s.self, State: "prepared", Revision: 1, PolicyRevision: q.PolicyRevision, Policy: q.Policy, Created: now, Updated: now}
		op := newOperation(s, root, "join", q.Key, nativeKey(s.authority), q, now)
		op.Review = &Review{Schema: Schema, Name: q.Name, RequestHash: op.RequestHash, Kind: "join", Workspace: root.ID, Generation: root.Generation, Source: s.self, Directory: q.Directory, Policy: q.Policy, PolicyRevision: q.PolicyRevision, Introducer: q.RequestIntroducer, HostInbox: q.Policy.HostInbox, Reason: q.Reason, Expires: now.Add(5 * time.Minute)}
		op.ReviewHash = hash(op.Review)
		op.ReviewExpires = op.Review.Expires
		if e := writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e := writeClock(ctx, tx, ClockRecord{Schema: Schema, Workspace: root.ID, Generation: root.Generation, WallFloor: now, LastRevision: 1}); e != nil {
			return e
		}
		if e := writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e := journal(ctx, tx, "join_prepared", op.Principal, q.Key, op.Phase, nil, root, nil, nil); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}

func (s *Store) Decide(ctx context.Context, id, key string, revision int64, reviewHash string, approve bool) (result Operation, err error) {
	if !validHex(id, 16) || !validKey(key) || revision < 1 || !validHex(reviewHash, 32) {
		return result, problem("invalid_request", "exact workspace/key/revision/review hash required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, id)
		if e != nil {
			return e
		}
		op, ok := operationOf(v, id, nativeKey(s.authority), key)
		if !ok || !known(op.Kind, "create", "join") {
			return problem("not_found", "review operation not found")
		}
		if op.ReviewHash != reviewHash {
			return problem("review_changed", "workspace review differs")
		}
		if terminal(op.Phase) || op.Phase == "approved" {
			if approve && known(op.Phase, "completed", "approved") || !approve && op.Phase == "declined" {
				result = op.View()
				return nil
			}
			return problem("decision_conflict", "recorded decision cannot be reversed")
		}
		if op.Phase != "awaiting_review" || op.Revision != revision {
			return problem("stale_revision", "workspace review revision changed")
		}
		now := s.now()
		if now.Before(root.Updated) {
			now = root.Updated
		}
		before := root
		if approve {
			if root.Self != s.self {
				return problem("identity_changed", "local workspace identity changed; explicit rotation required")
			}
			clock, e := clockOf(v, id)
			if e != nil {
				return e
			}
			if e = s.checkClock(clock.WallFloor); e != nil {
				return e
			}
			if op.Private.Boot != s.boot || !s.now().Before(op.ReviewExpires) {
				return problem("review_expired", "review expired or was prepared by an interrupted process; decline and prepare afresh")
			}
			if e = identityCurrent(root.Self, s.now()); e != nil {
				return e
			}
			if e = identityCurrent(root.Directory, s.now()); e != nil {
				return e
			}
			if root.Role == "directory" {
				root.State = "active"
				op.Phase = "completed"
			} else {
				root.State = "joining"
				op.Phase = "approved"
			}
		} else {
			root.State = "retired"
			op.Phase = "declined"
		}
		root.ConsentHash = op.ReviewHash
		root.Revision, e = next(root.Revision)
		if e != nil {
			return e
		}
		root.Updated = now
		op.Revision, e = next(op.Revision)
		if e != nil {
			return e
		}
		op.Updated = now
		if approve && root.Role == "directory" {
			p := Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
			member := Member{Schema: Schema, Workspace: id, Generation: root.Generation, Identity: root.Self, MembershipGeneration: 1, State: "active", Introducer: true, HostInbox: root.Policy.HostInbox, IntroducedBy: p, AdmissionKey: key, ConsentHash: op.ReviewHash, Revision: 1, Created: now, Updated: now, Reason: op.Review.Reason}
			op.Result = &member
			if e = writeMember(ctx, tx, member); e != nil {
				return e
			}
			if e = s.fail("create_member"); e != nil {
				return e
			}
			if e = writeTrusted(ctx, tx, TrustedMember{Schema: Schema, Workspace: id, Generation: root.Generation, Identity: member.Identity, MembershipGeneration: 1, State: "active", FirstSeen: root.Revision, LatestSeen: root.Revision}); e != nil {
				return e
			}
		}
		if e = room(v, id, op); e != nil {
			return e
		}
		if e = writeInstance(ctx, tx, root); e != nil {
			return e
		}
		if e = writeOperation(ctx, tx, op); e != nil {
			return e
		}
		if e = journal(ctx, tx, "decision", op.Principal, key, op.Phase, &before, root, nil, op.Result); e != nil {
			return e
		}
		result = op.View()
		return nil
	})
	return
}
func (s *Store) Lookup(ctx context.Context, id, key string) (Operation, bool, error) {
	v, e := s.Read(ctx)
	if e != nil {
		return Operation{}, false, e
	}
	o, ok := operationOf(&v, id, nativeKey(s.authority), key)
	return o.View(), ok, nil
}

// MemberLookup checks a CURRENT workspace principal even though returning a
// recorded outcome never dispatches another proof or external operation.
func (s *Store) MemberLookup(ctx context.Context, p Principal, key string) (result Operation, found bool, err error) {
	err = s.transaction(ctx, false, func(_ *sql.Tx, v *Records) error {
		root, e := rootOf(v, p.Workspace)
		if e != nil {
			return e
		}
		if _, e = s.authorize(v, root, p, false); e != nil {
			return e
		}
		op, ok := operationOf(v, root.ID, principalKey(p), key)
		result, found = op.View(), ok
		return nil
	})
	return
}
