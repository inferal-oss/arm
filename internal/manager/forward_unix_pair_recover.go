package manager

import (
	"context"
	"database/sql"
	"os"
	"syscall"

	"getarm.org/internal/database"
	"getarm.org/internal/local"
)

// checkForwardUnixRecoveryLock accepts only the original named daemon.lock
// FD. The caller retains its exclusive flock for the daemon lifetime.
func checkForwardUnixRecoveryLock(home string, lock *os.File) error {
	if lock == nil {
		return forwardUnixPairUnavailable()
	}
	if err := local.Prepare(home); err != nil {
		return err
	}
	dir, err := database.OpenDirectory(home)
	if err != nil {
		return err
	}
	defer dir.Close()
	named, _, err := dir.Open("daemon.lock", os.O_RDWR)
	if err != nil || named == nil {
		return forwardUnixPairUnavailable()
	}
	namedInfo, err := named.Stat()
	if err != nil {
		named.Close()
		return err
	}
	lockInfo, err := lock.Stat()
	if closeErr := named.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !os.SameFile(namedInfo, lockInfo) {
		return forwardUnixPairUnavailable()
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return forwardUnixPairUnavailable()
	}
	return dir.Validate()
}

// recoverForwardUnixNoBind runs on the already checked and configured Manager
// connection, before generic recovery can alter the original lease rows.
func recoverForwardUnixNoBind(ctx context.Context, home string, lock *os.File, db *sql.DB) error {
	if ctx == nil || ctx.Err() != nil || db == nil {
		return forwardUnixPairUnavailable()
	}
	if err := checkForwardUnixRecoveryLock(home, lock); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	objects, err := readForwardUnixGuardObjects(ctx, tx)
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		return nil // older ledger has no pair or pair schema
	}
	if err = validateForwardUnixGuardObjects(objects); err != nil {
		return err
	}
	if err = abortForwardUnixPlannedPairs(ctx, tx, home); err != nil {
		return err
	}
	return tx.Commit()
}
