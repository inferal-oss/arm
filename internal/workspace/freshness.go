package workspace

import (
	"context"
	"database/sql"
	"sort"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/local"
)

func snapshotHash(v Snapshot) string { v.Hash = ""; return hash(v) }
func validateSnapshot(v Snapshot) error {
	if v.Schema != Schema || !validHex(v.Workspace, 16) || !validHex(v.Generation, 16) || !validHex(v.Requester, 16) || v.RequesterGeneration < 1 || !validHex(v.Nonce, 32) || v.Revision < 1 || v.PolicyRevision < 1 || v.Issued.IsZero() || !v.ValidUntil.After(v.Issued) || v.TTL != v.ValidUntil.Sub(v.Issued) || v.TTL > v.Policy.MembershipTTL || len(v.Members) < 1 || len(v.Members) > MaxMembers || !validHex(v.Hash, 32) || snapshotHash(v) != v.Hash {
		return problem("invalid_snapshot", "invalid complete membership snapshot")
	}
	if e := ValidatePolicy(v.Policy); e != nil {
		return e
	}
	if _, e := ValidateIdentity(v.Directory); e != nil {
		return e
	}
	seen := map[string]bool{}
	active := 0
	for _, c := range v.Members {
		if e := validateCard(c); e != nil {
			return e
		}
		if c.State == "pending" {
			return problem("invalid_snapshot", "unproved pending candidates are not directory members")
		}
		// Retained revoked tombstones preserve pin continuity without consuming
		// active membership capacity. The complete roster still has a hard cap.
		if c.State == "active" {
			active++
			if active > v.Policy.MaxMembers {
				return problem("invalid_snapshot", "active directory membership exceeds approved policy")
			}
		}
		if seen[c.Identity.Host] {
			return problem("invalid_snapshot", "duplicate membership identity")
		}
		seen[c.Identity.Host] = true
	}
	return nil
}
func cardOf(s Snapshot, host string) (MemberCard, bool) {
	for _, m := range s.Members {
		if m.Identity.Host == host {
			return m, true
		}
	}
	return MemberCard{}, false
}
func (s *Store) Issue(ctx context.Context, q RefreshRequest) (result Snapshot, err error) {
	if !validPrincipal(q.Source) || !validHex(q.Nonce, 32) {
		return result, problem("invalid_request", "authenticated source and fresh bounded nonce required")
	}
	err = s.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, q.Source.Workspace)
		if e != nil {
			return e
		}
		member, e := s.authorize(v, root, q.Source, false)
		if e != nil {
			return e
		}
		now := s.now()
		until := minTime(now.Add(root.Policy.MembershipTTL), root.Self.Expires, member.Identity.Expires)
		if !until.After(now) {
			return problem("identity_expired", "fresh membership unavailable")
		}
		result = Snapshot{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Directory: root.Directory, Requester: member.Identity.Host, RequesterGeneration: member.MembershipGeneration, Nonce: q.Nonce, Revision: root.Revision, PolicyRevision: root.PolicyRevision, Policy: root.Policy, Issued: now, ValidUntil: until, TTL: until.Sub(now), Members: []MemberCard{}}
		for _, m := range v.Members {
			if m.Workspace == root.ID && m.State != "pending" {
				result.Members = append(result.Members, MemberCard{m.Identity, m.MembershipGeneration, m.State, m.Introducer, m.HostInbox, m.Revision})
			}
		}
		sort.Slice(result.Members, func(i, j int) bool { return result.Members[i].Identity.Host < result.Members[j].Identity.Host })
		result.Hash = snapshotHash(result)
		if _, e = encode(result); e != nil {
			return e
		}
		clock, e := clockOf(v, root.ID)
		if e != nil {
			return e
		}
		clock.WallFloor = now
		clock.LastIssued = &now
		clock.LastRevision = root.Revision
		clock.IssuedCount, e = next(clock.IssuedCount)
		if e != nil {
			return e
		}
		if e = s.fail("issuance_floor"); e != nil {
			return e
		}
		return writeClock(ctx, tx, clock)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return
}

type refreshTicket struct {
	lifetime               time.Duration
	workspace, nonce, boot string
	request                RefreshRequest
	startWall              time.Time
	startElapsed           time.Duration
}
type authorizedSnapshot struct {
	boot       string
	deadline   time.Duration
	hash       string
	generation int64
}

