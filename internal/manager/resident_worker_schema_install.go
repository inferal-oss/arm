package manager

import (
	"context"
	"database/sql"
	"getarm.org/internal/local"
	"getarm.org/internal/syncservice"
)

type residentWorkerSchemaExec struct {
	statement           string
	arguments           []any
	attempted, returned bool
	result              sql.Result
	err                 error
}
type residentWorkerSchemaInstallCall struct {
	owner                                                                *ResidentWorkerManagerPending
	op                                                                   *residentWorkerManagerOperation
	ctx                                                                  context.Context
	pool                                                                 *sql.DB
	tx                                                                   *sql.Tx
	pragmas                                                              [3]*residentWorkerSchemaExec
	statements                                                           [30]*residentWorkerSchemaExec
	durableAttempted, durableReturned                                    bool
	durableError                                                         error
	beginAttempted, beginReturned                                        bool
	beginError                                                           error
	emptyAttempted, emptyReturned, emptyScanAttempted, emptyScanReturned bool
	empty                                                                *sql.Row
	nonempty                                                             bool
	emptyError                                                           error
	hostAttempted, hostReturned                                          bool
	host                                                                 string
	commitAttempted, commitReturned                                      bool
	commitError                                                          error
	normalReturned                                                       bool
	resultError                                                          error
}

func (o *ResidentWorkerManagerPending) installFreshOriginalSchema(ctx context.Context) (resultErr error) {
	if o == nil || o.self != o || ctx == nil || o.manager == nil || o.rootContext == nil || o.claim == nil || o.database == nil || o.binding == nil || o.manager.db == nil || o.manager.residentWorker != o || o.manager.resident != nil || o.manager.residentService != nil {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	if o.active == nil || o.active.owner != o || o.active.kind != "open" || o.schemaInstall != nil {
		o.mu.Unlock()
		return errResidentWorkerManagerOriginal
	}
	c := &residentWorkerSchemaInstallCall{owner: o, op: o.active, ctx: o.rootContext, pool: o.manager.db}
	o.schemaInstall = c
	o.mu.Unlock()
	normal := false
	defer func() { o.mu.Lock(); c.normalReturned = normal; c.resultError = resultErr; o.mu.Unlock() }()
	resultErr = c.install()
	normal = true
	return resultErr
}
func (c *residentWorkerSchemaInstallCall) install() error {
	o := c.owner
	for i, statement := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=FULL`, `PRAGMA busy_timeout=5000`} {
		call := &residentWorkerSchemaExec{statement: statement}
		c.pragmas[i] = call
		o.phase("worker_original_schema_pragma_pending")
		call.attempted = true
		call.result, call.err = c.pool.ExecContext(c.ctx, call.statement)
		call.returned = true
		if call.err != nil {
			return call.err
		}
	}
	c.durableAttempted = true
	c.durableError = syncservice.VerifyDurableSQLite(c.ctx, c.pool)
	c.durableReturned = true
	if c.durableError != nil {
		return c.durableError
	}
	o.phase("worker_original_fresh_schema_begin_pending")
	c.beginAttempted = true
	c.tx, c.beginError = c.pool.BeginTx(c.ctx, nil)
	c.beginReturned = true
	o.mu.Lock()
	o.schemaTx = c.tx
	o.mu.Unlock()
	if c.beginError != nil {
		return c.beginError
	}
	c.emptyAttempted = true
	c.empty = c.tx.QueryRowContext(c.ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema LIMIT 1)`)
	c.emptyReturned = true
	c.emptyScanAttempted = true
	c.emptyError = c.empty.Scan(&c.nonempty)
	c.emptyScanReturned = true
	if c.emptyError != nil {
		return c.emptyError
	}
	if c.nonempty {
		return errResidentWorkerManagerOriginal
	}
	if len(residentManagerBaseSchema) != 22 || len(residentWorkerManagerSchema) != 6 {
		return errResidentWorkerManagerOriginal
	}
	for i, statement := range residentManagerBaseSchema {
		if err := c.exec(i, statement); err != nil {
			return err
		}
	}
	for i, statement := range residentWorkerManagerSchema {
		if err := c.exec(22+i, statement); err != nil {
			return err
		}
	}
	c.hostAttempted = true
	c.host = local.ID()
	c.hostReturned = true
	if err := c.exec(28, `INSERT INTO metadata(key,value) VALUES('host',?),('workspace_format','5')`, c.host); err != nil {
		return err
	}
	if err := c.exec(29, `INSERT INTO resident_worker_core_state(singleton,generation,revision,phase,active_epoch,activation_slot) VALUES(1,?,0,'preparing',NULL,NULL)`, o.original.Generation); err != nil {
		return err
	}
	o.phase("worker_original_fresh_schema_commit_pending")
	o.mu.Lock()
	o.schemaCommitTried = true
	o.mu.Unlock()
	c.commitAttempted = true
	c.commitError = c.tx.Commit()
	c.commitReturned = true
	o.mu.Lock()
	o.schemaCommitError = c.commitError
	o.mu.Unlock()
	return c.commitError
}
func (c *residentWorkerSchemaInstallCall) exec(index int, statement string, args ...any) error {
	if index < 0 || index >= len(c.statements) || c.statements[index] != nil || len(args) > 1 {
		return errResidentWorkerManagerOriginal
	}
	call := &residentWorkerSchemaExec{statement: statement, arguments: args}
	c.statements[index] = call
	c.owner.phase("worker_original_fresh_schema_exec_pending")
	call.attempted = true
	call.result, call.err = c.tx.ExecContext(c.ctx, call.statement, call.arguments...)
	call.returned = true
	return call.err
}
