package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yrashk/arm/internal/local"
	"golang.org/x/sys/unix"
)

// InstallResetSeed is called by the daemon after taking its existing authority
// lock and before Manager.Open. The exact passed descriptor remains locked for
// the caller's entire lifetime; this function never unlocks or closes it.
// Direct/unlocked manager callers must not install an unconsumed seed.
func InstallResetSeed(ctx context.Context, home string, lock *os.File) error {
	if err := local.CheckStateFence(home); err != nil {
		return err
	}
	present, err := resetPointerPresent(home)
	if err != nil || !present {
		return err
	}
	d, err := OpenDirectory(home)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = verifyInstallLock(d, lock); err != nil {
		return err
	}
	seed, seedFile, err := readResetJSON[ResetSeed](d, ResetSeedName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	s, err := openResetStore(d.Path, seed.Operation, false)
	if err != nil {
		return err
	}
	defer s.close()
	r, _, err := s.readReceipt()
	if err != nil {
		return err
	}
	if err = validSeedReceipt(seed, r); err != nil {
		return err
	}
	if seed.Installed {
		f, _, err := d.openSource("state.db")
		if err != nil {
			return err
		}
		if f == nil {
			return fmt.Errorf("consumed reset seed cannot recreate a missing established database")
		}
		return f.Close()
	}
	// A seed installation can be resumed after rename acknowledgement loss,
	// but only by recognizing the SAME prebuilt inode and applied marker.
	for _, name := range []string{"state.db-wal", "state.db-shm", "state.db-journal"} {
		v, err := d.Observe(name, false)
		if err != nil || v.Present {
			return fmt.Errorf("fresh authority installation found unexpected SQLite companions")
		}
	}
	f, main, err := d.openSource("state.db")
	if err != nil {
		return err
	}
	if f != nil {
		f.Close()
	}
	expected := seed.File
	expected.Name = "state.db"
	if !main.Present {
		if _, err = exactResetFile(ctx, s.operation, seed.File, false); err != nil {
			return err
		}
		if err = verifyInstallLock(d, lock); err != nil {
			return err
		}
		if err = d.Validate(); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = renameResetExclusive(int(s.operation.FD.Fd()), resetFreshName, int(d.FD.Fd()), "state.db"); err != nil {
			return err
		}
		if err = errors.Join(d.Sync(), s.operation.Sync()); err != nil {
			return err
		}
	} else {
		if !sameIdentity(main, expected) {
			return fmt.Errorf("established database differs from unconsumed authority seed; it was not repaired")
		}
		if staged, err := s.operation.Observe(resetFreshName, false); err != nil || staged.Present {
			return fmt.Errorf("fresh authority appears at conflicting paths")
		}
	}
	if _, err = exactResetFile(ctx, d, expected, false); err != nil {
		return err
	}
	if err = validateInstalledSeed(ctx, filepath.Join(d.Path, "state.db"), seed); err != nil {
		return err
	}
	if err = verifyInstallLock(d, lock); err != nil {
		return err
	}
	seed.Installed = true
	return atomicResetJSON(d, ResetSeedName, seed, &seedFile)
}

func verifyInstallLock(d *Directory, lock *os.File) error {
	if lock == nil {
		return fmt.Errorf("existing exclusive daemon lock descriptor required")
	}
	held, err := nativeFile(lock, "daemon.lock", false)
	if err != nil {
		return err
	}
	named, err := d.Observe("daemon.lock", false)
	if err != nil || held.Size != 0 || !sameIdentity(held, named) {
		return fmt.Errorf("daemon lock descriptor differs from private named authority lock")
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("exclusive daemon lock required for seed installation: %w", err)
	}
	return d.Validate()
}

func validSeedReceipt(seed ResetSeed, r ResetReceipt) error {
	if seed.Schema != ResetSchema || seed.Operation != r.Plan.ID || seed.Authority != r.Plan.NewHost || seed.PlanSHA256 != r.Plan.SHA256 || !sameResetJSON(seed.File, r.Seed) || r.Phase != "completed" || !r.SeedReady {
		return fmt.Errorf("fresh authority pointer differs from completed exact reset receipt")
	}
	return nil
}

func validateInstalledSeed(ctx context.Context, path string, seed ResetSeed) error {
	db, err := openRO(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var host, raw string
	if err = db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); err != nil {
		return err
	}
	if err = db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key=?`, resetAppliedKey).Scan(&raw); err != nil {
		return err
	}
	var applied resetApplied
	if err = strictEvidence(raw, &applied); err != nil {
		return err
	}
	if host != seed.Authority || !sameResetJSON(applied, resetApplied{ResetSchema, seed.Operation, seed.PlanSHA256, seed.Authority}) {
		return fmt.Errorf("fresh database does not contain the exact prebuilt applied-operation marker")
	}
	return nil
}

// CheckStartupReset is a read-only startup fence for callers which do not own
// the daemon lock. It never installs or regenerates an authority.
func CheckStartupReset(home string) error {
	present, err := resetPointerPresent(home)
	if err != nil || !present {
		return err
	}
	d, err := OpenDirectory(home)
	if err != nil {
		return err
	}
	defer d.Close()
	seed, _, err := readResetJSON[ResetSeed](d, ResetSeedName)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !seed.Installed {
		return fmt.Errorf("fresh authority requires installation by the backend under daemon.lock; start ARM before opening this ledger directly")
	}
	s, err := openResetStore(d.Path, seed.Operation, false)
	if err != nil {
		return err
	}
	defer s.close()
	r, _, err := s.readReceipt()
	if err != nil {
		return err
	}
	if err = validSeedReceipt(seed, r); err != nil {
		return err
	}
	f, _, err := d.openSource("state.db")
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("consumed authority seed cannot recreate a missing established database")
	}
	return f.Close()
}

// ExistingAuthority requires established metadata before any migration. Missing
// or malformed host identity is an explicit refusal, never a random repair.
func ExistingAuthority(ctx context.Context, db *sql.DB) (string, error) {
	var host string
	if err := db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); err != nil {
		return "", fmt.Errorf("established database authority is missing; no new identity was generated: %w", err)
	}
	if !validHost(host) {
		return "", fmt.Errorf("established database authority is malformed; no new identity was generated")
	}
	return host, nil
}

func StartupAuthority(ctx context.Context, home string, db *sql.DB) (string, error) {
	host, err := ExistingAuthority(ctx, db)
	if err != nil {
		return "", err
	}
	present, err := resetPointerPresent(home)
	if err != nil {
		return "", err
	}
	if !present {
		return host, nil
	}
	d, err := OpenDirectory(home)
	if err != nil {
		return "", err
	}
	defer d.Close()
	seed, _, err := readResetJSON[ResetSeed](d, ResetSeedName)
	if os.IsNotExist(err) {
		return host, nil
	}
	if err != nil {
		return "", err
	}
	if !seed.Installed || seed.Authority != host {
		return "", fmt.Errorf("established host differs from consumed reset authority; it was not repaired")
	}
	return host, nil
}

// Ordinary installations without reset state keep the existing startup path
// semantics (for example macOS /var -> /private/var). Observing ABSENCE is
// anchored to the verified immediate home and never creates or follows a seed.
// A present seed still requires the stronger complete maintenance namespace.
func resetPointerPresent(home string) (bool, error) {
	if err := local.CheckStateFence(home); err != nil {
		return false, err
	}
	before, err := os.Lstat(home)
	if err != nil {
		return false, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 {
		return false, fmt.Errorf("ARM state directory must remain private and real")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return false, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if _, err = nativeFile(dir, ".", true); err != nil {
		return false, err
	}
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return false, fmt.Errorf("startup home changed while observing reset state")
	}
	_, err = root.Lstat(ResetSeedName)
	if os.IsNotExist(err) {
		after, e := os.Lstat(home)
		if e != nil || !os.SameFile(opened, after) {
			return false, fmt.Errorf("startup home changed while observing reset absence")
		}
		return false, nil
	}
	return err == nil, err
}