// Runtime is an ephemeral authorization cache. Reopening the store or creating
// another Runtime never grants authority from serialized observations alone.
type Runtime struct {
	store         *Store
	mu            sync.Mutex
	pending       map[string]refreshTicket
	active        map[string]authorizedSnapshot
	closed        bool
	life          context.Context
	cancel        context.CancelFunc
	finalizing    sync.WaitGroup
	statusPending map[string]statusTicket
	statusActive  map[string]statusObservation
}

func NewRuntime(s *Store) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{life: ctx, cancel: cancel, store: s, pending: map[string]refreshTicket{}, statusPending: map[string]statusTicket{}, statusActive: map[string]statusObservation{}, active: map[string]authorizedSnapshot{}}
}
func (r *Runtime) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	clear(r.pending)
	clear(r.active)
	clear(r.statusPending)
	clear(r.statusActive)
	r.mu.Unlock()
	r.finalizing.Wait()
}
func (r *Runtime) Begin(ctx context.Context, id string) (RefreshRequest, error) {
	v, e := r.store.Read(ctx)
	if e != nil {
		return RefreshRequest{}, e
	}
	root, e := rootOf(&v, id)
	if e != nil {
		return RefreshRequest{}, e
	}
	if root.Role != "member" || !known(root.State, "joining", "active") || root.Self != r.store.self || denied(&v, id, root.Directory.Host) || denied(&v, id, root.Self.Host) {
		return RefreshRequest{}, problem("forbidden", "workspace binding is not active/joining")
	}
	clock, e := clockOf(&v, id)
	if e != nil {
		return RefreshRequest{}, e
	}
	r.store.mu.Lock()
	e = r.store.checkClock(clock.WallFloor)
	wall, elapsed := r.store.now(), r.store.clock.Elapsed()
	r.store.mu.Unlock()
	if e != nil {
		return RefreshRequest{}, e
	}
	if e = identityCurrent(root.Self, wall); e != nil {
		return RefreshRequest{}, e
	}
	if e = identityCurrent(root.Directory, wall); e != nil {
		return RefreshRequest{}, e
	}
	generation := root.SelfGeneration
	if generation == 0 {
		generation = 1
	}
	q := RefreshRequest{Source: Principal{id, root.Generation, root.Self.Host, generation, root.Self.Fingerprint}, Nonce: local.ID() + local.ID()}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return RefreshRequest{}, problem("backend_stopping", "workspace runtime closed")
	}
	for nonce, t := range r.pending {
		if elapsed >= t.startElapsed+t.lifetime {
			delete(r.pending, nonce)
		}
	}
	if len(r.pending) >= 16 {
		return RefreshRequest{}, problem("too_many_requests", "bounded refresh requests are occupied")
	}
	for _, t := range r.pending {
		if t.workspace == id {
			return RefreshRequest{}, problem("operation_busy", "a refresh is already pending for this workspace")
		}
	}
	r.pending[q.Nonce] = refreshTicket{lifetime: root.Policy.MembershipTTL, workspace: id, nonce: q.Nonce, boot: r.store.boot, request: q, startWall: wall, startElapsed: elapsed}
	return q, nil
}

