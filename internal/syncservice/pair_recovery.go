package syncservice

import (
	"context"
	"fmt"
	"reflect"

	"getarm.org/internal/syncreconcile"
)

// PairRecovery is a read-only comparison of the two original endpoint keys.
// NeedsAttention never authorizes a replay, cleanup, or allocation release.
// A caller must obtain each Ledger through its authenticated endpoint route;
// this comparison does not authenticate a supplied database connection.
type PairRecovery struct {
	Key              string
	PlanDigest       string
	Generation       uint64
	LocalState       string
	RemoteState      string
	LocalReceipt     *SideReceipt
	RemoteReceipt    *SideReceipt
	JointlyConfirmed bool
	NeedsAttention   bool
}

// InspectPair compares exact durable intent, manifest, receipts and confirmed
// baselines at both endpoints. Each endpoint is read independently, so a
// concurrent state change can cause a conservative error or attention result.
// It never starts a worker or writes a baseline.
func InspectPair(ctx context.Context, local, remote *Ledger) (PairRecovery, error) {
	var out PairRecovery
	if ctx == nil || ctx.Err() != nil || local == nil || remote == nil || local.Side != syncreconcile.Local || remote.Side != syncreconcile.Remote || local.Key != remote.Key {
		return out, fmt.Errorf("exact source and destination ledgers required")
	}
	if err := local.valid(); err != nil {
		return out, err
	}
	if err := remote.valid(); err != nil {
		return out, err
	}
	left, err := local.Lookup(ctx)
	if err != nil {
		return out, err
	}
	right, err := remote.Lookup(ctx)
	if err != nil {
		return out, err
	}
	rightIntent := right.Intent
	rightIntent.Side = syncreconcile.Local
	if !reflect.DeepEqual(left.Intent, rightIntent) || !reflect.DeepEqual(left.Manifest, right.Manifest) {
		return out, fmt.Errorf("%w: endpoint intent or selected manifest differs", ErrUnknown)
	}
	if left.LocalReceipt != nil && right.LocalReceipt != nil && !reflect.DeepEqual(*left.LocalReceipt, *right.LocalReceipt) {
		return out, fmt.Errorf("%w: source completion receipts differ", ErrUnknown)
	}
	if left.RemoteReceipt != nil && right.RemoteReceipt != nil && !reflect.DeepEqual(*left.RemoteReceipt, *right.RemoteReceipt) {
		return out, fmt.Errorf("%w: destination completion receipts differ", ErrUnknown)
	}
	out = PairRecovery{Key: local.Key, PlanDigest: left.Intent.PlanDigest, Generation: left.Intent.Generation,
		LocalState: left.State, RemoteState: right.State, LocalReceipt: left.LocalReceipt, RemoteReceipt: right.RemoteReceipt, NeedsAttention: true}
	// A one-sided confirmation is partial pair progress, but it must still
	// retain that endpoint's own complete confirmation evidence. Missing or
	// changed evidence cannot be hidden by the other endpoint's pending state.
	for _, endpoint := range []struct {
		ledger *Ledger
		lookup Lookup
	}{{local, left}, {remote, right}} {
		if endpoint.lookup.State != "confirmed" {
			continue
		}
		if endpoint.lookup.LocalReceipt == nil || endpoint.lookup.RemoteReceipt == nil {
			return out, fmt.Errorf("%w: confirmed endpoint lacks an exact receipt copy", ErrUnknown)
		}
		baseline, err := LoadBaseline(ctx, endpoint.ledger.DB, endpoint.lookup.Intent.Binding, endpoint.ledger.Side)
		if err != nil {
			return out, fmt.Errorf("%w: confirmed endpoint baseline unavailable: %w", ErrUnknown, err)
		}
		if baseline == nil || !reflect.DeepEqual(*baseline, endpoint.lookup.Intent.Proposal) {
			return out, fmt.Errorf("%w: endpoint confirmed baseline differs", ErrUnknown)
		}
	}
	if left.State != "confirmed" || right.State != "confirmed" {
		return out, nil
	}
	out.JointlyConfirmed = true
	out.NeedsAttention = false
	return out, nil
}
