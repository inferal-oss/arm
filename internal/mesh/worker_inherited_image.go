package mesh

import (
	"context"
	"getarm.org/internal/residentrefs"
	"reflect"
)

// Distinct worker surface; the private inner shares only the existing concrete
// inherited-image native verifier, never a service grant/charge/role parser.
// No old RuntimeExecutable borrower is returned by this surface.
type WorkerInheritedImagePending struct {
	self      *WorkerInheritedImagePending
	inner     *InheritedImagePending
	custody   *residentrefs.ImageOriginal
	view      *WorkerRuntimeView
	viewTried bool
}
type WorkerRuntimeView struct {
	self  *WorkerRuntimeView
	owner *WorkerInheritedImagePending
	image *RuntimeExecutable // internal native verifier identity, never exposed
}
type WorkerRuntimeProofData struct {
	SHA256     string
	Size       int64
	Method     string
	Identities residentrefs.ImageIdentities
}

func NewWorkerInheritedImagePending(destination **WorkerInheritedImagePending, rootContext context.Context, custody *residentrefs.ImageOriginal) error {
	if destination == nil || *destination != nil || rootContext == nil || custody == nil {
		return errResidentInheritedImage
	}
	if err := custody.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	p := &WorkerInheritedImagePending{custody: custody}
	p.self = p
	*destination = p
	inner := &InheritedImagePending{}
	p.inner = inner
	state := &inheritedImageState{self: inner, custody: custody, rootContext: rootContext, runtime: NewResidentRuntimePending()}
	inner.state = state
	view, err := custody.ClaimImageOriginalView()
	if err != nil {
		return err
	}
	state.view = view
	return nil
}
func (p *WorkerInheritedImagePending) VerifyOriginal(ctx context.Context, expected ResidentImageInventory) error {
	if p == nil || p.self != p || p.inner == nil || p.inner.state == nil || p.inner.state.self != p.inner || p.custody == nil {
		return errResidentInheritedImage
	}
	if err := p.custody.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	if ctx == nil || !reflect.ValueOf(ctx).Comparable() || !reflect.ValueOf(p.inner.state.rootContext).Comparable() || ctx != p.inner.state.rootContext {
		return errResidentInheritedImage
	}
	return p.inner.VerifyOriginal(ctx, expected)
}
func (p *WorkerInheritedImagePending) RuntimeViewOriginal() (*WorkerRuntimeView, error) {
	if p == nil || p.self != p || p.inner == nil || p.inner.state == nil {
		return nil, errResidentInheritedImage
	}
	if err := p.custody.RequireOriginalWorkerCore(); err != nil {
		return nil, err
	}
	s := p.inner.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self != p.inner || !s.verified || s.active != nil || s.firstError != nil || s.image == nil || s.image.inherited != p.inner || p.viewTried {
		return nil, errResidentInheritedImage
	}
	p.viewTried = true
	view := &WorkerRuntimeView{owner: p, image: s.image}
	view.self = view
	p.view = view
	return view, nil
}
func (v *WorkerRuntimeView) ProofOriginal(ctx context.Context, service *residentrefs.ServiceOriginal, home, debitID string) (data WorkerRuntimeProofData, resultErr error) {
	if v == nil || v.self != v || v.owner == nil || v.owner.self != v.owner || v.owner.view != v || v.owner.inner == nil || v.owner.inner.state == nil || service == nil || ctx == nil {
		return data, errResidentInheritedImage
	}
	p := v.owner
	s := p.inner.state
	if err := p.custody.RequireOriginalWorkerServiceLink(service); err != nil {
		return data, err
	}
	if !reflect.ValueOf(ctx).Comparable() || !reflect.ValueOf(s.rootContext).Comparable() || ctx != s.rootContext {
		return data, errResidentInheritedImage
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	s.mu.Lock()
	valid := s.self == p.inner && s.verified && s.firstError == nil && s.image == v.image && s.expected.Home == home && s.expected.DebitID == debitID
	ids := s.identities
	s.mu.Unlock()
	if !valid {
		return data, errResidentInheritedImage
	}
	proof, err := p.inner.proofOriginal(v.image)
	if err != nil {
		return data, err
	}
	return WorkerRuntimeProofData{SHA256: proof.SHA256, Size: proof.Size, Method: proof.Method, Identities: ids}, nil
}
