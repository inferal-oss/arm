package workspace

import (
	"context"
	"time"

	"getarm.org/internal/local"
)

type statusTicket struct {
	request      AdmissionStatusRequest
	child        string
	revision     int64
	startWall    time.Time
	startElapsed time.Duration
	boot         string
}
type statusObservation struct {
	ticket   statusTicket
	status   AdmissionStatus
	deadline time.Duration
}

func (r *Runtime) readRecovery(ctx context.Context, id, key string) (Records, Instance, Operation, RecoveryRequest, error) {
	v, e := r.store.Read(ctx)
	if e != nil {
		return v, Instance{}, Operation{}, RecoveryRequest{}, e
	}
	root, e := rootOf(&v, id)
	if e != nil {
		return v, root, Operation{}, RecoveryRequest{}, e
	}
	child, ok := operationOf(&v, id, nativeKey(r.store.authority), key)
	if !ok || child.Kind != "resume_join" {
		return v, root, child, RecoveryRequest{}, problem("not_found", "candidate recovery child not found")
	}
	q, e := recoveryRequest(child)
	if e != nil {
		return v, root, child, q, e
	}
	r.store.mu.Lock()
	_, _, e = r.store.recoveryParent(&v, root, child.Kind, q, true)
	r.store.mu.Unlock()
	if e != nil {
		return v, root, child, q, e
	}
	if child.Phase != "approved" || child.Private.Boot != r.store.boot || !r.store.now().Before(child.ReviewExpires) {
		return v, root, child, q, problem("review_expired", "fresh approved current-boot candidate recovery required")
	}
	return v, root, child, q, nil
}

// BeginRecoveryStatus starts the monotonic window before any socket setup. It
// grants neither a stream nor membership; the transport rechecks current pins.
func (r *Runtime) BeginRecoveryStatus(ctx context.Context, id, key string) (AdmissionStatusRequest, error) {
	_, root, child, q, e := r.readRecovery(ctx, id, key)
	if e != nil {
		return AdmissionStatusRequest{}, e
	}
	request := AdmissionStatusRequest{Workspace: id, Generation: root.Generation, Source: root.Self.Host, Consent: q.Consent, Nonce: local.ID() + local.ID()}
	now, elapsed := r.store.now(), r.store.clock.Elapsed()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return AdmissionStatusRequest{}, problem("backend_stopping", "workspace runtime closed")
	}
	for nonce, t := range r.statusPending {
		if elapsed-t.startElapsed >= 30*time.Second {
			delete(r.statusPending, nonce)
		}
	}
	if len(r.statusPending)+len(r.pending) >= 16 {
		return AdmissionStatusRequest{}, problem("too_many_requests", "bounded workspace observations occupied")
	}
	for _, t := range r.statusPending {
		if t.request.Workspace == id {
			return AdmissionStatusRequest{}, problem("operation_busy", "own admission status already pending")
		}
	}
	r.statusPending[request.Nonce] = statusTicket{request, child.Key, child.Revision, now, elapsed, r.store.boot}
	delete(r.statusActive, id)
	return request, nil
}

// AcceptRecoveryStatus accepts only a caller-held real pinned TLS observation;
// it must never be routed as a JSON endpoint accepting asserted authentication.
func (r *Runtime) AcceptRecoveryStatus(ctx context.Context, directory Identity, status AdmissionStatus) error {
	r.mu.Lock()
	ticket, ok := r.statusPending[status.Nonce]
	delete(r.statusPending, status.Nonce)
	closed := r.closed
	r.mu.Unlock()
	if !ok || closed || ticket.boot != r.store.boot {
		return problem("stale_refresh", "no current own-admission request matches receipt")
	}
	_, root, child, q, e := r.readRecovery(ctx, ticket.request.Workspace, ticket.child)
	if e != nil {
		return e
	}
	if child.Revision != ticket.revision || directory != root.Directory || status.Schema != Schema || status.Workspace != root.ID || status.Generation != root.Generation || status.Source != root.Directory.Host || status.Target != root.Self.Host || status.Consent != q.Consent || status.DirectoryPin != root.Directory.Fingerprint || status.CandidatePin != root.Self.Fingerprint || status.Membership != 1 || status.Introducer != q.Introducer || status.HostInbox != q.HostInbox || !text(status.OriginalPrincipal, 512) || !validKey(status.OriginalKey) || !validHex(status.OriginalRequestHash, 32) || status.DirectoryRevision < 1 || !known(status.State, "pending", "active", "revoked") {
		return problem("invalid_snapshot", "own admission receipt differs from exact request/identity/scope")
	}
	if status.State == "active" {
		if !validKey(status.CompletingAttempt) || !validHex(status.ChallengeHash, 32) {
			return problem("invalid_snapshot", "completed admission proof evidence missing")
		}
	} else if status.CompletingAttempt != "" || status.ChallengeHash != "" {
		return problem("invalid_snapshot", "pending/revoked admission asserted completion")
	}
	now, elapsed := r.store.now(), r.store.clock.Elapsed()
	if elapsed < ticket.startElapsed || status.Observed.Before(ticket.startWall.Add(-root.Policy.ClockSkew)) || status.Observed.After(now.Add(root.Policy.ClockSkew)) {
		return problem("workspace_clock_unavailable", "own admission receipt clock differs")
	}
	deadline := min(ticket.startElapsed+30*time.Second, elapsed+child.ReviewExpires.Sub(now), elapsed+root.Directory.Expires.Sub(now), elapsed+root.Self.Expires.Sub(now))
	if deadline <= elapsed {
		return problem("stale_refresh", "own admission receipt consumed request-start window")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return problem("backend_stopping", "workspace runtime closed during own admission status")
	}
	r.statusActive[root.ID] = statusObservation{ticket, status, deadline}
	return nil
}

