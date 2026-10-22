package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const resetAppliedKey = "reset_applied_v1"

type resetApplied struct {
	Schema    int    `json:"schema"`
	Operation string `json:"operation"`
	Plan      string `json:"plan_sha256"`
	Authority string `json:"authority"`
}

// buildResetSeed creates the fresh authority before touching the old ledger.
// Its inode is journalled before SQLite writes. A crash during construction
// therefore cannot grant cleanup rights over a replacement staging file.
// MEMORY rollback journals keep an interrupted build confined to that inode;
// the finished database is switched to standalone DELETE mode before hashing.
func (s *resetStore) buildResetSeed(ctx context.Context, r *ResetReceipt, receiptFile *File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.SeedReady {
		_, err := exactResetFile(ctx, s.operation, r.Seed, false)
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	// Unknown companions are never removed or opened as a SQLite journal.
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		v, err := s.operation.Observe(resetFreshName+suffix, false)
		if err != nil || v.Present {
			return fmt.Errorf("unverified fresh-database companion retained: %s", resetFreshName+suffix)
		}
	}
	if !r.Seed.Present {
		f, v, err := s.operation.Open(resetFreshName, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
		if err != nil {
			return err
		}
		if err = errors.Join(f.Sync(), f.Close(), s.operation.Sync()); err != nil {
			return err
		}
		r.Seed = v
		if err = s.updateReceipt(r, receiptFile); err != nil {
			return err
		}
	} else {
		f, v, err := s.operation.Open(resetFreshName, unix.O_RDWR)
		if err != nil {
			return err
		}
		if f == nil {
			return fmt.Errorf("recorded fresh database is missing")
		}
		if !sameIdentity(v, r.Seed) {
			f.Close()
			return fmt.Errorf("fresh database staging identity changed")
		}
		// Only this exact recorded construction inode is restartable. No old
		// ledger or child artifact is ever truncated by this path.
		err = f.Truncate(0)
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return err
		}
	}
	path := filepath.Join(s.operation.Path, resetFreshName)
	u := &url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	err = writeResetSeed(ctx, db, r.Plan)
	err = errors.Join(err, db.Close())
	if err != nil {
		return err
	}
	if err = s.validate(); err != nil {
		return err
	}
	f, v, err := s.operation.Open(resetFreshName, unix.O_RDWR)
	if err != nil {
		return err
	}
	if f == nil {
		return os.ErrNotExist
	}
	defer f.Close()
	if !sameIdentity(v, r.Seed) {
		return fmt.Errorf("fresh database changed during construction")
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if v.SHA256, err = hashFile(ctx, f); err != nil {
		return err
	}
	if err = s.operation.Sync(); err != nil {
		return err
	}
	r.Seed, r.SeedReady = v, true
	return s.updateReceipt(r, receiptFile)
}

func writeResetSeed(ctx context.Context, db *sql.DB, plan ResetPlan) error {
	for _, q := range []string{`PRAGMA journal_mode=MEMORY`, `PRAGMA synchronous=FULL`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	marker, err := json.Marshal(resetApplied{ResetSchema, plan.ID, plan.SHA256, plan.NewHost})
	if err != nil {
		return err
	}
	for _, entry := range [][2]string{{"host", plan.NewHost}, {resetAppliedKey, string(marker)}} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES(?,?)`, entry[0], entry[1]); err != nil {
			return err
		}
	}
	if plan.StorageRoot != nil {
		root, err := json.Marshal(plan.StorageRoot)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('storage_root',?)`, string(root)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var mode string
	if err = db.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		return fmt.Errorf("fresh database did not become standalone: %w", err)
	}
	var check string
	if err = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("fresh database integrity check failed: %w", err)
	}
	return ctx.Err()
}

// After an atomic publication reports fsync failure, do not continue from a
// guessed in-memory version. A new explicit continuation reads durable state.
func (s *resetStore) updateReceipt(r *ResetReceipt, file *File) error {
	r.Revision++
	if err := s.saveReceipt(*r, file); err != nil {
		return err
	}
	next, observed, err := s.readReceipt()
	if err != nil {
		return err
	}
	*r, *file = next, observed
	return nil
}