// Accept requires a TLS-derived exact directory identity from the future closed
// transport adapter. Caller JSON saying 'directory' is not authentication.
func (r *Runtime) Accept(ctx context.Context, authenticatedDirectory Identity, snapshot Snapshot) error {
	r.mu.Lock()
	t, ok := r.pending[snapshot.Nonce]
	if ok {
		delete(r.pending, snapshot.Nonce)
		delete(r.active, t.workspace)
	}
	closed := r.closed
	r.mu.Unlock()
	if !ok || closed || t.boot != r.store.boot {
		return problem("stale_refresh", "no current process refresh matches this nonce")
	}
	if e := validateSnapshot(snapshot); e != nil {
		return e
	}
	if snapshot.Workspace != t.workspace || snapshot.Generation != t.request.Source.Generation || snapshot.Requester != t.request.Source.Host || snapshot.RequesterGeneration != t.request.Source.MembershipGeneration {
		return problem("invalid_snapshot", "response differs from exact request source/target")
	}
	var deadline time.Duration
	e := r.store.transaction(ctx, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, t.workspace)
		if e != nil {
			return e
		}
		if root.Role != "member" || !known(root.State, "joining", "active") || root.Self != r.store.self || root.Directory != authenticatedDirectory || snapshot.Directory != root.Directory || root.Policy != snapshot.Policy || root.PolicyRevision != snapshot.PolicyRevision || denied(v, root.ID, root.Directory.Host) || denied(v, root.ID, root.Self.Host) {
			return problem("forbidden", "current local membership/policy/pins/deny changed")
		}
		clock, e := clockOf(v, root.ID)
		if e != nil {
			return e
		}
		if e = r.store.checkClock(clock.WallFloor); e != nil {
			return e
		}
		now := r.store.now()
		elapsed := r.store.clock.Elapsed()
		if elapsed < t.startElapsed || snapshot.Issued.Before(t.startWall.Add(-root.Policy.ClockSkew)) || snapshot.Issued.After(now.Add(root.Policy.ClockSkew)) {
			return problem("workspace_clock_unavailable", "directory time/request clock evidence differs")
		}
		if e = identityCurrent(root.Self, now); e != nil {
			return e
		}
		if e = identityCurrent(root.Directory, now); e != nil {
			return e
		}
		source, ok := cardOf(snapshot, root.Self.Host)
		if !ok || source.State != "active" || source.Identity != root.Self || source.MembershipGeneration != t.request.Source.MembershipGeneration {
			return problem("forbidden", "directory has no exact active source membership")
		}
		dir, ok := cardOf(snapshot, root.Directory.Host)
		if !ok || dir.State != "active" || dir.Identity != root.Directory {
			return problem("directory_changed", "directory identity/admission changed")
		}
		deadline = min(t.startElapsed+snapshot.TTL, elapsed+snapshot.ValidUntil.Sub(now), elapsed+root.Self.Expires.Sub(now), elapsed+root.Directory.Expires.Sub(now))
		if deadline <= elapsed {
			return problem("stale_refresh", "directory response consumed its request-start validity window")
		}
		for _, o := range v.Observations {
			if o.Workspace == root.ID && snapshot.Revision == o.Revision && membershipHash(snapshot) != membershipHash(o.Snapshot) {
				return problem("invalid_snapshot", "same directory revision changed immutable membership content")
			}
			if o.Workspace == root.ID && snapshot.Revision < o.Revision {
				return problem("stale_revision", "directory observation moved backwards")
			}
		}
		before := root
		changed := false
		trustedChanges := []TrustedMember{}
		for _, m := range snapshot.Members {
			var old *TrustedMember
			for _, x := range v.Trusted {
				if x.Workspace == root.ID && x.Identity.Host == m.Identity.Host {
					copy := x
					old = &copy
					break
				}
			}
			next := TrustedMember{Schema: Schema, Workspace: root.ID, Generation: root.Generation, Identity: m.Identity, MembershipGeneration: m.MembershipGeneration, State: "active", FirstSeen: snapshot.Revision, LatestSeen: snapshot.Revision}
			if m.State == "pending" && old == nil {
				continue
			}
			if m.State == "revoked" {
				next.State = "revoked"
			}
			if old != nil {
				next = *old
				next.LatestSeen = snapshot.Revision
				if old.State != "active" {
					next.State = old.State
				} else if m.State == "revoked" {
					next.State = "revoked"
				} else if old.Identity != m.Identity || old.MembershipGeneration != m.MembershipGeneration || m.State != "active" {
					next.State = "identity_change_pending"
				}
			}
			if old == nil || next.State != old.State || next.Identity != old.Identity || next.MembershipGeneration != old.MembershipGeneration {
				changed = true
				trustedChanges = append(trustedChanges, next)
			}
			if e = writeTrusted(ctx, tx, next); e != nil {
				return e
			}
		}
		for _, old := range v.Trusted {
			if old.Workspace == root.ID {
				if _, ok := cardOf(snapshot, old.Identity.Host); !ok {
					return problem("invalid_snapshot", "complete directory snapshot omitted retained identity/tombstone")
				}
			}
		}
		if changed {
			root.Revision, e = next(root.Revision)
			if e != nil {
				return e
			}
			root.Updated = now
			// Pin changes are recorded once per changed view, not every refresh.
			_, _, changes := counts(v, root.ID)
			pending := 0
			for _, o := range v.Operations {
				if o.Workspace == root.ID {
					pending += reserved(o)
				}
			}
			if changes+1+pending+1 > MaxChanges {
				return problem("history_full", "pin-change audit capacity exhausted")
			}
			if e = writeInstance(ctx, tx, root); e != nil {
				return e
			}
			if e = writeChange(ctx, tx, Change{Schema: Schema, Workspace: root.ID, Kind: "observation", Actor: "directory:" + root.Directory.Host, Key: "view-" + snapshot.Hash, Revision: root.Revision, At: now, Before: &before, After: root, Trusted: trustedChanges, OperationPhase: "completed"}); e != nil {
				return e
			}
		}
		observation := Observation{Schema: Schema, Workspace: root.ID, Generation: root.Generation, DirectoryPin: root.Directory.Fingerprint, Revision: snapshot.Revision, SnapshotHash: snapshot.Hash, Received: now, Snapshot: snapshot}
		if e = put(ctx, tx, `INSERT INTO workspace_observations(workspace,data) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET data=excluded.data`, observation, root.ID); e != nil {
			return e
		}
		clock.WallFloor = now
		clock.LastRevision = root.Revision
		return writeClock(ctx, tx, clock)
	})
	if e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return problem("backend_stopping", "runtime closed during refresh; persisted observation remains display-only")
	}
	r.active[t.workspace] = authorizedSnapshot{r.store.boot, deadline, snapshot.Hash, snapshot.RequesterGeneration}
	return nil
}

