package syncfs

import (
	"context"
	"errors"
	"fmt"
)

var ErrQuotaUnsupported = errors.New("native sync quota unavailable")
var ErrQuotaUnknown = errors.New("native sync quota status unknown")

// NativeQuotaEvidence reports a hard limit observed from the operating system
// for the original held roots. Only ProbeNativeQuota can construct a nonzero
// value. It does not establish exclusive operation ownership, a durable quota
// reservation, cleanup authority, or permission to open public sync dispatch.
type NativeQuotaEvidence struct {
	provider, scope, mount, selectedID, parentID string
	hardBytes, usedBytes                         uint64
}

func (v NativeQuotaEvidence) Provider() string       { return v.provider }
func (v NativeQuotaEvidence) Scope() string          { return v.scope }
func (v NativeQuotaEvidence) Mount() string          { return v.mount }
func (v NativeQuotaEvidence) SelectedRootID() string { return v.selectedID }
func (v NativeQuotaEvidence) ParentRootID() string   { return v.parentID }
func (v NativeQuotaEvidence) HardBytes() uint64      { return v.hardBytes }
func (v NativeQuotaEvidence) UsedBytes() uint64      { return v.usedBytes }

// ProbeNativeQuota reads an existing native hard quota without configuring or
// changing the host. The zero evidence value always accompanies a refusal.
// Callers must re-probe at every later authority boundary and compare all
// identities; this point-in-time report is not a lock on quota configuration.
func ProbeNativeQuota(ctx context.Context, selected, parent *Root) (NativeQuotaEvidence, error) {
	return probeNativeQuota(ctx, selected, parent, probePlatformQuota)
}

func probeNativeQuota(ctx context.Context, selected, parent *Root, native func(context.Context, *Root, *Root, string) (NativeQuotaEvidence, error)) (NativeQuotaEvidence, error) {
	if ctx == nil || selected == nil || parent == nil {
		return NativeQuotaEvidence{}, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return NativeQuotaEvidence{}, err
	}
	for _, root := range []*Root{selected, parent} {
		if err := root.Check(); err != nil {
			return NativeQuotaEvidence{}, err
		}
	}
	mount, err := surveyMountFD(selected.FD())
	if err != nil {
		return NativeQuotaEvidence{}, fmt.Errorf("selected mount identity: %w", ErrQuotaUnknown)
	}
	parentMount, err := surveyMountFD(parent.FD())
	if err != nil {
		return NativeQuotaEvidence{}, fmt.Errorf("workspace mount identity: %w", ErrQuotaUnknown)
	}
	if mount != parentMount {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	selectedID, err := selected.NativeID()
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	parentID, err := parent.NativeID()
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	v, err := native(ctx, selected, parent, mount)
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	if v.provider == "" || v.scope == "" || v.hardBytes == 0 || v.usedBytes > v.hardBytes {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	for _, root := range []*Root{selected, parent} {
		if err := root.Check(); err != nil {
			return NativeQuotaEvidence{}, err
		}
	}
	selectedAfter, err := selected.NativeID()
	if err != nil || selectedAfter != selectedID {
		return NativeQuotaEvidence{}, ErrChanged
	}
	parentAfter, err := parent.NativeID()
	if err != nil || parentAfter != parentID {
		return NativeQuotaEvidence{}, ErrChanged
	}
	selectedMountAfter, err := surveyMountFD(selected.FD())
	if err != nil || selectedMountAfter != mount {
		return NativeQuotaEvidence{}, ErrChanged
	}
	parentMountAfter, err := surveyMountFD(parent.FD())
	if err != nil || parentMountAfter != mount {
		return NativeQuotaEvidence{}, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return NativeQuotaEvidence{}, err
	}
	v.mount, v.selectedID, v.parentID = mount, selectedID, parentID
	return v, nil
}
