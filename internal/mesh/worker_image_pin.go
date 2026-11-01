package mesh

import "context"

// The worker wrapper shares only concrete native copy/verification primitives.
// Its inner legacy-shaped native bookkeeping never escapes. No old binding,
// runtime borrower, descriptor, launch or closer is returned by this surface.
type WorkerResidentImagePinPending struct {
	self            *WorkerResidentImagePinPending
	inner           *ResidentImagePinPending
	binding         *WorkerImageBindingPending
	references      *WorkerPinParentReferenceOwner
	retirement      *workerPinParentRetirement
	inventoryTried  bool
	validationCount uint8
}

// Pure construction. The actual funded service group must retain both original
// destinations before PrepareOriginal; native home identity is checked by the
// existing concrete pin before any namespace write. DATA alone grants nothing.
func NewWorkerResidentImagePinForHomeWithParentReferences(destination **WorkerResidentImagePinPending, references **WorkerPinParentReferenceOwner, home, debit, sha string, size int64, expected ResidentImagePinHomeData) error {
	if destination == nil || *destination != nil || references == nil || *references != nil || expected.Inode == 0 {
		return errResidentPinOriginal
	}
	p := &WorkerResidentImagePinPending{}
	p.self = p
	*destination = p
	inner, err := newResidentImagePinPending(home, debit, sha, size, workerResidentImagePinCore)
	p.inner = inner
	if err != nil {
		return err
	}
	data := expected
	inner.expectedHome = &data
	r := &WorkerPinParentReferenceOwner{pin: p}
	r.self = r
	p.references = r
	*references = r
	return nil
}

// Immutable private linkage only. Each concrete native primitive separately
// registers/serializes its own original effect and preserves pending errors.
func (p *WorkerResidentImagePinPending) originalInnerData() (*ResidentImagePinPending, error) {
	if p == nil || p.self != p || p.inner == nil || p.inner.residentImagePinState == nil || p.inner.self != p.inner || p.inner.coreScope != workerResidentImagePinCore || p.references == nil || p.references.self != p.references || p.references.pin != p {
		return nil, errResidentPinOriginal
	}
	return p.inner, nil
}

func (p *WorkerResidentImagePinPending) PrepareOriginal(ctx context.Context) error {
	inner, err := p.originalInnerData()
	if err != nil {
		return err
	}
	inner.mu.Lock()
	valid := p.retirement == nil && inner.p1Binding == nil && inner.parentReferences == nil && inner.parentRetirement == nil
	inner.mu.Unlock()
	if !valid {
		return errResidentPinOriginal
	}
	return inner.PrepareOriginal(ctx)
}

func (p *WorkerResidentImagePinPending) InventoryOriginal(ctx context.Context) (inventory ResidentImageInventory, resultErr error) {
	inner, err := p.originalInnerData()
	if err != nil {
		return inventory, err
	}
	inner.mu.Lock()
	if p.retirement != nil || !inner.ready || inner.active != nil || inner.firstError != nil || p.inventoryTried || inner.p1Binding != nil || inner.parentReferences != nil {
		inner.mu.Unlock()
		return inventory, errResidentPinOriginal
	}
	p.inventoryTried = true
	op := &residentPinOperation{phase: "worker_inventory_registered_before_native_effect"}
	inner.active = op
	inner.mu.Unlock()
	normal := false
	defer inner.finish(op, &normal, &resultErr)
	resultErr = inner.inventoryBody(ctx, &inventory)
	normal = true
	return inventory, resultErr
}

func (p *WorkerResidentImagePinPending) InspectOriginal() ResidentImagePinInspection {
	inner, err := p.originalInnerData()
	if err != nil {
		return ResidentImagePinInspection{FirstError: err, NativeOutcome: "unknown"}
	}
	return inner.InspectOriginal()
}
