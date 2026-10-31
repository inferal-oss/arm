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

// Original-operation DATA; never a grant, Manager pointer or caller-set Ready.
type ResidentPreparedManagerData struct {
	Native                 lifetimeview.Snapshot
	SettingsSHA256         string
	WorkspaceFormat        string
	DebitID, ProfileSHA256 string
}
type residentManagerPreparedCall struct {
	owner                         *ResidentManagerPending
	op                            *residentManagerOperation
	view                          *lifetimeview.ManagerView
	borrower                      *ResidentManagerServerBinding
	charge                        *serviceview.ManagerCharge
	pool                          *sql.DB
	originalSchemaTx              *sql.Tx
	native                        lifetimeview.Snapshot
	database                      database.ResidentPreparedDatabaseData
	settings                      []byte
	settingsSHA                   string
	beginAttempted, beginReturned bool
	tx                            *sql.Tx
	beginError                    error
	queries                       [3]*residentPreparedSchemaQuery
	endAttempted, endReturned     bool
	endError                      error
	returned                      bool
	resultError                   error
	data                          ResidentPreparedManagerData
}
type residentPreparedSchemaQuery struct {
	kind                             int
	queryAttempted, queryReturned    bool
	rows                             *sql.Rows
	queryError, scanError, rowsError error
	nextCalls, scanCalls, count      int
	data                             [2]residentPreparedSchemaRow
	closeAttempted, closeReturned    bool
	closeError                       error
}
type residentPreparedSchemaRow struct {
	firstType, secondType                  string
	firstLength, secondLength, thirdLength sql.NullInt64
	first, second, third                   []byte
	integer                                sql.NullInt64
	kindLength                             sql.NullInt64
	kindPrefix                             []byte
}

