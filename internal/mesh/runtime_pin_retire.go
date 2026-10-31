package mesh

import (
	"context"
	"encoding/hex"
)

// Only the actual private source of the actual actively preparing pin can retire
// this reference. It lends no File/Close method to any borrower and is not error
// cleanup. A failed/abnormal operation stays retained by the original owners.
func (o *ResidentRuntimePending) retirePinSourceOriginal(ctx context.Context, pin *ResidentImagePinPending) (resultErr error) {
	if o == nil || o.residentRuntimeState == nil || o.self != o || pin == nil || pin.residentImagePinState == nil || pin.self != pin || pin.source != o {
		return errResidentRuntimeOriginal
	}
	pin.mu.Lock()
	valid := pin.active != nil && !pin.active.abnormal && pin.tried && !pin.ready && pin.firstError == nil
	pin.mu.Unlock()
	// All checked objects are private originals of this same synchronous prepare.
	// No caller data/proof/callback can attest that the destination was retained.
	if !valid || pin.writable == nil || !pin.writable.closed || pin.readonly == nil || pin.readonly.closed || pin.readonly.closeTried || pin.copied != pin.expectedSize || pin.verified != pin.expectedSize || pin.copyHash == nil || pin.verifyHash == nil {
		return errResidentRuntimeOriginal
	}
	if hex.EncodeToString(pin.copyHash.Sum(nil)) != pin.expectedSHA || hex.EncodeToString(pin.verifyHash.Sum(nil)) != pin.expectedSHA {
		return errResidentRuntimeOriginal
	}
	if ctx == nil {
		return errResidentRuntimeOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	op, err := o.begin("pin-source-close")
	if err != nil {
		return err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.retirePinSource(ctx)
	normal = true
	return resultErr
}
func (o *ResidentRuntimePending) retirePinSource(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// This is the SAME actual captured File, not an FD reopened from a name or a
	// second wrapper. Its close operation and attempted state precede the effect.
	if o.file == nil || o.image == nil || o.image.file != o.file {
		return errResidentRuntimeOriginal
	}
	o.phase("original_runtime_source_reference_close_pending")
	o.mu.Lock()
	o.pinSourceCloseTried = true
	o.observed.SourceReferenceCloseAttempted = true
	o.mu.Unlock()
	o.pinSourceCloseError = o.file.Close()
	o.mu.Lock()
	o.pinSourceClosed = o.pinSourceCloseError == nil
	o.observed.SourceReferenceCloseKnown = o.pinSourceClosed
	o.mu.Unlock()
	return o.pinSourceCloseError
}
