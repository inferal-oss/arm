package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// StartupRead owns a WAL-aware read-only transaction under the caller's retained
// actual daemon.lock. Unlike maintenance Offline, it creates no DB-sized copy.
// Normal SQLite SHM/WAL bookkeeping may occur in the private authority directory;
// logical rows/schema/entitlements are never migrated or modified by this API.
// Existing maintenance snapshot/no-source-sidecar contracts stay unchanged.
type StartupRead struct {
	Directory *Directory
	db        *sql.DB
	tx        *sql.Tx
	ctx       context.Context
	main      File
	wal       File
	walEmpty  bool
	failure   error
	lock      *os.File // borrowed; never unlocked or closed here
	files     []*os.File
}

func OpenStartupRead(ctx context.Context, home string, lock *os.File) (read *StartupRead, present bool, resultErr error) {
	if lock == nil {
		return nil, false, fmt.Errorf("retained authority lock required")
	}
	dir, err := OpenDirectory(home)
	if err != nil {
		return nil, false, err
	}
	r := &StartupRead{Directory: dir, ctx: ctx, lock: lock}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, r.Close())
		}
	}()
	if err = r.checkLock(); err != nil {
		return nil, false, err
	}
	main, identity, err := dir.openSource("state.db")
	if err != nil {
		return nil, false, err
	}
	r.main = identity
	if main != nil {
		r.files = append(r.files, main)
	}
	for _, name := range []string{"state.db-wal", "state.db-shm", "state.db-journal"} {
		file, id, e := dir.openSource(name)
		if e != nil {
			return nil, false, e
		}
		if file != nil {
			r.files = append(r.files, file)
		}
		if name == "state.db-wal" {
			r.wal = id
			if id.Present {
				r.walEmpty, e = emptyStartupWAL(file, id)
				if e != nil {
					return nil, false, e
				}
			} else {
				r.walEmpty = true
			}
		}
		if id.Present && (!identity.Present || name == "state.db-journal") {
			return nil, false, fmt.Errorf("orphan or rollback journal requires explicit inspection; no stopped intent was assumed absent")
		}
	}
	if !identity.Present {
		return r, false, r.Validate(ctx)
	}
	r.db, err = openRO(filepath.Join(home, "state.db"))
	if err != nil {
		return nil, false, err
	}
	r.tx, err = r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	// The first read establishes a single SQLite snapshot including the WAL's
	// committed end mark. Every schema/record lookup uses this transaction.
	var schema int
	if err = r.tx.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schema); err != nil {
		return nil, false, err
	}
	if err = r.Validate(ctx); err != nil {
		return nil, false, err
	}
	return r, true, nil
}

func (r *StartupRead) Query(query string, args ...any) (*sql.Rows, error) {
	if r == nil || r.tx == nil {
		return nil, fmt.Errorf("existing read-only database required")
	}
	return r.tx.QueryContext(r.ctx, query, args...)
}
func (r *StartupRead) QueryRow(query string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.ctx, query, args...)
}

func (r *StartupRead) checkLock() error {
	held, err := nativeFile(r.lock, "daemon.lock", false)
	if err != nil {
		return err
	}
	named, err := r.Directory.Observe("daemon.lock", false)
	if err != nil {
		return err
	}
	if !sameIdentity(held, named) {
		return fmt.Errorf("retained daemon lock changed")
	}
	// Idempotent on this original open-file description; no alternate lock/PID
	// fallback. The caller keeps this exact descriptor through startup/close.
	return unix.Flock(int(r.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func (r *StartupRead) observeSource(name string) (File, error) {
	f, id, err := r.Directory.openSource(name)
	if err != nil {
		return id, err
	}
	if f != nil {
		err = f.Close()
	}
	return id, err
}

func (r *StartupRead) validateSidecars() error {
	for _, name := range []string{"state.db-shm", "state.db-journal"} {
		file, id, e := r.Directory.openSource(name)
		if e != nil {
			return e
		}
		if file != nil {
			closeErr := file.Close()
			if closeErr != nil {
				return closeErr
			}
		}
		if id.Present && (name == "state.db-journal" || !r.main.Present) {
			return fmt.Errorf("new orphan/rollback sidecar appeared during stopped read")
		}
	}
	return r.validateWAL()
}

func (r *StartupRead) Validate(ctx context.Context) (err error) {
	defer func() { r.failure = errors.Join(r.failure, err) }()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = r.Directory.Validate(); err != nil {
		return err
	}
	if err = r.checkLock(); err != nil {
		return err
	}
	main, err := r.observeSource("state.db")
	if err != nil {
		return err
	}
	if !sameIdentity(main, r.main) || main.Size != r.main.Size || main.Modified != r.main.Modified || main.Changed != r.main.Changed {
		return fmt.Errorf("read-only startup main file changed")
	}
	return r.validateSidecars()
}

// closeSQLite and finishClose split the real close cuts so private tests can
// commit through an independent SQLite connection between them deterministically.
// Close is the public owner; these helpers add no callback/native authority.
func (r *StartupRead) closeSQLite() (err error) {
	if r.tx != nil {
		e := r.tx.Rollback()
		if !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, e)
		}
		r.tx = nil
	}
	if r.db != nil {
		err = errors.Join(err, r.db.Close())
		r.db = nil
	}
	return err
}

func (r *StartupRead) finishClose() (err error) {
	for _, file := range r.files {
		err = errors.Join(err, file.Close())
	}
	r.files = nil
	if r.Directory != nil {
		now, e := r.observeSource("state.db")
		err = errors.Join(err, e)
		if e == nil && (!sameIdentity(now, r.main) || now.Size != r.main.Size || now.Modified != r.main.Modified || now.Changed != r.main.Changed) {
			err = errors.Join(err, fmt.Errorf("read-only startup main changed on close"))
		}
		err = errors.Join(err, r.checkLock(), r.validateSidecars())
		err = errors.Join(err, r.Directory.Validate(), r.Directory.Close())
		r.Directory = nil
	}
	r.failure = errors.Join(r.failure, err)
	return r.failure
}

func (r *StartupRead) Close() (err error) {
	if r == nil {
		return nil
	}
	if r.Directory != nil {
		err = errors.Join(err, r.Validate(r.ctx))
	}
	err = errors.Join(err, r.closeSQLite(), r.finishClose())
	r.failure = errors.Join(r.failure, err)
	return r.failure
}
