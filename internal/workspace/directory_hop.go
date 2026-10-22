package workspace

import (
	"context"
	"database/sql"
	"time"
)

type directoryGraphWitness struct {
	generation      string
	revision, count int64
	hash            string
	deadline        time.Duration
}

// DirectoryNextHop inspects a reachability-only first hop for this node's own
// exact pinned-directory refresh. A retained trusted pin is not fresh member
// authority: it cannot authorize messages, lookups, relaying or another target.
// Every intermediate still needs its own fresh membership and direct pairing.
func (r *Runtime) DirectoryNextHop(ctx context.Context, id, next string) (Identity, error) {
	return r.directoryNextHop(ctx, id, next, 0, "")
}

func (r *Runtime) DirectoryNextHopPinned(ctx context.Context, id, next string, generation int64, pin string) (Identity, error) {
	if generation < 1 || !validHex(pin, 32) {
		return Identity{}, problem("invalid_request", "exact probe generation/pin required")
	}
	return r.directoryNextHop(ctx, id, next, generation, pin)
}
func (r *Runtime) directoryNextHop(ctx context.Context, id, next string, generation int64, pin string) (Identity, error) {
	v, e := r.store.Read(ctx)
	if e != nil {
		return Identity{}, e
	}
	root, e := rootOf(&v, id)
	if e != nil {
		return Identity{}, e
	}
	var out Identity
	_, e = r.RouteTransaction(ctx, id, r.store.self.Host, root.Directory.Host, RouteDirectory, func(tx *sql.Tx, a RouteAccess) error {
		current, e := readRecords(ctx, tx, r.store.authority)
		if e != nil {
			return stateProblem(e)
		}
		if denied(&current, id, next) {
			return problem("forbidden", "directory probe first hop is locally denied")
		}
		if next == a.Directory.Host {
			if generation != 0 && (generation != 1 || pin != a.Directory.Fingerprint) {
				return problem("identity_changed", "directory probe destination generation/pin changed")
			}
			out = a.Directory
			return nil
		}
		for _, t := range current.Trusted {
			if t.Workspace == id && t.Identity.Host == next && t.State == "active" && t.MembershipGeneration > 0 {
				if generation != 0 && (generation != t.MembershipGeneration || pin != t.Identity.Fingerprint) {
					return problem("identity_changed", "directory probe retained first-hop generation/pin changed")
				}
				if e = identityCurrent(t.Identity, a.At); e != nil {
					return e
				}
				out = t.Identity
				return nil
			}
		}
		return problem("identity_changed", "directory probe requires exact retained active first-hop pin")
	})
	if e != nil {
		return Identity{}, e
	}
	return out, nil
}