// Access is a bounded observation for the future destination-owned dispatcher,
// NOT a transferable grant/token. Current destination authorization is required
// again inside every eventual message transaction; capabilities never confer it.
type Access struct {
	Workspace, Source, Target          string
	DirectoryRevision                  int64
	SourceGeneration, TargetGeneration int64
	SourcePin, TargetPin               string
	Remaining, StreamTTL               time.Duration
}

func (r *Runtime) Check(ctx context.Context, id, target string) (Access, error) {
	r.mu.Lock()
	cached, ok := r.active[id]
	closed := r.closed
	r.mu.Unlock()
	if closed || !ok || cached.boot != r.store.boot {
		return Access{}, problem("membership_unknown", "fresh process-bound directory membership required")
	}
	v, e := r.store.Read(ctx)
	if e != nil {
		return Access{}, e
	}
	root, e := rootOf(&v, id)
	if e != nil {
		return Access{}, e
	}
	if root.Role != "member" || root.State != "active" || root.Self != r.store.self || root.SelfGeneration != cached.generation || denied(&v, id, target) || denied(&v, id, root.Directory.Host) || denied(&v, id, root.Self.Host) {
		return Access{}, problem("forbidden", "local workspace state/generation/deny does not permit messaging")
	}
	clock, e := clockOf(&v, id)
	if e != nil {
		return Access{}, e
	}
	r.store.mu.Lock()
	e = r.store.checkClock(clock.WallFloor)
	now, elapsed := r.store.now(), r.store.clock.Elapsed()
	r.store.mu.Unlock()
	if e != nil {
		return Access{}, e
	}
	if elapsed >= cached.deadline {
		return Access{}, problem("membership_stale", "request-start membership deadline passed")
	}
	var snap Snapshot
	for _, o := range v.Observations {
		if o.Workspace == id && o.SnapshotHash == cached.hash {
			snap = o.Snapshot
			break
		}
	}
	if snap.Hash == "" || !now.Before(snap.ValidUntil) {
		return Access{}, problem("membership_stale", "directory view changed or expired")
	}
	var trusted TrustedMember
	for _, t := range v.Trusted {
		if t.Workspace == id && t.Identity.Host == target {
			trusted = t
			break
		}
	}
	m, ok := cardOf(snap, target)
	if !ok || m.State != "active" || trusted.State != "active" || trusted.Identity != m.Identity || trusted.MembershipGeneration != m.MembershipGeneration {
		return Access{}, problem("identity_changed", "target membership/pin requires current explicit review")
	}
	for _, i := range []Identity{root.Self, root.Directory, m.Identity} {
		if e = identityCurrent(i, now); e != nil {
			return Access{}, e
		}
	}
	remaining := min(cached.deadline-elapsed, snap.ValidUntil.Sub(now), root.Self.Expires.Sub(now), root.Directory.Expires.Sub(now), m.Identity.Expires.Sub(now))
	return Access{id, root.Self.Host, target, snap.Revision, root.SelfGeneration, m.MembershipGeneration, root.Self.Fingerprint, m.Identity.Fingerprint, remaining, min(root.Policy.StreamTTL, remaining)}, nil
}

func membershipHash(s Snapshot) string {
	s.Requester = ""
	s.RequesterGeneration = 0
	s.Nonce = ""
	s.Issued = time.Time{}
	s.ValidUntil = time.Time{}
	s.TTL = 0
	s.Hash = ""
	return hash(s)
}

// Abandon only discards an unused refresh nonce after a transport read failure.
// It neither retries a call nor changes a still-unexpired prior observation.
func (r *Runtime) Abandon(nonce string) { r.mu.Lock(); delete(r.pending, nonce); r.mu.Unlock() }
