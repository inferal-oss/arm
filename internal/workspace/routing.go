package workspace

import (
	"context"
	"database/sql"
	"time"
)

const RouteMessage = "message"
const RouteDirectory = "directory"
const RouteSSH = "ssh"

// RouteAccess is ephemeral current ledger authorization, never a wire grant.
// The immediate direct-pair identity is authenticated separately by transport.
// Origin and target here are ORIGINAL end-to-end workspace endpoints.
type RouteAccess struct {
	At                                      time.Time
	Workspace, Generation                   string
	GraphHash                               string
	Local, Origin, Destination, Directory   Identity
	OriginGeneration, DestinationGeneration int64
	DirectoryRevision                       int64
	DestinationInbox                        bool
	Expires                                 time.Time
	Remaining                               time.Duration
}

func (s *Store) routeAccess(v *Records, cached authorizedSnapshot, id, origin, target, class string) (RouteAccess, error) {
	root, e := rootOf(v, id)
	if e != nil {
		return RouteAccess{}, e
	}
	if !validHex(origin, 16) || !validHex(target, 16) || origin == target || !known(class, RouteMessage, RouteDirectory, RouteSSH) || (root.State != "active" && !(root.Role == "member" && root.State == "joining" && class == RouteDirectory && origin == root.Self.Host && target == root.Directory.Host)) || root.Self != s.self || denied(v, id, root.Self.Host) || denied(v, id, root.Directory.Host) || denied(v, id, origin) || denied(v, id, target) {
		return RouteAccess{}, problem("forbidden", "current workspace state/pins/deny do not permit this route")
	}
	if class == RouteDirectory && target != root.Directory.Host {
		return RouteAccess{}, problem("forbidden", "directory route has a different final authority")
	}
	if root.Self.Host != origin && root.Self.Host != target && !root.Policy.Relay {
		return RouteAccess{}, problem("forbidden", "workspace policy does not enable this relay")
	}
	clock, e := clockOf(v, id)
	if e != nil {
		return RouteAccess{}, e
	}
	if e = s.checkClock(clock.WallFloor); e != nil {
		return RouteAccess{}, e
	}
	now, elapsed := s.now(), s.clock.Elapsed()
	out := RouteAccess{At: now, Workspace: id, Generation: root.Generation, Local: root.Self, Directory: root.Directory, Expires: minTime(now.Add(root.Policy.StreamTTL), root.Self.Expires, root.Directory.Expires), Remaining: root.Policy.StreamTTL}
	if root.Role == "directory" {
		lookup := func(host string) (Member, error) {
			m, ok := memberOf(v, id, host)
			if !ok {
				return m, problem("forbidden", "current directory member missing")
			}
			p := Principal{Workspace: id, Generation: root.Generation, Host: host, MembershipGeneration: m.MembershipGeneration, Fingerprint: m.Identity.Fingerprint}
			if _, e = s.authorize(v, root, p, false); e != nil {
				return m, e
			}
			return m, nil
		}
		from, e := lookup(origin)
		if e != nil {
			return out, e
		}
		to, e := lookup(target)
		if e != nil {
			return out, e
		}
		out.Origin, out.Destination = from.Identity, to.Identity
		out.OriginGeneration, out.DestinationGeneration = from.MembershipGeneration, to.MembershipGeneration
		out.DirectoryRevision = root.Revision
		out.DestinationInbox = to.HostInbox
		if g, ok := s.directoryGraphs[id]; ok && g.generation == root.Generation && g.revision == root.Revision && elapsed < g.deadline {
			out.GraphHash = g.hash
		}
	} else if root.Role == "member" {
		// Refreshing the pinned directory is permitted from the durable admitted
		// self binding after restart. It cannot send messages, lookup outcomes or
		// relay another source before a REAL current directory observation exists.
		if class == RouteDirectory && origin == root.Self.Host {
			out.Origin, out.Destination = root.Self, root.Directory
			out.OriginGeneration = root.SelfGeneration
			if out.OriginGeneration == 0 && root.State == "joining" {
				out.OriginGeneration = 1
			}
			out.DestinationGeneration = 1
			out.DirectoryRevision = root.PolicyRevision
			// A directory probe does not REQUIRE a current observation, but may
			// identify an already-active graph without renewing its deadline.
			// After restart/expiry this remains empty and saved paths are only
			// reachability probes, never message/lookup authority.
			if cached.boot == s.boot && cached.hash != "" && cached.generation == root.SelfGeneration && elapsed < cached.deadline && elapsed < cached.graphDeadline {
				for _, observation := range v.Observations {
					if observation.Workspace == id && observation.SnapshotHash == cached.hash && now.Before(observation.Snapshot.ValidUntil) {
						out.GraphHash = cached.graph
						break
					}
				}
			}
		} else {
			if cached.boot != s.boot || cached.hash == "" || cached.generation != root.SelfGeneration || elapsed >= cached.deadline {
				return out, problem("membership_stale", "fresh process-bound directory membership required for routed messages/relay")
			}
			var snapshot Snapshot
			for _, o := range v.Observations {
				if o.Workspace == id && o.SnapshotHash == cached.hash {
					snapshot = o.Snapshot
					break
				}
			}
			if snapshot.Hash == "" || !now.Before(snapshot.ValidUntil) {
				return out, problem("membership_stale", "complete current directory view unavailable")
			}
			lookup := func(host string) (MemberCard, error) {
				m, ok := cardOf(snapshot, host)
				if !ok || m.State != "active" {
					return m, problem("forbidden", "current directory has no active route member")
				}
				var trusted TrustedMember
				for _, t := range v.Trusted {
					if t.Workspace == id && t.Identity.Host == host {
						trusted = t
						break
					}
				}
				if trusted.State != "active" || trusted.Identity != m.Identity || trusted.MembershipGeneration != m.MembershipGeneration {
					return m, problem("identity_changed", "route member pin/generation needs current review")
				}
				return m, nil
			}
			own, e := lookup(root.Self.Host)
			if e != nil || own.Identity != root.Self || own.MembershipGeneration != root.SelfGeneration {
				return out, problem("forbidden", "current complete view has no exact local membership")
			}
			from, e := lookup(origin)
			if e != nil {
				return out, e
			}
			to, e := lookup(target)
			if e != nil {
				return out, e
			}
			out.Origin, out.Destination = from.Identity, to.Identity
			out.OriginGeneration, out.DestinationGeneration = from.MembershipGeneration, to.MembershipGeneration
			out.DirectoryRevision = snapshot.Revision
			if elapsed < cached.graphDeadline {
				out.GraphHash = cached.graph
			}
			out.DestinationInbox = to.HostInbox
			out.Remaining = min(out.Remaining, cached.deadline-elapsed, snapshot.ValidUntil.Sub(now))
			out.Expires = minTime(out.Expires, now.Add(out.Remaining), snapshot.ValidUntil)
		}
	} else {
		return out, problem("forbidden", "unsupported routed workspace role")
	}
	for _, identity := range []Identity{out.Local, out.Directory, out.Origin, out.Destination} {
		if e = identityCurrent(identity, now); e != nil {
			return out, e
		}
		out.Expires = minTime(out.Expires, identity.Expires)
	}
	out.Remaining = min(out.Remaining, out.Expires.Sub(now))
	if out.Remaining <= 0 {
		return out, problem("membership_stale", "route lifetime exhausted")
	}
	return out, nil
}