// Same actual Manager, issued view, standing charge, Server borrower and official
// pool. This remains unstarted and does not lend the Manager to Server/workers.
func (o *ResidentManagerPending) ValidatePreparedOriginal(ctx context.Context, view *lifetimeview.ManagerView, charge *serviceview.ManagerCharge, borrower *ResidentManagerServerBinding) (data ResidentPreparedManagerData, resultErr error) {
	if o == nil || o.self != o || ctx == nil {
		return data, errResidentManagerOriginal
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := view != nil && o.view == view && charge != nil && borrower != nil && o.server == borrower && borrower.owner == o && borrower.validated && borrower.claim != nil && o.manager != nil && o.manager.resident == o && o.database != nil && o.binding != nil && o.claim != nil && o.schemaTx != nil && o.schemaCommitTried && o.schemaCommitError == nil
	o.mu.Unlock()
	if !valid {
		return data, errResidentManagerOriginal
	}
	op, err := o.beginOriginal("validate_prepared")
	if err != nil {
		return data, err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	call := &residentManagerPreparedCall{owner: o, op: op, view: view, borrower: borrower, charge: charge, pool: o.manager.db, originalSchemaTx: o.schemaTx}
	o.mu.Lock()
	o.preparedValidation = call
	o.mu.Unlock()
	resultErr = o.validatePreparedBody(ctx, call)
	if resultErr == nil {
		data = call.data
	}
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return data, resultErr
}
func (o *ResidentManagerPending) validatePreparedBody(ctx context.Context, call *residentManagerPreparedCall) error {
	o.phase("prepared_original_Manager_native_claim_pending")
	var err error
	call.native, err = o.claim.ValidateOriginal()
	if err != nil {
		return err
	}
	if !call.native.InheritedFence || call.native != o.original || call.pool == nil || call.pool != o.manager.db || call.originalSchemaTx != o.schemaTx {
		return errResidentManagerOriginal
	}
	o.manager.mu.Lock()
	standing := o.manager.residentService
	valid := standing != nil && standing.manager == o.manager && standing.claim == call.charge && standing.bound && standing.validated && validStandingVector(standing.held) && standing.home == call.native.Path && standing.homeUID == call.native.UID && standing.homeDevice == call.native.Device && standing.homeInode == call.native.Inode
	o.manager.mu.Unlock()
	if !valid {
		return errResidentManagerOriginal
	}
	observed, err := call.charge.ObserveOriginal() // bounded actual original owner; no native read
	if err != nil || !observed.AdmissionAvailable || observed.DebitID != standing.debit || observed.ProfileSHA256 != standing.profile || observed.Resources.CPU != standing.held.CPU || observed.Resources.Memory != standing.held.Memory || observed.Resources.Disk != standing.held.Disk {
		return errResidentManagerOriginal
	}
	if err = o.settings.Validate(); err != nil {
		return err
	}
	call.settings, err = json.Marshal(o.settings)
	if err != nil || len(call.settings) > 16<<10 {
		return errResidentManagerOriginal
	}
	sum := sha256.Sum256(call.settings)
	call.settingsSHA = hex.EncodeToString(sum[:])
	o.phase("prepared_original_official_database_binding_pending")
	call.database, err = o.database.ValidatePreparedOriginal(ctx, o.binding, o.claim, call.pool)
	if err != nil {
		return err
	}
	if call.database.Native != call.native {
		return errResidentManagerOriginal
	}
	o.phase("prepared_original_read_transaction_begin_pending")
	call.beginAttempted = true
	call.tx, call.beginError = call.pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	call.beginReturned = true
	if call.beginError != nil {
		return call.beginError
	}
	if call.tx == nil {
		return errResidentManagerOriginal
	}
	for kind := 0; kind < len(call.queries); kind++ {
		query := &residentPreparedSchemaQuery{kind: kind}
		call.queries[kind] = query // retained before Query/Next/Scan/Close
		if err = o.readPreparedSchema(ctx, call, query); err != nil {
			return err // no unconditional rollback/close through partial/unknown work
		}
	}
	o.phase("prepared_original_read_transaction_end_pending")
	call.endAttempted = true
	call.endError = call.tx.Commit()
	call.endReturned = true // public Tx API outcome, NEVER native quiescence
	if call.endError != nil {
		return call.endError
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	call.data = ResidentPreparedManagerData{Native: call.native, SettingsSHA256: call.settingsSHA, WorkspaceFormat: residentManagerWorkspaceFormat, DebitID: observed.DebitID, ProfileSHA256: observed.ProfileSHA256}
	return nil
}

var preparedSchemaQueries = [...]string{
	`SELECT typeof(key),length(CAST(key AS BLOB)),substr(CAST(key AS BLOB),1,17),typeof(value),length(CAST(value AS BLOB)),substr(CAST(value AS BLOB),1,2) FROM metadata WHERE replace(replace(lower(CAST(key AS TEXT)),'ſ','s'),'K','k')='workspace_format' LIMIT 2`,
	`SELECT typeof(type),length(CAST(type AS BLOB)),substr(CAST(type AS BLOB),1,6),length(CAST(name AS BLOB)),substr(CAST(name AS BLOB),1,65),length(CAST(tbl_name AS BLOB)),substr(CAST(tbl_name AS BLOB),1,65),length(CAST(sql AS BLOB)),substr(CAST(sql AS BLOB),1,257) FROM sqlite_schema WHERE replace(replace(lower(name),'ſ','s'),'K','k') GLOB 'resident_worker*' OR replace(replace(lower(tbl_name),'ſ','s'),'K','k') GLOB 'resident_worker*' LIMIT 2`,
	`SELECT typeof(singleton),CAST(singleton AS INTEGER),typeof(generation),length(CAST(generation AS BLOB)),substr(CAST(generation AS BLOB),1,33) FROM resident_worker_core_state LIMIT 2`,
}

func (o *ResidentManagerPending) readPreparedSchema(ctx context.Context, call *residentManagerPreparedCall, query *residentPreparedSchemaQuery) error {
	o.phase("prepared_original_bounded_schema_query_pending")
	query.queryAttempted = true
	query.rows, query.queryError = call.tx.QueryContext(ctx, preparedSchemaQueries[query.kind])
	query.queryReturned = true
	if query.queryError != nil {
		return query.queryError
	}
	if query.rows == nil {
		return errResidentManagerOriginal
	}
	for query.nextCalls < 3 {
		query.nextCalls++
		if !query.rows.Next() {
			break
		}
		if query.count == len(query.data) {
			return errResidentManagerOriginal
		}
		row := &query.data[query.count]
		query.scanCalls++
		switch query.kind {
		case 0:
			query.scanError = query.rows.Scan(&row.firstType, &row.firstLength, &row.first, &row.secondType, &row.secondLength, &row.second)
		case 1:
			// type/name/table/DDL have separate bounded prefixes; type is kept
			// in secondType only after its own bounded temporary field scan.
			query.scanError = query.rows.Scan(&row.firstType, &row.kindLength, &row.kindPrefix, &row.firstLength, &row.first, &row.secondLength, &row.second, &row.thirdLength, &row.third)
		case 2:
			query.scanError = query.rows.Scan(&row.firstType, &row.integer, &row.secondType, &row.firstLength, &row.first)
		}
		if query.scanError != nil {
			return query.scanError
		}
		query.count++
	}
	query.rowsError = query.rows.Err()
	if query.rowsError != nil {
		return query.rowsError
	}
	query.closeAttempted = true
	query.closeError = query.rows.Close()
	query.closeReturned = true
	if query.closeError != nil {
		return query.closeError
	}
	return validatePreparedSchemaRowsData(query.kind, query.data[:query.count], o.original.Generation)
}
func validatePreparedSchemaRowsData(kind int, rows []residentPreparedSchemaRow, generation string) error {
	if len(rows) != 1 || len(generation) != 32 {
		return errResidentManagerOriginal
	}
	row := rows[0]
	switch kind {
	case 0:
		if row.firstType != "text" || !row.firstLength.Valid || row.firstLength.Int64 != 16 || string(row.first) != "workspace_format" || row.secondType != "text" || !row.secondLength.Valid || row.secondLength.Int64 != 1 || string(row.second) != residentManagerWorkspaceFormat {
			return errResidentManagerOriginal
		}
	case 1:
		if row.firstType != "text" || !row.kindLength.Valid || row.kindLength.Int64 != 5 || string(row.kindPrefix) != "table" || !row.firstLength.Valid || row.firstLength.Int64 != int64(len("resident_worker_core_state")) || string(row.first) != "resident_worker_core_state" || !row.secondLength.Valid || row.secondLength.Int64 != int64(len("resident_worker_core_state")) || string(row.second) != "resident_worker_core_state" || !row.thirdLength.Valid || row.thirdLength.Int64 != int64(len(residentManagerCoreSchema)) || string(row.third) != residentManagerCoreSchema {
			return errResidentManagerOriginal
		}
	case 2:
		if row.firstType != "integer" || !row.integer.Valid || row.integer.Int64 != 1 || row.secondType != "text" || !row.firstLength.Valid || row.firstLength.Int64 != int64(len(generation)) || string(row.first) != generation {
			return errResidentManagerOriginal
		}
	default:
		return errResidentManagerOriginal
	}
	return nil
}

// Bounded DATA from the SAME actual completed prepared call for its one Server
// borrower. It never lends Manager/pool/native pointers or starts anything.
func (o *ResidentManagerPending) OriginalPreparedBorrowerData(binding *ResidentManagerServerBinding, claim *lifetimeview.ServerClaim) (data ResidentPreparedManagerData, resultErr error) {
	if o == nil || o.self != o {
		return data, errResidentManagerOriginal
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	call := o.preparedValidation
	if o.active != nil || o.firstError != nil || !o.ready || o.startTried || o.started || call == nil || !call.returned || call.resultError != nil || call.owner != o || call.borrower != binding || binding == nil || binding != o.server || binding.owner != o || !binding.validated || binding.claim != claim || claim == nil {
		return data, errResidentManagerOriginal
	}
	return call.data, nil
}
