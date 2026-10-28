package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/providerpreparation"
)

// These private types have no constructor, registry publisher or restoration
// path in this cut. The future A0 publisher must first atomically admit both
// members and strictly bind their raw bytes, typed clocks and budget ancestry,
// then publish this original pointer only after a positive commit ACK.
// No Manager field or production entrypoint constructs or invokes this core.
type providerPairCore struct {
	manager   *Manager
	epoch     providerpreparation.Hex32
	originals map[providerpreparation.Hex32]*providerPairOriginal
	fault     error
}

type providerPairOriginal struct {
	core             *providerPairCore
	id, epoch        providerpreparation.Hex32
	authority, owner string
	lifetime         context.Context
	rows             [2]providerPairLeaseRow // worker then temp; immutable copies
	clocks           [2]leaseclock.Deadline
	expires          [2]time.Time
}

// A member observation is deliberately not providerexec.EntitlementCheck,
// Prepared, native evidence, or settlement. The complete journal/header/body,
// membership, generation/progress CAS and global accounting checks are still
// mandatory before a future trusted checker may consume a remaining duration.
type providerPairMemberObservation struct {
	original  *providerPairOriginal
	sample    leaseclock.Sample
	remaining time.Duration
}

// lockProviderPairMembers uses the real Manager fence, with cancellable entry.
// The timer bounds read latency only; it never calculates or extends a lease.
func (m *Manager) lockProviderPairMembers(ctx context.Context) error {
	if m == nil || ctx == nil {
		return fmt.Errorf("original manager and caller context required")
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.mu.TryLock() {
			if err := ctx.Err(); err != nil {
				m.mu.Unlock()
				return err
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// observeProviderPairMembers is an unused prerequisite reader, not an eligible
// pair checker. It neither writes SQL nor creates or re-registers an owner.
// Raw reads precede one fresh sticky Manager clock sample under the same fence;
// no wall subtraction, cached remaining lifetime or timer rebase is used.
func (m *Manager) observeProviderPairMembers(ctx context.Context, c *providerPairCore, original *providerPairOriginal) (result providerPairMemberObservation, resultErr error) {
	if ctx == nil {
		return result, fmt.Errorf("caller context required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if original != nil && original.lifetime != nil {
		stop := context.AfterFunc(original.lifetime, cancel)
		defer stop()
	}
	if err := m.lockProviderPairMembers(ctx); err != nil {
		return result, err
	}
	defer m.mu.Unlock()
	if c == nil || original == nil || m.db == nil || c.manager != m || original.core != c || !providerPairHexID(string(c.epoch)) || original.epoch != c.epoch || !providerPairHexID(string(original.id)) || c.originals[original.id] != original || original.authority != m.host || original.owner != m.owner || original.lifetime == nil || c.fault != nil || m.closing || m.draining {
		return result, fmt.Errorf("original pair owner/open epoch unavailable")
	}
	if err := original.lifetime.Err(); err != nil {
		return result, err
	}
	if !original.rows[0].bounded() || !original.rows[1].bounded() || original.rows[0].id == original.rows[1].id || original.rows[0].requestKey == original.rows[1].requestKey {
		return result, fmt.Errorf("paired members must have bounded, distinct original SQL identities and keys")
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			result, resultErr = providerPairMemberObservation{}, errors.Join(resultErr, err)
		}
		if err := ctx.Err(); err != nil {
			result, resultErr = providerPairMemberObservation{}, errors.Join(resultErr, err)
		}
		if err := original.lifetime.Err(); err != nil {
			result, resultErr = providerPairMemberObservation{}, errors.Join(resultErr, err)
		}
	}()
	for _, saved := range original.rows {
		row, err := readProviderPairLeaseRow(ctx, tx, saved.id)
		if err != nil {
			return result, err
		}
		if !row.same(saved) {
			return result, fmt.Errorf("same-revision replacement or raw member binding change")
		}
	}
	sample, err := m.readLeaseClock()
	if err != nil {
		return result, err
	}
	remaining := time.Duration(providerpreparation.LifetimeNS)
	for i := range original.clocks {
		d := &original.clocks[i]
		if d.Anchor < 0 || d.End <= d.Anchor || d.End-d.Anchor != providerpreparation.LifetimeNS {
			return result, fmt.Errorf("original member clock differs from inert profile lifetime")
		}
		n, err := d.Remaining(sample, &original.expires[i])
		if err != nil || n <= 0 {
			return result, fmt.Errorf("original native member entitlement unavailable")
		}
		if n < remaining {
			remaining = n
		}
	}
	return providerPairMemberObservation{original: original, sample: sample, remaining: remaining}, nil
}
