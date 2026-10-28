package syncfs

import (
	"context"
	"encoding/hex"
	"math"

	"getarm.org/internal/syncscope"
)

// ScopeClaimFields are expected identities copied from a durable original
// preparation record. These caller values are requests, never proof.
type ScopeClaimFields struct {
	Key, Side, PreparationLease                 string
	Provider, ScopeID, MountID                  string
	ProjectRootID, SelectedRootID, ParentRootID string
	OwnershipDigest                             string
	ProjectID                                   uint32
	OwnerEpoch                                  uint64
	// ChargedHardBytes is the entire immutable native project hard ceiling
	// charged to the original Manager lease, not estimated free headroom.
	ChargedHardBytes int64
}

// ScopeClaim is a validated request to inspect one already-enforced native
// scope. It cannot authorize setup, execution, or native cleanup.
type ScopeClaim struct{ fields ScopeClaimFields }

func NewScopeClaim(fields ScopeClaimFields) (ScopeClaim, error) {
	if len(fields.Key) < 16 || len(fields.Key) > 128 ||
		(fields.Side != "local" && fields.Side != "remote") ||
		fields.PreparationLease == "" || len(fields.PreparationLease) > 256 ||
		fields.Provider != "xfs-project-quota" ||
		fields.ScopeID == "" || len(fields.ScopeID) > 512 ||
		fields.MountID == "" || len(fields.MountID) > 512 ||
		fields.ProjectRootID == "" || fields.SelectedRootID == "" || fields.ParentRootID == "" ||
		len(fields.ProjectRootID) > 512 || len(fields.SelectedRootID) > 512 || len(fields.ParentRootID) > 512 ||
		fields.ProjectID == 0 || fields.OwnerEpoch == 0 || fields.OwnerEpoch > math.MaxInt64 ||
		fields.ChargedHardBytes <= 0 ||
		len(fields.OwnershipDigest) != 64 {
		return ScopeClaim{}, ErrQuotaUnknown
	}
	for _, c := range fields.Key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return ScopeClaim{}, ErrQuotaUnknown
		}
	}
	decoded, err := hex.DecodeString(fields.OwnershipDigest)
	if err != nil || hex.EncodeToString(decoded) != fields.OwnershipDigest {
		return ScopeClaim{}, ErrQuotaUnknown
	}
	return ScopeClaim{fields: fields}, nil
}

// scopeOwnerFence is implemented only by a reviewed provider joined to the
// original charged owner's mutation gate and durable native ownership record.
// TryHoldOriginal must enter that gate without waiting: the returned hold keeps
// all Manager mutations of the selected tree and private home excluded until
// Release. A check before and after a directory walk cannot replace this hold.
// No production implementation is installed by this source-only patch.
type scopeOwnerFence interface {
	scopeOwnerFence()
	TryHoldOriginal(context.Context, ScopeClaim, *Root, *Root, *Root) (scopeOwnerHold, error)
}

// scopeOwnerHold is a live critical section, not a positive caller assertion.
// CheckOriginal must revalidate the original charge, durable scope owner,
// native owner record, and exact held roots while that section remains held.
type scopeOwnerHold interface {
	CheckOriginal(context.Context, ScopeClaim, *Root, *Root, *Root) error
	Release()
}

// ExclusiveScopeOwner cannot be populated by a wire field or ordinary caller.
// Only an in-package provider with a real original-owner fence may construct
// it. This type alone is not Manager execution or cleanup authority.
type ExclusiveScopeOwner struct {
	claim    ScopeClaim
	project  *Root
	selected *Root
	parent   *Root
	fence    scopeOwnerFence
}

// newExclusiveScopeOwner is the internal constructor seam for a reviewed
// provider. The fence must already be bound to the original charged lease and
// durable scope owner; this function does not create either. A provider must
// also verify native enforcement before any ownership-record allocation.
func newExclusiveScopeOwner(ctx context.Context, claim ScopeClaim, project, selected, parent *Root, fence scopeOwnerFence) (*ExclusiveScopeOwner, error) {
	validated, err := NewScopeClaim(claim.fields)
	if err != nil || validated != claim || project == nil || selected == nil || parent == nil || fence == nil {
		return nil, ErrQuotaUnknown
	}
	owner := &ExclusiveScopeOwner{claim: claim, project: project, selected: selected, parent: parent, fence: fence}
	hold, err := owner.holdFor(ctx, claim, project, selected, parent)
	if err != nil {
		return nil, err
	}
	hold.Release()
	return owner, nil
}

