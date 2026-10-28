package syncfs

import (
	"context"
	"math"

	"getarm.org/internal/syncscope"
)

// NewBoundOriginalScopeOwner joins a concrete live Manager original to the
// sealed native verifier. The shared Original keeps the sole SQLite connection
// and native mutation gate throughout VerifyCompleteScope. No caller-provided
// boolean or positive callback is accepted here.
func NewBoundOriginalScopeOwner(ctx context.Context, claim ScopeClaim, original *syncscope.Original, project, selected, parent *Root) (*ExclusiveScopeOwner, error) {
	if original == nil {
		return nil, ErrQuotaUnknown
	}
	r := original.Record()
	f := claim.fields
	if r.Key != f.Key || r.Side != f.Side || r.LeaseID != f.PreparationLease ||
		r.Provider != f.Provider || r.ScopeID != f.ScopeID || r.MountID != f.MountID ||
		r.ProjectRootID != f.ProjectRootID || r.SelectedRootID != f.SelectedRootID || r.HomeRootID != f.ParentRootID ||
		r.OwnershipDigest != f.OwnershipDigest || r.ProjectID <= 0 || r.ProjectID > math.MaxUint32 ||
		uint32(r.ProjectID) != f.ProjectID || r.OwnerEpoch <= 0 || uint64(r.OwnerEpoch) != f.OwnerEpoch ||
		r.ChargedHard != f.ChargedHardBytes || r.State != "preparing" {
		return nil, ErrQuotaUnknown
	}
	return newExclusiveScopeOwner(ctx, claim, project, selected, parent, &boundOriginalFence{original: original, claim: claim, project: project, selected: selected, parent: parent})
}

type boundOriginalFence struct {
	original                  *syncscope.Original
	claim                     ScopeClaim
	project, selected, parent *Root
}

func (*boundOriginalFence) scopeOwnerFence() {}

func (f *boundOriginalFence) TryHoldOriginal(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) (scopeOwnerHold, error) {
	if f == nil || claim != f.claim || project != f.project || selected != f.selected || parent != f.parent {
		return nil, ErrQuotaUnknown
	}
	h, err := f.original.TryHold(ctx)
	if err != nil {
		return nil, ErrQuotaUnknown
	}
	return &boundOriginalHold{fence: f, hold: h}, nil
}

type boundOriginalHold struct {
	fence *boundOriginalFence
	hold  *syncscope.Hold
}

func (h *boundOriginalHold) verificationToken() *syncscope.Verifier {
	if h == nil || h.hold == nil {
		return nil
	}
	return h.hold.Verifier()
}

func (h *boundOriginalHold) CheckOriginal(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) error {
	if h == nil || h.fence == nil || claim != h.fence.claim ||
		project != h.fence.project || selected != h.fence.selected || parent != h.fence.parent {
		return ErrQuotaUnknown
	}
	if err := h.hold.Check(ctx); err != nil {
		return ErrQuotaUnknown
	}
	return nil
}

func (h *boundOriginalHold) Release() {
	if h != nil && h.hold != nil {
		h.hold.Release()
	}
}
