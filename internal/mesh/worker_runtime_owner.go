package mesh

// Exact original registry identity only. This does not issue another view,
// expose RuntimeExecutable, validate current native state or grant resources.
// The root must separately perform ProofOriginal on this same retained view.
func (v *WorkerRuntimeView) RequireOriginalImageOwner(p *WorkerInheritedImagePending) error {
	if v == nil || v.self != v || p == nil || p.self != p || v.owner != p || p.view != v || !p.viewTried || p.custody == nil || p.inner == nil || p.inner.state == nil {
		return errResidentInheritedImage
	}
	if err := p.custody.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	s := p.inner.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self != p.inner || !s.verified || s.firstError != nil || s.active != nil || s.image == nil || s.image != v.image || s.image.inherited != p.inner {
		return errResidentInheritedImage
	}
	return nil
}
