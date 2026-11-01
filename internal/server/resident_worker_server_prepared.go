package server

import (
	"context"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/lifetimeview"
)

type residentWorkerPreparedServerData struct {
	Native          lifetimeview.WorkerSnapshot
	SettingsSHA256  string
	WorkspaceFormat string
}
type residentWorkerServerPreparedCall struct {
	owner       *residentWorkerServerPending
	op          *residentWorkerServerOperation
	view        *lifetimeview.WorkerServerView
	manager     *manager.ResidentWorkerManagerPending
	borrower    *manager.ResidentWorkerManagerServerBinding
	native      lifetimeview.WorkerSnapshot
	managerData manager.ResidentWorkerPreparedManagerData
	returned    bool
	resultError error
}

// Actual original, OPENED but UNSTARTED Server and its one real Manager borrower.
// This does not borrow ManagerForOriginalServer or validate ingress readiness.
func (o *residentWorkerServerPending) validatePreparedOriginal(ctx context.Context, view *lifetimeview.WorkerServerView, originalManager *manager.ResidentWorkerManagerPending) (data residentWorkerPreparedServerData, resultErr error) {
	if o == nil || o.self != o || !sameWorkerServerContextData(ctx, o.rootContext) {
		return data, errResidentWorkerServerOriginal
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := view != nil && o.view == view && originalManager != nil && o.manager == originalManager && o.claim != nil && o.managerBound != nil && o.server != nil && o.server.workerResident == o && o.server.resident == nil && sameWorkerServerContextData(o.server.lifecycleContext, o.rootContext) && o.server.Manager == nil && o.binding != nil && o.binding.owner == o && o.binding.server == o.server && o.binding.claim == o.claim
	o.mu.Unlock()
	if !valid {
		return data, errResidentWorkerServerOriginal
	}
	op, err := o.begin("validate_prepared")
	if err != nil {
		return data, err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	call := &residentWorkerServerPreparedCall{owner: o, op: op, view: view, manager: originalManager, borrower: o.managerBound}
	o.mu.Lock()
	o.preparedValidation = call
	o.mu.Unlock()
	o.phase("prepared_original_Server_claim_pending")
	call.native, resultErr = o.claim.ValidateOriginalContext(o.rootContext)
	if resultErr == nil && (call.native.Fence.Directory.Inode == 0 || call.native.Fence.Ledger.Inode == 0 || call.native != o.original || o.server.home != call.native.Path || o.server.generation != call.native.Generation) {
		resultErr = errResidentWorkerServerOriginal
	}
	if resultErr == nil {
		o.phase("prepared_same_original_Manager_Server_borrower")
		call.managerData, resultErr = originalManager.OriginalPreparedBorrowerData(call.borrower, o.claim)
		if resultErr == nil && call.managerData.Native != call.native {
			resultErr = errResidentWorkerServerOriginal
		}
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		data = residentWorkerPreparedServerData{Native: call.native, SettingsSHA256: call.managerData.SettingsSHA256, WorkspaceFormat: call.managerData.WorkspaceFormat}
	}
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return data, resultErr
}
