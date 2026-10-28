package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Interception data, never ordinary permission or positive availability. Even
// known pairs may be retained on a globally unknown census; no caller may turn
// a missing lookup plus problem into an ordinary cleanup/admission path.
type providerPairCustodySnapshot struct {
	projection     providerPairC2Projection
	pairs          map[string]providerPairDecoded
	byLease, byKey map[string]string
	problem        error
}

func readProviderPairCustody(ctx context.Context, tx *sql.Tx, host, owner string) (s providerPairCustodySnapshot) {
	defer func() {
		if s.problem != nil {
			s.projection.state = "unknown"
		}
	}()
	s.pairs, s.byLease, s.byKey = map[string]providerPairDecoded{}, map[string]string{}, map[string]string{}
	s.projection.state = "unknown"
	if ctx == nil || tx == nil || !providerPairHexID(host) || !providerPairUID(owner) {
		s.problem = fmt.Errorf("custody read snapshot/authority required")
		return s
	}
	s.projection, s.problem = readProviderPairInventory(ctx, tx, host, owner)
	r := providerPairC2Reader{ctx: ctx, tx: tx, host: host, owner: owner}
	present, err := r.namespace()
	if err != nil {
		s.problem = errors.Join(s.problem, err)
		return s
	}
	if !present {
		return s // C2 has still checked ordinary/orphan evidence
	}
	pairs, err := readProviderPairJournalSnapshot(ctx, tx, host, owner)
	if err != nil {
		s.problem = errors.Join(s.problem, err)
		return s
	}
	for _, pair := range pairs {
		id := string(pair.initial.Pair)
		s.pairs[id] = pair
		for _, row := range pair.rows {
			if prior := s.byLease[row.id]; prior != "" && prior != id {
				s.problem = errors.Join(s.problem, fmt.Errorf("ambiguous pair member custody"))
			}
			s.byLease[row.id] = id
		}
		for _, key := range []string{string(pair.initial.OperationKey), pair.rows[0].requestKey, pair.rows[1].requestKey} {
			if prior := s.byKey[key]; prior != "" && prior != id {
				s.problem = errors.Join(s.problem, fmt.Errorf("ambiguous pair request-key custody"))
			}
			s.byKey[key] = id
		}
	}
	if err := ctx.Err(); err != nil {
		s.problem = errors.Join(s.problem, err)
	}
	return s
}

// selected returns retaining data only. On an error, zero matches is NOT proof
// of unrelated ordinary ownership. Callers must fence unsafe resource effects.
func (s providerPairCustodySnapshot) selected(selection providerPairLifecycleSelection) (map[string]providerPairDecoded, error) {
	if !selection.valid() {
		return nil, fmt.Errorf("bounded lifecycle selection required")
	}
	if s.problem != nil || s.projection.state != "covered" {
		return nil, errors.Join(s.problem, fmt.Errorf("unknown global custody; no ordinary fallback"))
	}
	result := map[string]providerPairDecoded{}
	if selection.all {
		for id, pair := range s.pairs {
			result[id] = pair
		}
	} else {
		for _, target := range selection.targets {
			id := s.byLease[target.leaseID]
			if target.requestKey != "" {
				id = s.byKey[target.requestKey]
			}
			if id != "" {
				result[id] = s.pairs[id]
			}
		}
	}
	return result, nil
}

// cancelProviderPairCustody is a private, unwired retaining-intent adapter. The
// lock-held capture/read helpers are separate for future existing-lock hooks.
// Its only native-facing action is dispatch of captured original cancellation
// after rollback/unlock, including on SQL/census failure. It never joins work.
func (m *Manager) cancelProviderPairCustody(ctx context.Context, scope *providerPairLifecycleScope, selection providerPairLifecycleSelection) (s providerPairCustodySnapshot, err error) {
	s.projection.state = "unknown"
	if ctx == nil {
		return s, fmt.Errorf("caller context required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = m.lockProviderPairMembers(ctx); err != nil {
		return s, err
	}
	batch, captureErr := m.captureProviderPairCancellationsLocked(scope, selection)
	defer func() {
		m.mu.Unlock()
		err = errors.Join(err, captureErr, batch.dispatch())
		if err != nil {
			s.problem = errors.Join(s.problem, err)
			s.projection.state = "unknown"
		}
	}()
	if m.db == nil {
		return s, fmt.Errorf("custody database unavailable; original cancellations retained")
	}
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return s, e
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, e)
		}
		err = errors.Join(err, ctx.Err())
	}()
	s = readProviderPairCustody(ctx, tx, m.host, m.owner)
	return s, s.problem
}