// RecoveryStatus is a read-only process-scoped result. It may explain pending
// state but is not transferable authority or permission to retry proof.
func (r *Runtime) RecoveryStatus(id, key string) (AdmissionStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.statusActive[id]
	if r.closed || !ok || status.ticket.child != key || status.ticket.boot != r.store.boot || r.store.clock.Elapsed() >= status.deadline {
		return AdmissionStatus{}, problem("membership_unknown", "fresh own admission status required")
	}
	return status.status, nil
}

type recoveryFinalize struct {
	child   Operation
	request RecoveryRequest
	status  statusObservation
}

func (r *Runtime) FinalizeRecovery(ctx context.Context, id, key string, revision int64) (Operation, error) {
	saved, found, e := r.store.Lookup(ctx, id, key)
	if e != nil {
		return Operation{}, e
	}
	if found && saved.Kind == "resume_join" && saved.Phase == "completed" {
		return saved, nil
	}
	_, _, child, q, e := r.readRecovery(ctx, id, key)
	if e != nil {
		return Operation{}, e
	}
	if child.Revision != revision {
		return Operation{}, problem("stale_revision", "candidate recovery child changed")
	}
	r.mu.Lock()
	status, ok := r.statusActive[id]
	closed := r.closed
	r.mu.Unlock()
	if closed || !ok || status.ticket.child != key || status.ticket.revision != revision || status.status.State != "active" || status.ticket.boot != r.store.boot || r.store.clock.Elapsed() >= status.deadline {
		return Operation{}, problem("membership_unknown", "fresh completed exact admission status required before finalization")
	}
	v, e := r.store.Read(ctx)
	if e != nil {
		return Operation{}, e
	}
	parent, ok := operationOf(&v, id, q.Original.Principal, q.Original.Key)
	if !ok {
		return Operation{}, problem("not_found", "original join missing")
	}
	return r.finalize(ctx, id, parent.Key, parent.Revision, &recoveryFinalize{child, q, status})
}

func (s *Store) checkFinalizeRecovery(v *Records, root Instance, parent Operation, g *recoveryFinalize) (Operation, error) {
	child, ok := operationOf(v, root.ID, nativeKey(s.authority), g.child.Key)
	if !ok || child.Kind != "resume_join" || child.Revision != g.child.Revision || child.Phase != "approved" || child.Private.Boot != s.boot || !s.now().Before(child.ReviewExpires) || s.clock.Elapsed() >= g.status.deadline {
		return child, problem("recovery_changed", "candidate recovery permission changed before finalization")
	}
	q, e := recoveryRequest(child)
	if e != nil {
		return child, e
	}
	if hash(q) != hash(g.request) || q.Original.Principal != parent.Principal || q.Original.Key != parent.Key || q.Original.RequestHash != parent.RequestHash || g.status.status.Consent != parent.ReviewHash {
		return child, problem("recovery_changed", "original candidate consent/request differs")
	}
	if _, _, e = s.recoveryParent(v, root, child.Kind, q, true); e != nil {
		return child, e
	}
	proved := false
	if parent.Private.ProofCount > 0 && parent.Private.LastChallenge == g.status.status.ChallengeHash {
		proved = true
	}
	children, e := familyChildren(v, root.ID, q.Original)
	if e != nil {
		return child, e
	}
	for _, attempt := range children {
		if attempt.Private.ProofCount > 0 && attempt.Private.LastChallenge == g.status.status.ChallengeHash {
			proved = true
		}
	}
	if !proved {
		return child, problem("forbidden", "directory completion has no matching durable candidate proof")
	}
	return child, nil
}
