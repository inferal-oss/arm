package syncfs

import (
	"context"
	"encoding/hex"
	"math"
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
	PromiseBytes                                int64
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
		fields.PromiseBytes <= 0 ||
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

// scopeOwnerFence is implemented only by a reviewed provider that has joined
// the original owner's exclusion and holds its native ownership record. The
// check must revalidate the original claim and held descriptors; it cannot be
// satisfied by a caller-supplied positive flag. No implementation or
// constructor is installed by this source-only patch.
type scopeOwnerFence interface {
	scopeOwnerFence()
	CheckOriginal(ScopeClaim, *Root, *Root, *Root) error
}

// ExclusiveScopeOwner cannot be populated by a wire field or ordinary caller.
// A future constructor must bind it to the original charged preparation and
// a native exclusion that remains live throughout complete-tree verification.
type ExclusiveScopeOwner struct {
	claim    ScopeClaim
	project  *Root
	selected *Root
	parent   *Root
	fence    scopeOwnerFence
}

func (o *ExclusiveScopeOwner) validFor(claim ScopeClaim, project, selected, parent *Root) bool {
	return o != nil && o.fence != nil && o.claim == claim &&
		o.project == project && o.selected == selected && o.parent == parent &&
		o.fence.CheckOriginal(claim, project, selected, parent) == nil
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
}

func (p CompleteScopeProof) BindingFor(claim ScopeClaim) (ScopeBinding, error) {
	if p.binding.provider == "" || p.claim != claim ||
		!p.owner.validFor(claim, p.project, p.selected, p.parent) {
		return ScopeBinding{}, ErrQuotaUnknown
	}
	return p.binding, nil
}

// VerifyCompleteScope reads an existing native limit and complete held trees.
// A diagnostic ProbeNativeQuota result or caller-supplied claim cannot mint a
// positive proof: the original exclusive owner must already be live. There is
// no owner constructor in this patch, so all current callers fail closed.
func VerifyCompleteScope(ctx context.Context, claim ScopeClaim, owner *ExclusiveScopeOwner, project, selected, parent *Root) (CompleteScopeProof, error) {
	if ctx == nil {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		return CompleteScopeProof{}, err
	}
	if project == nil || selected == nil || parent == nil || !owner.validFor(claim, project, selected, parent) ||
		claim.fields.Provider == "" {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	binding, err := verifyPlatformCompleteScope(ctx, claim, project, selected, parent)
	if err != nil {
		return CompleteScopeProof{}, err
	}
	if !owner.validFor(claim, project, selected, parent) {
		return CompleteScopeProof{}, ErrQuotaUnknown
	}
	if err := ctx.Err(); err != nil {
		return CompleteScopeProof{}, err
	}
	return CompleteScopeProof{claim: claim, binding: binding, owner: owner, project: project, selected: selected, parent: parent}, nil
}
