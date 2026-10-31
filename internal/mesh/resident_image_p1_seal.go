package mesh

import (
	"context"
	"os/exec"
)

// ShortSealOriginal uses the actual original pin's bounded native census after
// both full validations. It creates no references, wrappers or pathname walk,
// does not hash/reopen, and does not lend a File/FD or assert atomic exec.
func (p *ResidentImageP1Pending) ShortSealOriginal(ctx context.Context, command *exec.Cmd) (resultErr error) {
	if p == nil || p.residentImageP1State == nil || p.self != p || command != p.command {
		return errResidentImageP1Original
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
func (p *ResidentImageP1Pending) shortSealBody(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errResidentImageP1Original
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c := p.command
	if c.Process != nil || c.ProcessState != nil || c.Stdout != nil || c.Stderr != nil || len(c.ExtraFiles) != 13 || len(p.extraFiles) != 13 || &c.ExtraFiles[0] != &p.extraFiles[0] {
		return errResidentImageP1Original
	}
	for i, f := range c.ExtraFiles {
		if f == nil || f != p.boundFiles[i] {
			return errResidentImageP1Original
		}
	}
	pin := p.pin
	if pin == nil || pin.residentImagePinState == nil || pin.self != pin {
		return errResidentImageP1Original
	}
	pin.mu.Lock()
	if pin.p1Binding != p || !pin.ready || pin.firstError != nil || pin.active != nil || pin.p1ValidationCount != 2 {
		pin.mu.Unlock()
		return errResidentImageP1Original
	}
	original := &residentPinOperation{phase: "original_P1_short_native_seal_registered"}
	pin.active = original
	pin.mu.Unlock()
	normal := false
	defer pin.finish(original, &normal, &resultErr)
	var data ResidentImageInventory
	resultErr = pin.inventoryBody(ctx, &data)
	normal = true
	return resultErr
}
