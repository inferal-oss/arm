package residentrefs

// The existing process-wide claim remains the only authority to allocate a
// fixed13 census. Legacy is the existing zero value. No public constructor
// accepts a caller-selected scope. The worker producer is a later reviewed cut.
type childCoreScope uint8

const (
	legacyChildCore childCoreScope = iota
	workerChildCore
)

func (r *ServiceOriginal) RequireOriginalLegacyCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, legacyChildCore)
}
func (r *ServiceOriginal) RequireOriginalWorkerCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, workerChildCore)
}
func (r *FenceOriginal) RequireOriginalLegacyCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, legacyChildCore)
}
func (r *FenceOriginal) RequireOriginalWorkerCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, workerChildCore)
}
func (r *ImageOriginal) RequireOriginalLegacyCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, legacyChildCore)
}
func (r *ImageOriginal) RequireOriginalWorkerCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, workerChildCore)
}
func (r *ControlOriginal) RequireOriginalLegacyCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, legacyChildCore)
}
func (r *ControlOriginal) RequireOriginalWorkerCore() error {
	if r == nil || r.self != r {
		return ErrOriginal
	}
	return requireCoreScope(r.state, r, workerChildCore)
}
func requireCoreScope(r *recipientState, original any, scope childCoreScope) error {
	if r == nil || r.census == nil || r.census.self == nil || !r.census.self.ownsProcessClaim() {
		return ErrOriginal
	}
	s := r.census
	s.mu.Lock()
	defer s.mu.Unlock()
	return coreScopeMetadataLocked(r, original, scope)
}

// Pure immutable lineage checking only. An active census operation cannot
// change ownership or scope; actual native admission uses the original queue.
// This helper alone never establishes the process-wide original claim.
func coreScopeMetadataLocked(r *recipientState, original any, scope childCoreScope) error {
	if r == nil || r.census == nil || r.census.self == nil {
		return ErrOriginal
	}
	s := r.census
	if s.self.state != s || s.scope != scope || s.firstError != nil || r.firstError != nil || r.original != original {
		return ErrOriginal
	}
	var indices []int
	switch r.role {
	case "service":
		if s.service != original {
			return ErrOriginal
		}
		indices = []int{1, 2, 3}
	case "fence":
		if s.fence != original {
			return ErrOriginal
		}
		indices = []int{4, 5, 9, 10, 11, 12}
	case "image":
		if s.image != original {
			return ErrOriginal
		}
		indices = []int{6, 7, 8}
	case "control":
		if s.control != original {
			return ErrOriginal
		}
		indices = []int{0}
	default:
		return ErrOriginal
	}
	if r.count != len(indices) {
		return ErrOriginal
	}
	for at, index := range indices {
		entry := r.entries[at]
		if entry == nil || entry.self != entry || entry.census != s || s.entries[index] != entry || entry.number != index+3 || entry.reservedRecipient != r {
			return ErrOriginal
		}
	}
	return nil
}
func (r *ImageOriginal) RequireOriginalWorkerServiceLink(service *ServiceOriginal) error {
	if err := r.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	if err := service.RequireOriginalWorkerCore(); err != nil {
		return err
	}
	if r.state.census != service.state.census {
		return ErrOriginal
	}
	return nil
}
