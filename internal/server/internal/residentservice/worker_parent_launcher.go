package residentservice

import "getarm.org/internal/residentrefs"

// The real standalone main supplies its original entry view. A running core,
// worker, serve process or caller-selected boolean cannot supply this binding.
func (p *WorkerParentPending) BindOriginalLauncher(scope *residentrefs.WorkerLauncherOriginal) error {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil || scope == nil || scope.RequireOriginalStandaloneStart() != nil {
		return ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.launcher != nil || p.openTried || p.debitTried || p.funding != nil || p.structural != nil || w.raw.active != nil || w.raw.firstError != nil || w.parent != p || !w.typedOriginalLocked() || !w.legacyEmptyLocked() {
		return ErrInvalid
	}
	p.launcher = scope
	return nil
}
