package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// prepareResetLocks is called only after explicit confirmation, with stopped
// daemon authority and all already-present coordination locks still held.
// A newly appearing child never becomes permission to adopt its contents.
func (r *ResetReview) prepareResetLocks(ctx context.Context) error {
	review := r.review
	home := review.source.Directory
	lifecycle := filepath.Join(home.Path, "mesh-lifecycle.lock")
	if !review.namespacePresent(lifecycle) {
		if err := review.createLock(home, "mesh-lifecycle.lock"); err != nil {
			return err
		}
	}
	if r.Plan.Forget {
		return review.Validate(ctx)
	}
	var pending *Directory
	for _, d := range review.directories {
		if filepath.Base(d.Path) == "dashboard-pending" {
			pending = d
			break
		}
	}
	if pending == nil {
		if err := unix.Mkdirat(int(home.FD.Fd()), "dashboard-pending", 0700); err != nil {
			return fmt.Errorf("pending namespace appeared or cannot be created: %w", err)
		}
		if err := home.Sync(); err != nil {
			return err
		}
		d, err := resetChild(home, "dashboard-pending", false)
		if err != nil {
			return err
		}
		pending = d
		review.directories = append(review.directories, d)
		review.removeNamespace("dashboard-pending")
	}
	lock := filepath.Join(pending.Path, ".lock")
	if !review.namespacePresent(lock) {
		if err := review.createLock(pending, ".lock"); err != nil {
			return err
		}
	}
	// Replace ONLY the roster changed by our own explicit lock creation.
	// Any intent which raced before the locked recheck remains a blocker.
	review.removeNamespace("dashboard-pending")
	files, _, err := review.readNamespace(ctx, pending, 64<<10, 65)
	if err != nil {
		return err
	}
	if len(files) != 1 || files[0].Name != ".lock" {
		return fmt.Errorf("pending client evidence appeared; nothing may be reset")
	}
	return review.Validate(ctx)
}

func (r *SettledReview) namespacePresent(name string) bool {
	for _, n := range r.Namespaces {
		if n.Name == name && n.Present {
			return true
		}
	}
	return false
}
func (r *SettledReview) removeNamespace(name string) {
	kept := r.Namespaces[:0]
	for _, n := range r.Namespaces {
		if n.Name != name {
			kept = append(kept, n)
		}
	}
	r.Namespaces = kept
}
func (r *SettledReview) createLock(d *Directory, name string) error {
	f, v, err := d.Open(name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR)
	if err != nil {
		return fmt.Errorf("new coordination lock appeared or cannot be created: %w", err)
	}
	if err = errors.Join(f.Sync(), d.Sync()); err != nil {
		f.Close()
		return err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return err
	}
	named, err := d.Observe(name, false)
	if err != nil || !sameIdentity(v, named) {
		f.Close()
		return fmt.Errorf("new coordination lock was replaced")
	}
	r.locks = append(r.locks, f)
	full := filepath.Join(d.Path, name)
	r.removeNamespace(full)
	r.Namespaces = append(r.Namespaces, NamespaceEvidence{Name: full, Present: true, Identity: v, Files: []File{v}})
	return nil
}

// lockResetAuthority intentionally does not call Prepare/Offline: a fenced
// continuation may have already moved state.db. It creates no daemon lock.
func lockResetAuthority(d *Directory) (*os.File, error) {
	f, v, err := d.Open("daemon.lock", unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("existing private daemon authority lock required")
	}
	if v.Size != 0 {
		f.Close()
		return nil, fmt.Errorf("daemon lock must be empty")
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("stopped-exclusive authority required: %w", err)
	}
	named, err := d.Observe("daemon.lock", false)
	if err != nil || !sameIdentity(v, named) {
		f.Close()
		return nil, fmt.Errorf("daemon authority lock changed")
	}
	return f, nil
}

func unlockReset(f *os.File) error {
	if f == nil {
		return nil
	}
	return errors.Join(unix.Flock(int(f.Fd()), unix.LOCK_UN), f.Close())
}
