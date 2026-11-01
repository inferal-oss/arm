package residentservice

import "getarm.org/internal/mesh"

func (p *WorkerServiceGroup) NewOriginalWorkerImagePinWithParentReferences() (pin *mesh.WorkerResidentImagePinPending, references *mesh.WorkerPinParentReferenceOwner, resultErr error) {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || p.parent.custody.raw == nil {
		return nil, nil, ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || w.raw.active != nil || w.raw.firstError != nil || !w.originalWorkerDebitLocked(p.debit) || !p.fundingChecked || p.pinTried || p.channelTried || p.writerWithdrawn || p.retirement != nil {
		return nil, nil, ErrInvalid
	}
	p.pinTried = true
	id := w.raw.files.home.id
	resultErr = mesh.NewWorkerResidentImagePinForHomeWithParentReferences(&p.pin, &p.pinReferences, w.path, p.debit.record.ID, w.grant.Intent.Profile.Core.Build.SHA256, w.grant.Intent.Profile.Core.Build.Size, mesh.ResidentImagePinHomeData{Device: id.Device, Inode: id.Inode, UID: id.UID})
	p.pinError = resultErr
	return p.pin, p.pinReferences, resultErr
}

func (p *WorkerServiceGroup) CheckOriginalImagePin(pin *mesh.WorkerResidentImagePinPending) error {
	if p == nil || p.self != p || p.parent == nil || p.parent.custody == nil || p.parent.custody.raw == nil {
		return ErrInvalid
	}
	w := p.parent.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.parent.group != p || w.raw.active != nil || w.raw.firstError != nil || !p.fundingChecked || !p.pinTried || p.pinError != nil || pin == nil || pin != p.pin || !w.originalWorkerDebitLocked(p.debit) || p.retirement != nil {
		return ErrInvalid
	}
	return nil
}
