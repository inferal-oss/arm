package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"getarm.org/internal/database"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

type ResidentWorkerPreparedManagerData struct {
	Native                                                                  lifetimeview.WorkerSnapshot
	SettingsSHA256, WorkspaceFormat, DebitID, ProfileSHA256, ContractSHA256 string
}
type residentWorkerManagerPreparedCall struct {
	owner                                                    *ResidentWorkerManagerPending
	op                                                       *residentWorkerManagerOperation
	view                                                     *lifetimeview.WorkerManagerView
	charge                                                   *serviceview.WorkerManagerCharge
	borrower                                                 *ResidentWorkerManagerServerBinding
	pool                                                     *sql.DB
	originalSchemaTx                                         *sql.Tx
	native                                                   lifetimeview.WorkerSnapshot
	database                                                 database.WorkerPreparedDatabaseData
	settings                                                 []byte
	tx                                                       *sql.Tx
	beginAttempted, beginReturned, endAttempted, endReturned bool
	beginError, endError                                     error
	normalReturned                                           bool
	resultError                                              error
	data                                                     ResidentWorkerPreparedManagerData
}

func (o *ResidentWorkerManagerPending) ValidatePreparedOriginal(ctx context.Context, view *lifetimeview.WorkerManagerView, charge *serviceview.WorkerManagerCharge, borrower *ResidentWorkerManagerServerBinding) (data ResidentWorkerPreparedManagerData, resultErr error) {
	if o == nil || o.self != o || ctx == nil {
		return data, errResidentWorkerManagerOriginal
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := view != nil && view == o.view && charge != nil && charge == o.charge && borrower != nil && borrower == o.server && borrower.owner == o && borrower.validated && borrower.claim != nil && o.manager != nil && o.manager.residentWorker == o && o.manager.resident == nil && o.manager.residentService == nil && o.database != nil && o.binding != nil && o.claim != nil && o.schemaTx != nil && o.schemaCommitTried && o.schemaCommitError == nil
	o.mu.Unlock()
	if !valid {
		return data, errResidentWorkerManagerOriginal
	}
	op, err := o.beginOriginal("validate_prepared")
	if err != nil {
		return data, err
	}
	c := &residentWorkerManagerPreparedCall{owner: o, op: op, view: view, charge: charge, borrower: borrower, pool: o.manager.db, originalSchemaTx: o.schemaTx}
	o.mu.Lock()
	o.preparedValidation = c
	o.mu.Unlock()
	normal := false
	defer func() {
		o.mu.Lock()
		c.normalReturned = normal && op.normalFinished && !op.abnormal
		c.resultError = resultErr
		o.mu.Unlock()
	}()
	defer o.finishOriginal(op, &normal, &resultErr)
	resultErr = o.validateWorkerPreparedBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}
func (o *ResidentWorkerManagerPending) validateWorkerPreparedBody(c *residentWorkerManagerPreparedCall) error {
	ctx := o.rootContext
	if ctx == nil {
		return errResidentWorkerManagerOriginal
	}
	o.phase("worker_prepared_original_native_claim_pending")
	var err error
	c.native, err = o.claim.ValidateOriginalContext(ctx)
	if err != nil {
		return err
	}
	if c.native != o.original || c.pool == nil || c.pool != o.manager.db || c.originalSchemaTx != o.schemaTx {
		return errResidentWorkerManagerOriginal
	}
	o.manager.mu.Lock()
	b := o.manager.residentWorkerService
	valid := o.manager.resident == nil && o.manager.residentService == nil && b != nil && b.manager == o.manager && b.claim == c.charge && b.validated && b.bound && validStandingVector(b.held)
	o.manager.mu.Unlock()
	if !valid {
		return errResidentWorkerManagerOriginal
	}
	// Native charge check is outside Manager.mu; recurring accounting uses DATA.
	observed, err := c.charge.ObserveOriginal()
	if err != nil || !observed.CurrentOriginalValidated || observed.DebitID != b.debit || observed.ProfileSHA256 != b.profile || observed.ContractSHA256 != b.contract || observed.HomeID.Device != c.native.Device || observed.HomeID.Inode != c.native.Inode || observed.HomeID.UID != c.native.UID || observed.Resources.CPU != b.held.CPU || observed.Resources.Memory != b.held.Memory || observed.Resources.Disk != b.held.Disk {
		return errResidentWorkerManagerOriginal
	}
	if err = o.settings.Validate(); err != nil {
		return err
	}
	c.settings, err = json.Marshal(o.settings)
	if err != nil || len(c.settings) > 16<<10 {
		return errResidentWorkerManagerOriginal
	}
	c.database, err = o.database.ValidatePreparedOriginal(ctx, o.binding, o.claim, c.pool)
	if err != nil {
		return err
	}
	if c.database.Native != c.native {
		return errResidentWorkerManagerOriginal
	}
	o.phase("worker_prepared_same_pool_schema_begin_pending")
	c.beginAttempted = true
	c.tx, c.beginError = c.pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	c.beginReturned = true
	if c.beginError != nil {
		return c.beginError
	}
	if err = o.checkOriginalWorkspace(ctx, c.tx); err != nil {
		return err
	}
	c.endAttempted = true
	c.endError = c.tx.Rollback()
	c.endReturned = true
	if c.endError != nil {
		return c.endError
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	sum := sha256.Sum256(c.settings)
	c.data = ResidentWorkerPreparedManagerData{Native: c.native, SettingsSHA256: hex.EncodeToString(sum[:]), WorkspaceFormat: residentWorkerManagerWorkspaceFormat, DebitID: b.debit, ProfileSHA256: b.profile, ContractSHA256: b.contract}
	return nil
}

// Bounded DATA from the exact completed original prepared call, not a new claim.
func (o *ResidentWorkerManagerPending) OriginalPreparedBorrowerData(binding *ResidentWorkerManagerServerBinding, claim *lifetimeview.WorkerServerClaim) (ResidentWorkerPreparedManagerData, error) {
	if o == nil || o.self != o {
		return ResidentWorkerPreparedManagerData{}, errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.preparedValidation
	if o.active != nil || o.firstError != nil || !o.ready || o.startTried || o.started || c == nil || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "validate_prepared" || !c.op.normalFinished || c.op.abnormal || !c.normalReturned || c.resultError != nil || c.borrower != binding || binding == nil || binding != o.server || binding.owner != o || !binding.validated || claim == nil || binding.claim != claim {
		return ResidentWorkerPreparedManagerData{}, errResidentWorkerManagerOriginal
	}
	return c.data, nil
}
