package server

import (
	"context"
	"os"
	"os/exec"

	"getarm.org/internal/mesh"
	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/server/internal/residentbirth"
	"getarm.org/internal/server/internal/residentservice"
)

type residentParentRetirementCall struct {
	owner                                            *residentParentPending
	op                                               *residentParentOperation
	protocol                                         *residentParentProtocolCall
	command                                          *exec.Cmd
	process                                          *os.Process
	pin                                              *mesh.PinParentReferenceOwner
	fence                                            *residentbirth.FenceParentReferenceOwner
	service                                          *residentservice.ServiceParentReferenceOwner
	pinClose, fenceClose, serviceClose, controlClose residentProtocolResult
	release                                          residentProtocolResult
	bodyReturned                                     bool
	bodyError                                        error
}

// Completion is derived solely from the same original launch's retained calls.
// This does not rerun Rack-only selectors after the challenge, nor use a writer
// result, caller boolean, PID, or replacement object as handoff authority.
func (r *residentParentPending) completedParentHandoffLocked(c *residentParentProtocolCall) bool {
	if !r.parentProtocolBindingsLocked(c) || !c.normalReturned || !c.bodyReturned || c.bodyError != nil || c.finalError != nil || !c.op.normalFinished || c.op.finalError != nil || c.op.abnormal {
		return false
	}
	for i := 0; i < 4; i++ {
		if !r.parentProtocolPhaseLocked(c, i) {
			return false
		}
	}
	if !c.ackAuth.completeData() || !c.ackData.completeData() || !c.serviceCurrent.completeData() || !c.fenceCurrent.completeData() || !c.serviceSelector.completeData() || !c.fenceSelector.completeData() || !c.structural.completeData() || !c.challengeSign.completeData() || !c.proofAuth.completeData() || !c.entropy.attempted || !c.entropy.returned || c.entropy.err != nil || c.entropy.n != len(c.entropy.buffer) || c.serviceData != c.serviceRecheck || c.fenceData != c.fenceRecheck || c.fenceData.Service != c.serviceData || c.serviceData.Context != c.context || c.proof.Context != c.context || residentprotocol.MatchNonceData(c.proof.Nonce, c.entropy.buffer) != nil {
		return false
	}
	return true
}

func (r *residentParentPending) beginParentRetirement(ctx context.Context) (*residentParentRetirementCall, error) {
	if r == nil || r.self != r || r.mu == nil {
		return nil, errResidentParentOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.retirement != nil || r.firstError != nil || !r.completedParentHandoffLocked(r.protocol) || r.pinReferences == nil || r.fenceReferences == nil || r.serviceReferences == nil {
		return nil, errResidentParentOriginal
	}
	op := &residentParentOperation{ctx: ctx, kind: "retire-parent-references", phase: "registered_before_ANY_parent_reference_disposition"}
	c := &residentParentRetirementCall{owner: r, op: op, protocol: r.protocol, command: r.command, process: r.process, pin: r.pinReferences, fence: r.fenceReferences, service: r.serviceReferences}
	r.retirement, r.active = c, op // irreversible original attempt, before foreign calls
	return c, nil
}

func (r *residentParentPending) parentRetirementGate(c *residentParentRetirementCall, stage int) error {
	if r == nil || r.self != r || r.mu == nil {
		return errResidentParentOriginal
	}
	r.mu.Lock()
	valid := c != nil && r.retirement == c && c.owner == r && r.active == c.op && c.op != nil && c.op.kind == "retire-parent-references" && !c.op.normalFinished && !c.op.abnormal && c.protocol == r.protocol && r.completedParentHandoffLocked(c.protocol) && c.command == r.command && c.process == r.process && c.pin == r.pinReferences && c.fence == r.fenceReferences && c.service == r.serviceReferences && !c.release.attempted && !c.bodyReturned && stage >= 0 && stage <= 4
	if valid {
		previous := [4]residentProtocolResult{c.pinClose, c.fenceClose, c.serviceClose, c.controlClose}
		for i := 0; i < stage; i++ {
			valid = valid && previous[i].completeData()
		}
		for i := stage; i < 4; i++ {
			valid = valid && !previous[i].attempted && !previous[i].returned
		}
	}
	r.mu.Unlock()
	if !valid || c.op.ctx == nil {
		return errResidentParentOriginal
	}
	return c.op.ctx.Err()
}

// This retires launcher references only. The resident and its original job
// custody remain alive and fully funded. Nothing here advertises public Ready.
func (r *residentParentPending) retireParentReferencesOriginal(ctx context.Context) (resultErr error) {
	c, err := r.beginParentRetirement(ctx)
	if err != nil {
		return err
	}
	normal := false
	defer r.finishOriginal(c.op, &normal, &resultErr)
	resultErr = r.retireParentReferencesBody(c)
	c.bodyError, c.bodyReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *residentParentPending) retireParentReferencesBody(c *residentParentRetirementCall) error {
	if err := r.parentRetirementGate(c, 0); err != nil {
		return err
	}
	c.pinClose.attempted = true
	c.pinClose.err = c.pin.RetireOriginal(c.op.ctx, c.command)
	c.pinClose.returned = true
	if c.pinClose.err != nil {
		return c.pinClose.err
	}
	if err := r.parentRetirementGate(c, 1); err != nil {
		return err
	}
	c.fenceClose.attempted = true
	c.fenceClose.err = c.fence.RetireOriginal(c.op.ctx, c.command)
	c.fenceClose.returned = true
	if c.fenceClose.err != nil {
		return c.fenceClose.err
	}
	if err := r.parentRetirementGate(c, 2); err != nil {
		return err
	}
	c.serviceClose.attempted = true
	c.serviceClose.err = c.service.RetireServiceOriginal(c.op.ctx, c.command)
	c.serviceClose.returned = true
	if c.serviceClose.err != nil {
		return c.serviceClose.err
	}
	if err := r.parentRetirementGate(c, 3); err != nil {
		return err
	}
	c.controlClose.attempted = true
	c.controlClose.err = c.service.RetireControlOriginal(c.op.ctx, c.command)
	c.controlClose.returned = true
	if c.controlClose.err != nil {
		return c.controlClose.err
	}
	if err := r.parentRetirementGate(c, 4); err != nil {
		return err
	}
	c.release.attempted = true
	c.release.err = c.process.Release() // original returned handle, once; never Wait
	c.release.returned = true
	if c.release.err != nil {
		return c.release.err
	}
	return c.op.ctx.Err()
}
