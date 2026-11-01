package database

import (
	"context"
	"database/sql"

	"getarm.org/internal/residentprotocol"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

// Fresh original-operation DATA, never a pool/native/Ready capability.
type WorkerCurrentDatabaseData struct{ Native lifetimeview.WorkerSnapshot }
type workerCurrentDatabaseCall struct {
	owner                                     *WorkerResidentOpen
	op                                        *workerResidentSQLOperation
	ctx                                       context.Context
	prepared                                  *workerPreparedDatabaseCall
	binding                                   *WorkerResidentDatabaseBinding
	claim                                     *lifetimeview.WorkerManagerClaim
	pool                                      *sql.DB
	view                                      *serviceview.WorkerCurrentChallengeView
	charge                                    *serviceview.WorkerManagerCharge
	challenge                                 residentprotocol.WorkerCurrentChallengeBodyData
	viewAttempted, viewReturned               bool
	viewError                                 error
	nativeAttempted, nativeReturned           bool
	native                                    lifetimeview.WorkerSnapshot
	nativeError                               error
	filesAttempted, filesReturned             bool
	filesError                                error
	queryAttempted, queryReturned             bool
	rows                                      *sql.Rows
	queryError, scanError, rowsError          error
	nextCalls, scanCalls, rowCount            int
	rowsRead                                  [3]workerPreparedDatabaseRow // fresh DATA slots, no prepared Rows alias
	closeAttempted, closeReturned             bool
	closeError                                error
	viewRecheckAttempted, viewRecheckReturned bool
	viewRecheck                               residentprotocol.WorkerCurrentChallengeBodyData
	viewRecheckError                          error
	data                                      WorkerCurrentDatabaseData
	normalReturned                            bool
	resultError                               error
}

func (o *WorkerResidentOpen) currentBindingLocked(binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB) bool {
	return o.firstError == nil && !o.closeTried && !o.connectSealed && binding != nil && binding == o.binding && binding.owner == o && pool != nil && pool == o.pool && binding.pool == pool && claim != nil && claim == o.claim && o.connector != nil && o.connector.owner == o && o.connector.original == o.standardConnector && o.connector.originalClaim == claim && o.connect != nil && o.connect.owner == o && o.connect.connector == o.connector && o.connect.finished && !o.connect.abnormal && o.connect.resultErr == nil && o.connect.returned != nil && o.connectRefusals == 0
}
func (o *WorkerResidentOpen) currentPreparedLocked(binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB) bool {
	p := o.preparedValidation
	return o.preparedTried && o.currentBindingLocked(binding, claim, pool) && p != nil && p.owner == o && p.op != nil && p.op.owner == o && p.op.kind == "validate_prepared" && p.op.normalFinished && !p.op.abnormal && p.returned && p.resultError == nil && p.binding == binding && p.claim == claim && p.pool == pool
}
func (o *WorkerResidentOpen) beginCurrentOriginal(ctx context.Context, binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB, view *serviceview.WorkerCurrentChallengeView, charge *serviceview.WorkerManagerCharge) (*workerCurrentDatabaseCall, error) {
	if o == nil || o.self != o || ctx == nil || view == nil || charge == nil {
		return nil, errWorkerResidentDatabaseOriginal
	}
	if err := charge.RequireOriginalRootContext(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	valid := o.active == nil && o.currentPreparedLocked(binding, claim, pool)
	o.mu.Unlock()
	if !valid {
		return nil, errWorkerResidentDatabaseOriginal
	}
	op, err := o.beginOriginal("validate_current")
	if err != nil {
		return nil, err
	}
	call := &workerCurrentDatabaseCall{owner: o, op: op, ctx: ctx, prepared: o.preparedValidation, binding: binding, claim: claim, pool: pool, view: view, charge: charge}
	o.mu.Lock()
	o.currentValidation = call
	o.mu.Unlock() // before any foreign/native/SQL call
	return call, nil
}
func (o *WorkerResidentOpen) currentCallLocked(c *workerCurrentDatabaseCall) bool {
	return c != nil && c == o.currentValidation && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "validate_current" && !c.op.abnormal && o.active == c.op && c.ctx != nil && c.prepared == o.preparedValidation && c.view != nil && c.charge != nil && o.currentPreparedLocked(c.binding, c.claim, c.pool)
}
func (o *WorkerResidentOpen) currentGate(c *workerCurrentDatabaseCall) error {
	o.mu.Lock()
	valid := o.currentCallLocked(c)
	o.mu.Unlock()
	if !valid {
		return errWorkerResidentDatabaseOriginal
	}
	return c.ctx.Err()
}
func (o *WorkerResidentOpen) publishCurrentOriginal(c *workerCurrentDatabaseCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c == nil || c != o.currentValidation || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "validate_current" {
		panic("original current database identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}
func (o *WorkerResidentOpen) ValidateCurrentOriginal(ctx context.Context, binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB, view *serviceview.WorkerCurrentChallengeView, charge *serviceview.WorkerManagerCharge) (data WorkerCurrentDatabaseData, resultErr error) {
	c, err := o.beginCurrentOriginal(ctx, binding, claim, pool, view, charge)
	if err != nil {
		return data, err
	}
	normal := false
	defer o.publishCurrentOriginal(c, &normal, &resultErr)
	defer o.finishOriginal(c.op, &normal, &resultErr)
	resultErr = o.validateCurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}
func (o *WorkerResidentOpen) validateCurrentBody(c *workerCurrentDatabaseCall) error {
	ctx := c.ctx
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.viewAttempted = true
	c.challenge, c.viewError = c.view.ValidateOriginal(c.charge)
	c.viewReturned = true
	if c.viewError != nil {
		return c.viewError
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.nativeAttempted = true
	c.native, c.nativeError = c.claim.ValidateOriginalContext(ctx)
	c.nativeReturned = true
	if c.nativeError != nil {
		return c.nativeError
	}
	if c.native.Fence.Directory.Inode == 0 || c.native.Fence.Ledger.Inode == 0 || c.native.Generation != c.binding.generation || c.native.Generation != c.challenge.Context.Generation || c.native != c.prepared.native {
		return errWorkerResidentDatabaseOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.filesAttempted = true
	c.filesError = o.matchOriginalPreparedFiles(ctx, c.native)
	c.filesReturned = true
	if c.filesError != nil {
		return c.filesError
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	if err := o.readCurrentSelectedFile(ctx, c); err != nil {
		return err
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.viewRecheckAttempted = true
	c.viewRecheck, c.viewRecheckError = c.view.ValidateOriginal(c.charge)
	c.viewRecheckReturned = true
	if c.viewRecheckError != nil {
		return c.viewRecheckError
	}
	if c.viewRecheck != c.challenge {
		return errWorkerResidentDatabaseOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.data = WorkerCurrentDatabaseData{Native: c.native}
	return ctx.Err()
}
func (o *WorkerResidentOpen) readCurrentSelectedFile(ctx context.Context, c *workerCurrentDatabaseCall) error {
	o.setOriginalPhase("current_original_selected_file_query_pending")
	c.queryAttempted = true
	c.rows, c.queryError = c.pool.QueryContext(ctx, `SELECT seq,typeof(name),length(CAST(name AS BLOB)),substr(CAST(name AS BLOB),1,5),typeof(file),length(CAST(file AS BLOB)),substr(CAST(file AS BLOB),1,4097) FROM pragma_database_list LIMIT 3`)
	c.queryReturned = true
	if c.queryError != nil {
		return c.queryError
	}
	if c.rows == nil {
		return errWorkerResidentDatabaseOriginal
	}
	for c.nextCalls < 4 {
		c.nextCalls++
		if !c.rows.Next() {
			break
		}
		if c.rowCount == len(c.rowsRead) {
			return errWorkerResidentDatabaseOriginal
		}
		row := &c.rowsRead[c.rowCount]
		c.scanCalls++
		c.scanError = c.rows.Scan(&row.sequence, &row.nameType, &row.nameBytes, &row.namePrefix, &row.fileType, &row.fileBytes, &row.filePrefix)
		if c.scanError != nil {
			return c.scanError
		}
		c.rowCount++
	}
	c.rowsError = c.rows.Err()
	if c.rowsError != nil {
		return c.rowsError
	}
	// Explicit public close only after normal complete iteration. No cleanup on
	// a partial/error/abnormal/unreturned call, and no native-Closed inference.
	c.closeAttempted = true
	c.closeError = c.rows.Close()
	c.closeReturned = true
	if c.closeError != nil {
		return c.closeError
	}
	return validateWorkerPreparedSelectedRowsData(c.rowsRead[:c.rowCount], o.filename) // pure DATA only
}
func (o *WorkerResidentOpen) completedCurrentLocked(c *workerCurrentDatabaseCall, binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB, view *serviceview.WorkerCurrentChallengeView, charge *serviceview.WorkerManagerCharge) bool {
	return o.active == nil && o.currentTried && c != nil && c == o.currentValidation && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "validate_current" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && c.prepared == o.preparedValidation && c.binding == binding && c.claim == claim && c.pool == pool && c.view == view && view != nil && c.charge == charge && charge != nil && o.currentPreparedLocked(binding, claim, pool) && c.viewAttempted && c.viewReturned && c.viewError == nil && c.nativeAttempted && c.nativeReturned && c.nativeError == nil && c.filesAttempted && c.filesReturned && c.filesError == nil && c.queryAttempted && c.queryReturned && c.queryError == nil && c.rows != nil && c.scanError == nil && c.rowsError == nil && c.rowCount > 0 && c.rowCount <= 2 && c.nextCalls <= 4 && c.scanCalls == c.rowCount && c.closeAttempted && c.closeReturned && c.closeError == nil && c.viewRecheckAttempted && c.viewRecheckReturned && c.viewRecheckError == nil && c.viewRecheck == c.challenge && c.data.Native == c.native
}

// Exact completed CURRENT call only. Snapshot local metadata, release lock for
// foreign phase validation, then recheck same original/no-active identity.
func (o *WorkerResidentOpen) CurrentOriginalData(binding *WorkerResidentDatabaseBinding, claim *lifetimeview.WorkerManagerClaim, pool *sql.DB, view *serviceview.WorkerCurrentChallengeView, charge *serviceview.WorkerManagerCharge) (WorkerCurrentDatabaseData, error) {
	if o == nil || o.self != o {
		return WorkerCurrentDatabaseData{}, errWorkerResidentDatabaseOriginal
	}
	o.mu.Lock()
	c := o.currentValidation
	valid := o.completedCurrentLocked(c, binding, claim, pool, view, charge)
	o.mu.Unlock()
	if !valid {
		return WorkerCurrentDatabaseData{}, errWorkerResidentDatabaseOriginal
	}
	body, err := view.ValidateOriginal(charge)
	if err != nil {
		return WorkerCurrentDatabaseData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.completedCurrentLocked(c, binding, claim, pool, view, charge) || body != c.challenge {
		return WorkerCurrentDatabaseData{}, errWorkerResidentDatabaseOriginal
	}
	return c.data, nil
}
