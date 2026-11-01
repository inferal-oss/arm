package server

import (
	"getarm.org/internal/manager"
	"getarm.org/internal/server/internal/residentservice"
)

// The original root retains every continuation result before invoking its
// actual original Manager or native ingress. This is never public readiness.
type residentRuntimeCall struct {
	owner                                                   *residentChildPending
	op                                                      *residentChildOperation
	protocol                                                *residentRootProtocolCall
	manager                                                 *manager.ResidentManagerPending
	server                                                  *residentServerPending
	charge                                                  *residentservice.ManagerCharge
	budget, managerStart, ingressConstruction, ingressStart residentProtocolResult
	ingress                                                 *residentIngressPending
	bodyReturned                                            bool
	bodyError                                               error
}

func (r *residentChildPending) completedRuntimeProtocolLocked(c *residentRootProtocolCall) bool {
	if c == nil || c != r.protocol || c.owner != r || c.op == nil || c.op.kind != "root-protocol" || !c.op.normalFinished || c.op.abnormal || c.op.finalError != nil || !c.normalReturned || c.resultError != nil || !r.protocolTried || !r.originalProtocolPriorLocked() || !sameResidentProtocolContextData(c.ctx, r.rootContext) || c.bootstrap != r.bootstrapCall || c.manager != r.manager || c.server != r.server || c.managerBound != r.server.managerBound || c.charge != r.charge || c.refs != r.refs || c.control != r.control || c.serviceCustody != r.serviceCustody || c.fenceCustody != r.fenceCustody || c.service != r.service || c.fence != r.fence || c.native != r.native || c.image != r.image || c.runtime != r.runtime || c.managerView != r.managerView || c.serverView != r.serverView || c.prepared != r.preparedValidation || c.rootBound != r.rootBound {
		return false
	}
	for i := 0; i < 4; i++ {
		if !r.childProtocolPhaseLocked(c, i) {
			return false
		}
	}
	for _, result := range []residentProtocolResult{c.contextBuild, c.ackSign, c.challengeAuth, c.serviceCurrent, c.launchCurrent, c.managerCurrent, c.serverCurrent, c.nativeCurrent, c.layoutCurrent, c.imageBorrow, c.currentA, c.freshBuild, c.proofSign} {
		if !result.completeData() {
			return false
		}
	}
	for _, check := range c.checks {
		if !check.attempted || !check.complete {
			return false
		}
	}
	return true // never query the consumed CurrentChallengeView again
}

func (r *residentChildPending) beginOriginalRuntime() (*residentRuntimeCall, error) {
	if r == nil || r.self != r || r.mu == nil || r.entry == nil || !r.entry.ownsStartingRuntime(r) {
		return nil, errResidentChildOriginal
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil || r.firstError != nil || r.runtimeCall != nil || r.rootContext == nil || !r.completedRuntimeProtocolLocked(r.protocol) {
		return nil, errResidentChildOriginal
	}
	op := &residentChildOperation{kind: "runtime", phase: "registered_before_original_runtime_effects"}
	c := &residentRuntimeCall{owner: r, op: op, protocol: r.protocol, manager: r.manager, server: r.server, charge: r.charge}
	r.runtimeCall, r.active = c, op
	return c, nil
}

func (r *residentChildPending) runtimeGate(c *residentRuntimeCall) error {
	if r == nil || r.self != r || r.mu == nil || r.entry == nil || !r.entry.ownsStartingRuntime(r) {
		return errResidentChildOriginal
	}
	r.mu.Lock()
	valid := c != nil && c == r.runtimeCall && c.owner == r && c.op != nil && r.active == c.op && c.op.kind == "runtime" && !c.op.normalFinished && !c.op.abnormal && !c.bodyReturned && c.manager == r.manager && c.server == r.server && c.charge == r.charge && r.completedRuntimeProtocolLocked(c.protocol)
	r.mu.Unlock()
	if !valid {
		return errResidentChildOriginal
	}
	return r.rootContext.Err()
}

func (r *residentChildPending) startOriginalRuntime() (resultErr error) {
	c, err := r.beginOriginalRuntime()
	if err != nil {
		return err
	}
	normal := false
	defer r.finish(c.op, &normal, &resultErr)
	resultErr = r.startOriginalRuntimeBody(c)
	c.bodyError, c.bodyReturned = resultErr, true
	normal = true
	return resultErr
}

func (r *residentChildPending) startOriginalRuntimeBody(c *residentRuntimeCall) error {
	if err := r.runtimeGate(c); err != nil {
		return err
	}
	c.budget.attempted = true
	c.budget.err = c.charge.CheckOriginalRuntimeBounds(residentIngressRequiredBounds())
	c.budget.returned = true
	if c.budget.err != nil {
		return c.budget.err
	}
	if err := r.runtimeGate(c); err != nil {
		return err
	}
	c.managerStart.attempted = true
	c.managerStart.err = c.server.startOriginalManager()
	c.managerStart.returned = true
	if c.managerStart.err != nil {
		return c.managerStart.err
	}
	if err := r.runtimeGate(c); err != nil {
		return err
	}
	c.ingressConstruction.attempted = true
	c.ingress, c.ingressConstruction.err = c.server.newOriginalIngress(r)
	c.ingressConstruction.returned = true
	if c.ingressConstruction.err != nil {
		return c.ingressConstruction.err
	}
	if err := r.runtimeGate(c); err != nil {
		return err
	}
	c.ingressStart.attempted = true
	c.ingressStart.err = c.ingress.StartOriginal()
	c.ingressStart.returned = true
	return c.ingressStart.err
}
