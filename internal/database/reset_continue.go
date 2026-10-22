package database

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ResetContinuation acquires existing authority and namespace locks, reads the
// exact retained intent and performs NO recovery until Continue is called after
// explicit review/confirmation. Restoring an intent never automatically erases.
type ResetContinuation struct {
	Receipt ResetReceipt `json:"receipt"`
	store   *resetStore
	file    File
	locks   []*os.File
}

func (c *ResetContinuation) Close() error {
	if c == nil {
		return nil
	}
	var result error
	for i := len(c.locks) - 1; i >= 0; i-- {
		if err := unlockReset(c.locks[i]); err != nil {
			result = err
		}
	}
	c.locks = nil
	if c.store != nil {
		if err := c.store.close(); err != nil {
			result = err
		}
		c.store = nil
	}
	return result
}

func ReviewResetContinuation(ctx context.Context, home, id string) (c *ResetContinuation, err error) {
	s, err := openResetStore(home, id, false)
	if err != nil {
		return nil, err
	}
	c = &ResetContinuation{store: s}
	opened := c
	defer func() {
		if err != nil {
			opened.Close()
		}
	}()
	lock, err := lockResetAuthority(s.home)
	if err != nil {
		return nil, err
	}
	c.locks = append(c.locks, lock)
	c.Receipt, c.file, err = s.readReceipt()
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// A completed receipt without a fence is immutable history; returning it
	// does not replay prior deletion or require revalidating later artifacts.
	if c.Receipt.Phase == "completed" {
		if _, _, e := readResetJSON[resetFence](s.home, "database-reset-pending.json"); os.IsNotExist(e) {
			return c, nil
		}
	}
	locks, err := s.lockProtectedNamespaces(c.Receipt)
	if err != nil {
		return nil, err
	}
	c.locks = append(c.locks, locks...)
	if err = s.validateResetNamespaces(ctx, c.Receipt); err != nil {
		return nil, err
	}
	if err = s.validateResetProgress(ctx, c.Receipt); err != nil {
		return nil, err
	}
	names, err := resetNames(s.container, maxResetReceipts)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if name == "reset-"+id {
			continue
		}
		if !strings.HasPrefix(name, "reset-") || !validHost(strings.TrimPrefix(name, "reset-")) {
			return nil, fmt.Errorf("unfamiliar reset history retained")
		}
		d, e := resetChild(s.container, name, false)
		if e != nil {
			return nil, e
		}
		other := &resetStore{home: s.home, container: s.container, operation: d}
		r, _, e := other.readReceipt()
		d.Close()
		if e != nil {
			return nil, e
		}
		if r.Phase != "completed" {
			return nil, fmt.Errorf("another incomplete reset receipt must be inspected")
		}
	}
	return c, nil
}

func (c *ResetContinuation) Continue(ctx context.Context) (ResetResult, error) {
	return c.continueReset(ctx, nil)
}

func (c *ResetContinuation) continueReset(ctx context.Context, hook func(string) error) (ResetResult, error) {
	if c == nil || c.store == nil {
		return ResetResult{}, fmt.Errorf("live stopped reset continuation required")
	}
	r := &c.Receipt
	if r.Phase == "completed" {
		if _, _, err := readResetJSON[resetFence](c.store.home, "database-reset-pending.json"); os.IsNotExist(err) {
			if err = c.store.validateCompletedResetPointer(ctx, *r); err != nil {
				return resetResult(*r), err
			}
			if err = c.store.validate(); err != nil {
				return resetResult(*r), err
			}
			if err = c.store.home.Sync(); err != nil {
				return resetResult(*r), err
			}
			return resetCompletedResult(*r), nil
		}
	}
	if r.Phase == "staged" {
		for _, v := range r.Plan.Files {
			if _, err := exactResetFile(ctx, c.store.home, v, true); err != nil {
				return resetResult(*r), err
			}
		}
		if err := c.store.buildResetSeed(ctx, r, &c.file); err != nil {
			return resetResult(*r), err
		}
	}
	return c.store.finishReset(ctx, r, &c.file, hook)
}

func (s *resetStore) validateCompletedResetPointer(ctx context.Context, r ResetReceipt) error {
	seed, _, err := readResetJSON[ResetSeed](s.home, ResetSeedName)
	if err != nil {
		return fmt.Errorf("retained completed reset lost its exact authority pointer; preserve evidence and restore the reviewed pointer explicitly, no authority was initialized: %w", err)
	}
	if err = validSeedReceipt(seed, r); err != nil {
		return err
	}
	if seed.Installed {
		f, _, err := s.home.openSource("state.db")
		if err != nil {
			return err
		}
		if f == nil {
			return fmt.Errorf("consumed authority pointer cannot recreate a missing established database")
		}
		return f.Close()
	}
	if _, err = exactResetFile(ctx, s.operation, r.Seed, false); err != nil {
		return err
	}
	return s.validateResetFileLocations(ctx, r)
}
