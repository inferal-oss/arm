package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// C3 private source primitive, not a registry publisher. C4 must enroll a fully
// bound original duty before its admission commit and publish eligibility only
// after positive ACK. Pending/unknown-ACK duties can be cancelled without ever
// appearing in core.originals. Nothing decodes or reconstructs these entries.
type providerPairCancellationState uint8

const (
	providerPairCancellationPending providerPairCancellationState = iota + 1
	providerPairCancellationUnknownACK
	providerPairCancellationAdmitted
)

type providerPairCancellationEntry struct {
	original     *providerPairOriginal
	operationKey string
	state        providerPairCancellationState
	cancel       context.CancelFunc
	once         sync.Once // belongs to this entry; distinct originals cannot alias it
}

type providerPairLifecycleScope struct {
	core   *providerPairCore
	duties map[*providerPairOriginal]*providerPairCancellationEntry
}

type providerPairLifecycleTarget struct{ leaseID, requestKey string }
type providerPairLifecycleSelection struct {
	all     bool // accepted operation scope, never evidence/permission
	targets []providerPairLifecycleTarget
}

func (s providerPairLifecycleSelection) valid() bool {
	if s.all {
		return len(s.targets) == 0
	}
	if len(s.targets) == 0 || len(s.targets) > providerPairC2Rows {
		return false
	}
	for _, t := range s.targets {
		if t.leaseID != "" {
			if t.requestKey != "" || !providerPairHexID(t.leaseID) {
				return false
			}
		} else if !providerPairRequestKey(t.requestKey) {
			return false
		}
	}
	return true
}

func (s providerPairLifecycleSelection) matches(original *providerPairOriginal, operationKey string) bool {
	if s.all {
		return true
	}
	for _, target := range s.targets {
		if target.requestKey != "" && target.requestKey == operationKey {
			return true
		}
		for _, row := range original.rows {
			if target.leaseID != "" && target.leaseID == row.id || target.requestKey != "" && target.requestKey == row.requestKey {
				return true
			}
		}
	}
	return false
}

// The batch retains only the exact original closures and once identities. It
// performs no registry/SQL/PID lookup after unlock and contains no join authority.
type providerPairCancellationAction struct {
	cancel context.CancelFunc
	once   *sync.Once
}
type providerPairCancellationBatch struct {
	actions []providerPairCancellationAction
}

// captureProviderPairCancellationsLocked requires Manager.mu. It must precede
// SQL. A core fault, cancelled lifetime or one malformed duty must not suppress
// other valid registered original cancellations. The private duty map's storage
// bounds/prepayment belong to C4; it is not allocated from a wire inventory.
func (m *Manager) captureProviderPairCancellationsLocked(scope *providerPairLifecycleScope, selection providerPairLifecycleSelection) (batch providerPairCancellationBatch, err error) {
	if m == nil || !selection.valid() {
		return batch, fmt.Errorf("bounded accepted lifecycle selection required")
	}
	if scope == nil {
		return batch, nil // no native owner is inferred from a journal
	}
	c := scope.core
	if c == nil || c.manager != m || !providerPairHexID(string(c.epoch)) {
		return batch, fmt.Errorf("original lifecycle Manager/open epoch unavailable")
	}
	err = c.fault // cancellation is still required through a bookkeeping fault
	for original, entry := range scope.duties {
		if original == nil || entry == nil || entry.original != original || original.core != c || original.epoch != c.epoch || !providerPairHexID(string(original.id)) || original.authority != m.host || original.owner != m.owner || original.lifetime == nil || !providerPairRequestKey(entry.operationKey) || entry.cancel == nil || !original.rows[0].bounded() || !original.rows[1].bounded() || original.rows[0].id == original.rows[1].id || original.rows[0].requestKey == original.rows[1].requestKey {
			err = errors.Join(err, fmt.Errorf("malformed original cancellation duty; no callback adopted"))
			continue
		}
		registered := c.originals[original.id]
		switch entry.state {
		case providerPairCancellationPending, providerPairCancellationUnknownACK:
			if registered != nil {
				err = errors.Join(err, fmt.Errorf("pending duty cannot borrow an eligible original"))
				continue
			}
		case providerPairCancellationAdmitted:
			if registered != original {
				err = errors.Join(err, fmt.Errorf("original cancellation registration changed"))
				continue
			}
		default:
			err = errors.Join(err, fmt.Errorf("unknown original cancellation state"))
			continue
		}
		if selection.matches(original, entry.operationKey) {
			batch.actions = append(batch.actions, providerPairCancellationAction{cancel: entry.cancel, once: &entry.once})
		}
	}
	return batch, err
}

// dispatch runs AFTER releasing Manager.mu. Successful return means only that
// these original cancellation closures returned, not native join or any credit.
// A defective trusted closure does not starve the other captured originals.
func (b providerPairCancellationBatch) dispatch() (err error) {
	for _, action := range b.actions {
		func() {
			defer func() {
				if recover() != nil {
					err = errors.Join(err, fmt.Errorf("original cancellation panicked; custody remains retained"))
				}
			}()
			action.once.Do(action.cancel)
		}()
	}
	return err
}