func (o *ExclusiveScopeOwner) holdFor(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) (scopeOwnerHold, error) {
	if ctx == nil || o == nil || o.fence == nil || o.claim != claim ||
		o.project != project || o.selected != selected || o.parent != parent {
		return nil, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hold, err := o.fence.TryHoldOriginal(ctx, claim, project, selected, parent)
	if err != nil {
		if hold != nil {
			hold.Release()
		}
		return nil, ErrQuotaUnknown
	}
	if hold == nil {
		return nil, ErrQuotaUnknown
	}
	if err := hold.CheckOriginal(ctx, claim, project, selected, parent); err != nil {
		hold.Release()
		return nil, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		hold.Release()
		return nil, err
	}
	return hold, nil
}

// ScopeBinding is a copied native observation, not Manager execution
// authority. All fields are private; callers can only inspect its values.
type ScopeBinding struct {
	provider, scopeID, mountID, projectRootID, selectedRootID, parentRootID string
	ownershipDigest                                                         string
	projectID                                                               uint32
	ownerEpoch                                                              uint64
	hardBytes, usedBytes                                                    uint64
}

func (b ScopeBinding) Provider() string        { return b.provider }
func (b ScopeBinding) ScopeID() string         { return b.scopeID }
func (b ScopeBinding) MountID() string         { return b.mountID }
func (b ScopeBinding) ProjectRootID() string   { return b.projectRootID }
func (b ScopeBinding) SelectedRootID() string  { return b.selectedRootID }
func (b ScopeBinding) ParentRootID() string    { return b.parentRootID }
func (b ScopeBinding) OwnershipDigest() string { return b.ownershipDigest }
func (b ScopeBinding) ProjectID() uint32       { return b.projectID }
func (b ScopeBinding) OwnerEpoch() uint64      { return b.ownerEpoch }
func (b ScopeBinding) HardBytes() uint64       { return b.hardBytes }
func (b ScopeBinding) UsedBytes() uint64       { return b.usedBytes }

func (b ScopeBinding) matches(claim ScopeClaim) bool {
	f := claim.fields
	return f.ChargedHardBytes > 0 && b.provider == f.Provider && b.scopeID == f.ScopeID &&
		b.mountID == f.MountID && b.projectRootID == f.ProjectRootID &&
		b.selectedRootID == f.SelectedRootID && b.parentRootID == f.ParentRootID &&
		b.ownershipDigest == f.OwnershipDigest && b.projectID == f.ProjectID &&
		b.ownerEpoch == f.OwnerEpoch && b.hardBytes > 0 && b.hardBytes <= math.MaxInt64 &&
		b.usedBytes <= b.hardBytes && b.hardBytes == uint64(f.ChargedHardBytes)
}

// CompleteScopeProof can only be built after an original owner exclusion and
// the complete platform verification. A copied or zero proof is refused when
// the owner fence is no longer live. BindingFor returns the cached observation;
// it does not freshly query quota or rescan the tree, and is not authority to
// execute work.
type CompleteScopeProof struct {
	claim    ScopeClaim
	binding  ScopeBinding
	owner    *ExclusiveScopeOwner
	project  *Root
	selected *Root
	parent   *Root
	verifier *syncscope.Verifier
}

// VerificationToken is only an ordering witness for a subsequent fresh
// writer. It supplies no execution or quota authority by itself.
func (p CompleteScopeProof) VerificationToken() *syncscope.Verifier { return p.verifier }

func (p CompleteScopeProof) BindingFor(ctx context.Context, claim ScopeClaim) (ScopeBinding, error) {
	if p.claim != claim || !p.binding.matches(claim) {
		return ScopeBinding{}, ErrQuotaUnknown
	}
	hold, err := p.owner.holdFor(ctx, claim, p.project, p.selected, p.parent)
	if err != nil {
		return ScopeBinding{}, err
	}
	defer hold.Release()
	if err := ctx.Err(); err != nil {
		return ScopeBinding{}, err
	}
	return p.binding, nil
}

// VerifyCompleteScope reads an existing native limit and complete held trees.
// A diagnostic ProbeNativeQuota result or caller-supplied claim cannot mint a
// positive proof: the original exclusive owner must already be live. There is
// no production owner fence in this patch, so all current callers fail closed.
func VerifyCompleteScope(ctx context.Context, claim ScopeClaim, owner *ExclusiveScopeOwner, project, selected, parent *Root) (CompleteScopeProof, error) {
	return verifyCompleteScopeWith(ctx, claim, owner, project, selected, parent, verifyPlatformCompleteScope)
}

// verifyCompleteScopeWith keeps the exclusion hold across the entire native
// verifier. Its injected form is package-private so tests can exercise failure
// and cancellation cleanup without claiming a native positive fixture.
func verifyCompleteScopeWith(ctx context.Context, claim ScopeClaim, owner *ExclusiveScopeOwner, project, selected, parent *Root, verify func(context.Context, ScopeClaim, *Root, *Root, *Root) (ScopeBinding, error)) (CompleteScopeProof, error) {
	if ctx == nil {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		return CompleteScopeProof{}, err
	}
	if project == nil || selected == nil || parent == nil || claim.fields.Provider == "" || verify == nil {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	hold, err := owner.holdFor(ctx, claim, project, selected, parent)
	if err != nil {
		return CompleteScopeProof{}, err
	}
	defer hold.Release()
	binding, err := verify(ctx, claim, project, selected, parent)
	if err != nil {
		return CompleteScopeProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return CompleteScopeProof{}, err
	}
	if !binding.matches(claim) {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	if err := hold.CheckOriginal(ctx, claim, project, selected, parent); err != nil {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		return CompleteScopeProof{}, err
	}
	var token *syncscope.Verifier
	if bound, ok := hold.(interface{ verificationToken() *syncscope.Verifier }); ok {
		token = bound.verificationToken()
	}
	return CompleteScopeProof{claim: claim, binding: binding, owner: owner, project: project, selected: selected, parent: parent, verifier: token}, nil
}
