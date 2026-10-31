package residentrefs

// Pure exact-original relationship only. No identity DATA, native observation,
// descriptor/closer, constructor or approval callback enters this method.
func (r *ServiceOriginal) ValidateOriginalFenceLink(fence *FenceOriginal) error {
	if r == nil || r.self != r || r.state == nil || r.state.census == nil || fence == nil || fence.self != fence || fence.state == nil {
		return ErrOriginal
	}
	s := r.state.census
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self == nil || s.self.state != s || !s.self.ownsProcessClaim() || !s.sealed || !s.delegated || !s.rootCloseRetired || s.firstError != nil || s.service != r || s.fence != fence || r.state.original != r || fence.state.original != fence || fence.state.census != s || r.state.role != "service" || fence.state.role != "fence" || !r.state.materialized || !fence.state.materialized || r.state.firstError != nil || fence.state.firstError != nil {
		return ErrOriginal
	}
	return nil
}
