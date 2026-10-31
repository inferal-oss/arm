package residentservice

// Pure parent allocation. The expected build/env come from THIS original
// native-unused grant on OpenExistingAuthorizedOriginal, not a copied/live
// authority DTO or an unfunded global executable hash probe. The funded image
// pending MUST then capture/verify actual current A before creating a pin or
// starting a process; mismatch keeps the committed original/full promise.
func NewP1ExistingGrantParentPending(home string) (*ParentPending, error) {
	if !validInputPath(home) {
		return nil, ErrInvalid
	}
	p := &ParentPending{}
	o := &owner{parent: p, path: home, buildFromOriginalGrant: true}
	p.owner = o
	o.files = &nativeFiles{owner: o}
	return p, nil
}
