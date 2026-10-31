package residentservice

import "getarm.org/internal/mesh"

// Pure construction through the ACTUAL retained original group/debit, with its
// own native-home selectors. No DTO creates funding; group retains the actual
// resulting pin. Pin's real held-home check must precede its first mkdir/write.
func (p *P1ServiceGroup) NewOriginalImagePinPending() (*mesh.ResidentImagePinPending, error) {
	pin, _, err := p.newOriginalImagePin(false)
	return pin, err
}

func (p *P1ServiceGroup) NewOriginalImagePinWithParentReferences() (*mesh.ResidentImagePinPending, *mesh.PinParentReferenceOwner, error) {
	return p.newOriginalImagePin(true)
}

func (p *P1ServiceGroup) newOriginalImagePin(withReferences bool) (pin *mesh.ResidentImagePinPending, references *mesh.PinParentReferenceOwner, resultErr error) {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return nil, nil, ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.parentRetirement != nil || o.active != nil || o.firstError != nil || !p.fundingChecked || p.pinTried || o.channelTried || o.writerWithdrawn || p.debit.validateOriginalLocked(o) != nil || o.files == nil || o.files.home == nil {
		return nil, nil, ErrInvalid
	}
	p.pinTried = true
	id := o.files.home.id
	expected := mesh.ResidentImagePinHomeData{Device: id.Device, Inode: id.Inode, UID: id.UID}
	if withReferences {
		pin, references, resultErr = mesh.NewResidentImagePinForHomeWithParentReferences(o.path, p.debit.record.ID, o.build.SHA256, o.build.Size, expected)
	} else {
		pin, resultErr = mesh.NewResidentImagePinForHomeDataPending(o.path, p.debit.record.ID, o.build.SHA256, o.build.Size, expected)
	}
	p.pin, p.pinError = pin, resultErr
	return pin, references, resultErr
}
func (p *P1ServiceGroup) CheckOriginalImagePin(pin *mesh.ResidentImagePinPending) error {
	if p == nil || p.p1ServiceState == nil || p.self != p || p.parent == nil || p.parent.owner == nil {
		return ErrInvalid
	}
	o := p.parent.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.p1 != p || o.active != nil || o.firstError != nil || !p.fundingChecked || !p.pinTried || p.pinError != nil || pin == nil || p.pin != pin || p.debit.validateOriginalLocked(o) != nil {
		return ErrInvalid
	}
	return nil
}