// RouteTransaction is a Go-only adapter for narrow central-SQL producers. Its
// callback can atomically persist route dedup/inbox evidence under the SAME
// current membership/deny transaction, without a read-then-write gap. It must
// not perform network/native-agent I/O or acquire Manager.mu inside fn.
// Close cancels/joins the callback just like membership finalization.
func (r *Runtime) RouteTransaction(ctx context.Context, id, origin, target, class string, fn func(*sql.Tx, RouteAccess) error) (RouteAccess, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return RouteAccess{}, problem("backend_stopping", "workspace route runtime closing")
	}
	life, cancel := context.WithCancel(r.life)
	r.finalizing.Add(1)
	r.mu.Unlock()
	defer r.finalizing.Done()
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	var out RouteAccess
	var cached authorizedSnapshot
	e := r.store.transactionChecked(life, false, func(tx *sql.Tx, v *Records) error {
		r.mu.Lock()
		cached = r.active[id]
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return problem("backend_stopping", "workspace route runtime closing")
		}
		var e error
		out, e = r.store.routeAccess(v, cached, id, origin, target, class)
		if e != nil {
			return e
		}
		if fn != nil {
			return fn(tx, out)
		}
		return nil
	}, func(v *Records) error {
		// Time passes independently of SQL serialization. A blocked/slow callback
		// must not commit after request-start membership expiry or clock rollback.
		current, e := r.store.routeAccess(v, cached, id, origin, target, class)
		if e == nil {
			out = current
		}
		return e
	})
	return out, e
}
func (r *Runtime) RouteAccess(ctx context.Context, id, origin, target, class string) (RouteAccess, error) {
	return r.RouteTransaction(ctx, id, origin, target, class, nil)
}
