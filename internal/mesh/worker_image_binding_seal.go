package mesh

import (
	"context"
	"os/exec"
)

// ShortSealOriginal uses the actual original pin's bounded native census after
// both full validations. It creates no references, wrappers or pathname walk,
// does not hash/reopen, and does not lend a File/FD or assert atomic exec.
func (p *WorkerImageBindingPending) ShortSealOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.workerImageBindingState == nil || p.self != p || command != p.command {
		return errWorkerImageBindingOriginal
	}
	op, err := p.begin("short-seal")
	if err != nil {
		return err
	}
	normal := false
	defer p.finish(op, &normal, &resultErr)
	resultErr = p.shortSealBody(ctx)
	normal = true
	return resultErr
}
func (p *WorkerImageBindingPending) shortSealBody(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errWorkerImageBindingOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := p.command
	if c.Process != nil || c.ProcessState != nil || c.Stdout != nil || c.Stderr != nil || len(c.ExtraFiles) != 13 || len(p.extraFiles) != 13 || &c.ExtraFiles[0] != &p.extraFiles[0] {
		return errWorkerImageBindingOriginal
	}
	for i, f := range c.ExtraFiles {
		if f == nil || f != p.boundFiles[i] {
			return errWorkerImageBindingOriginal
		}
	}
	pin, err := p.pin.originalInnerData()
	if err != nil {
		return errWorkerImageBindingOriginal
	}
	pin.mu.Lock()
	if p.pin.binding != p || pin.coreScope != workerResidentImagePinCore || pin.p1Binding != nil || !pin.ready || pin.firstError != nil || pin.active != nil || p.pin.validationCount != 2 {
		pin.mu.Unlock()
		return errWorkerImageBindingOriginal
	}
	original := &residentPinOperation{phase: "original_worker_short_native_seal_registered"}
	pin.active = original
	pin.mu.Unlock()
	normal := false
	defer pin.finish(original, &normal, &resultErr)
	var data ResidentImageInventory
	resultErr = pin.inventoryBody(ctx, &data)
	normal = true
	return resultErr
}
