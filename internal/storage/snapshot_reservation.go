package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/yrashk/arm/internal/model"
)

// SnapshotReservationObservation is bounded control evidence, not a plan,
// source-idle result, native handle, allocation or permission to copy.
type SnapshotReservationObservation struct {
	Provider model.FileIdentity    `json:"provider"`
	Source   model.FileIdentity    `json:"source"`
	Mount    string                `json:"mount"`
	Native   SnapshotNativeReceipt `json:"native"`
}

// SnapshotReservationPreflight reuses the original S descriptor owner for only
// one existing provider and source root. Every original close outcome is
// returned, including failure paths; nothing is read from the source tree.
func (p *Provider) SnapshotReservationPreflight(ctx context.Context, a model.Artifact) (SnapshotReservationObservation, error) {
	return p.snapshotReservationPreflight(ctx, a, newSnapshotFDSet())
}

func (p *Provider) snapshotReservationPreflight(ctx context.Context, a model.Artifact, owners *snapshotFDSet) (out SnapshotReservationObservation, resultErr error) {
	defer func() {
		resultErr = errors.Join(resultErr, owners.closeAll())
		out.Native = owners.receipt()
	}()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		return out, err
	}
	root, err := provider.snapshotOpenArtifact(ctx, a, owners, "reservation-source-root")
	if err != nil {
		return out, err
	}
	id, err := durableIdentity(int(root.Fd()), "")
	if err != nil {
		return out, err
	}
	out.Provider, out.Source, out.Mount = provider.identity, id, provider.mount
	return out, ctx.Err()
}

// Validate checks the exact fixed successful helper shape, not just a joined flag.
func (o SnapshotReservationObservation) Validate() error {
	if o.Mount == "" || o.Native.validateClosed() != nil || len(o.Native.Descriptors) != 2 || o.Native.HighWater != 2 {
		return fmt.Errorf("incomplete original reservation preflight receipt")
	}
	for i, role := range []string{"provider", "reservation-source-root"} {
		r := o.Native.Descriptors[i]
		want := o.Provider
		if i == 1 {
			want = o.Source
		}
		if r.Role != role || r.State != "closed" || r.Sync != "not-requested" || r.Error != "" || r.SyncError != "" || r.CloseError != "" || r.Identity == nil || *r.Identity != want {
			return fmt.Errorf("original reservation descriptor identity/role/closure differs")
		}
	}
	return nil
}
