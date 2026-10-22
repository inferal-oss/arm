package workspace

import (
	"context"
	"database/sql"
	"time"
)

// RefreshTransaction is a Go-only dependent source-report producer, not a wire
// RPC. Unlike an ordinary directory reachability check it owns the EXACT
// pending refresh ticket through final commit. Callbacks do local SQL only;
// current target/source identities are derived inside that same transaction.
func (r *Runtime) RefreshTransaction(ctx context.Context, q RefreshRequest, fn func(context.Context, *sql.Tx, RouteAccess, RefreshStart) (DirectoryDependent, error)) error {
	r.mu.Lock()
	ticket, ok := r.pending[q.Nonce]
	if r.closed || !ok || ticket.request != q || ticket.boot != r.store.boot || fn == nil {
		r.mu.Unlock()
		return problem("stale_refresh", "exact pending source refresh is unavailable")
	}
	life, cancel := context.WithCancel(r.life)
	r.finalizing.Add(1)
	r.mu.Unlock()
	defer r.finalizing.Done()
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	start := RefreshStart{Request: q, Boot: ticket.boot, Wall: ticket.startWall, Elapsed: ticket.startElapsed, Lifetime: ticket.lifetime}
	var dependent DirectoryDependent
	var deadline time.Duration
	var directory string
	var initial RouteAccess
	return r.store.transactionChecked(life, false, func(tx *sql.Tx, v *Records) error {
		root, e := rootOf(v, ticket.workspace)
		if e != nil {
			return e
		}
		directory = root.Directory.Host
		initial, e = r.store.routeAccess(v, authorizedSnapshot{}, ticket.workspace, root.Self.Host, directory, RouteDirectory)
		if e != nil {
			return e
		}
		if q.Source.Host != initial.Local.Host || q.Source.Fingerprint != initial.Local.Fingerprint || q.Source.MembershipGeneration != initial.OriginGeneration || q.Source.Generation != initial.Generation {
			return problem("forbidden", "pending refresh differs from current exact source binding")
		}
		if e = r.pendingRefresh(ticket); e != nil {
			return e
		}
		dependent, e = fn(life, tx, initial, start)
		if e != nil {
			return e
		}
		if !validHex(dependent.Hash, 32) || dependent.Issued != ticket.startWall || !dependent.ValidUntil.After(dependent.Issued) || dependent.ValidUntil.Sub(dependent.Issued) > ticket.lifetime || dependent.ValidUntil.After(initial.Expires) {
			return problem("invalid_snapshot", "source report lacks exact ORIGINAL pending refresh validity")
		}
		deadline = ticket.startElapsed + dependent.ValidUntil.Sub(dependent.Issued)
		return nil
	}, func(v *Records) error {
		if e := r.pendingRefresh(ticket); e != nil {
			return e
		}
		current, e := r.store.routeAccess(v, authorizedSnapshot{}, ticket.workspace, q.Source.Host, directory, RouteDirectory)
		if e != nil {
			return e
		}
		if current.Local != initial.Local || current.Directory != initial.Directory || current.Generation != initial.Generation || current.OriginGeneration != initial.OriginGeneration {
			return problem("forbidden", "source report binding changed before final commit")
		}
		now, elapsed := r.store.now(), r.store.clock.Elapsed()
		if elapsed < ticket.startElapsed || elapsed >= deadline || !now.Before(dependent.ValidUntil) {
			return problem("stale_refresh", "source report consumed its ORIGINAL pending request-start deadline")
		}
		return nil
	})
}

// Called under Store.mu during an admitted source operation. No Runtime method
// waits for Store.mu while holding Runtime.mu; Close cancels under that lock and
// joins outside it. Accept may consume the ticket concurrently, which refuses
// this commit instead of silently manufacturing a new report window.
func (r *Runtime) pendingRefresh(t refreshTicket) error {
	r.mu.Lock()
	current, ok := r.pending[t.nonce]
	closed := r.closed
	r.mu.Unlock()
	elapsed := r.store.clock.Elapsed()
	if closed || !ok || current != t || t.boot != r.store.boot || elapsed < t.startElapsed || elapsed >= t.startElapsed+t.lifetime {
		return problem("stale_refresh", "original source refresh was consumed, closed or expired")
	}
	return nil
}
