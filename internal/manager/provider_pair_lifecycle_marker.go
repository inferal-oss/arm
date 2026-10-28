package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"getarm.org/internal/providerpreparation"
)

// A staged value is data, not an ACK, eligible original or permission to clean
// up either member. The transaction owner must withhold publication on any
// commit error, including an unknown acknowledgement. No publisher exists here.
type providerPairMarkerStage struct {
	progress providerpreparation.Progress
	raw      []byte
	changed  bool
}

func providerPairLifecycleSHA(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// stageProviderPairRetentionLocked requires Manager.mu and a caller-owned
// writer transaction from this Manager. It grants only retention: even global
// census uncertainty must never become cleanup/admission/settlement credit.
// The complete strict C1 pair and known namespace are mandatory for a write.
// ANY refusal aborts the whole supplied transaction, including event failures.
// Neither this function nor its result commits or publishes a successor.
func (m *Manager) stageProviderPairRetentionLocked(ctx context.Context, tx *sql.Tx, expected providerPairDecoded, cause providerPairStopCause, at time.Time) (stage providerPairMarkerStage, err error) {
	defer func() {
		if err != nil {
			stage = providerPairMarkerStage{}
			if tx != nil {
				if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
					err = errors.Join(err, e)
				}
			}
		}
	}()
	if m == nil || ctx == nil || tx == nil || !providerPairHexID(m.host) || !providerPairUID(m.owner) || len(expected.rawInitial) == 0 || len(expected.rawProgress) == 0 {
		return stage, fmt.Errorf("bound retaining predecessor and writer transaction required")
	}
	r := providerPairC2Reader{ctx: ctx, tx: tx, host: m.host, owner: m.owner}
	present, err := r.namespace()
	if err != nil || !present {
		return stage, errors.Join(err, fmt.Errorf("known paired namespace required for retaining marker"))
	}
	// An event trigger must not turn this marker-only transaction into a
	// member rewrite or another indirect resource mutation. The paired
	// namespace checker already rejects triggers on its two tables. Reject
	// temporary shadows as well: all protected statements address main data.
	var indirect int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sqlite_schema WHERE type='trigger' AND tbl_name IN ('events','leases','lease_budget_bindings') UNION ALL SELECT 1 FROM sqlite_temp_schema WHERE (type='trigger' AND tbl_name IN ('events','leases','lease_budget_bindings','provider_preparations','provider_preparation_members')) OR name IN ('events','leases','lease_budget_bindings','provider_preparations','provider_preparation_members','metadata') LIMIT 1`).Scan(&indirect)
	if err == nil {
		return stage, fmt.Errorf("indirect lifecycle writes or temporary shadows are unsupported")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return stage, err
	}
	pairs, err := readProviderPairJournalSnapshot(ctx, tx, m.host, m.owner)
	if err != nil {
		return stage, err
	}
	var current *providerPairDecoded
	for i := range pairs {
		if pairs[i].initial.Pair == expected.initial.Pair {
			current = &pairs[i]
			break
		}
	}
	if current == nil || !bytes.Equal(current.rawInitial, expected.rawInitial) || !bytes.Equal(current.rawProgress, expected.rawProgress) {
		return stage, fmt.Errorf("retaining predecessor changed; no fresh row adopted")
	}
	if cause == providerPairExpired {
		// Sample AFTER the strict raw reads, under the same Manager fence. A
		// cancellation closure never depends on this positive-expiry proof.
		sample, e := m.readLeaseClock()
		if e != nil {
			return stage, e
		}
		expired := false
		for i := range current.clocks {
			remaining, e := current.clocks[i].Remaining(sample, &current.leases[i].Expires)
			if e != nil {
				return stage, e
			}
			expired = expired || remaining == 0
		}
		if !expired {
			return stage, fmt.Errorf("positive pair expiry has no fresh elapsed proof")
		}
	}
	next, changed, err := retainProviderPairProgress(current.progress, cause, at)
	if err != nil {
		return stage, err
	}
	if !changed {
		return providerPairMarkerStage{progress: current.progress, raw: bytes.Clone(current.rawProgress)}, ctx.Err()
	}
	raw, err := json.Marshal(next)
	if err != nil || len(raw) > providerpreparation.MaxProgressBytes {
		return stage, errors.Join(err, fmt.Errorf("bounded pair progress required"))
	}
	revision, err := providerPairDecimal(string(next.Revision), true)
	if err != nil {
		return stage, err
	}
	previousRevision, err := providerPairDecimal(string(current.progress.Revision), true)
	if err != nil {
		return stage, err
	}
	i := current.initial
	// TEXT parameters preserve SQLite's exact raw TEXT CAS. []byte would bind
	// BLOB and cannot substitute for the protected storage-class contract.
	result, err := tx.ExecContext(ctx, providerpreparation.ProgressCASSQL,
		string(next.Phase), string(raw), revision,
		string(i.Pair), string(i.OperationKey), string(i.Authority), string(i.Owner), i.Profile, 1, string(current.progress.Phase),
		string(i.Members[0].Row.ID), string(i.Members[1].Row.ID), providerpreparation.WorkerCPUMillis, providerpreparation.WorkerMemoryBytes, providerpreparation.TempDiskBytes,
		string(current.rawInitial), providerPairLifecycleSHA(current.rawInitial), string(current.rawProgress), previousRevision, providerpreparation.RecordReserveBytes)
	if err != nil {
		return stage, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return stage, errors.Join(err, fmt.Errorf("retaining CAS did not affect exactly one pair"))
	}
	event, err := json.Marshal(struct {
		Pair       providerpreparation.Hex32   `json:"pair"`
		Generation providerpreparation.Decimal `json:"generation"`
		Revision   providerpreparation.Decimal `json:"revision"`
		Phase      providerpreparation.Phase   `json:"phase"`
		Reason     string                      `json:"reason"`
	}{i.Pair, next.Generation, next.Revision, next.Phase, next.Reason})
	if err != nil || len(event) > providerpreparation.MaxEventBytes {
		return stage, errors.Join(err, fmt.Errorf("bounded pair retaining event required"))
	}
	eventResult, err := tx.ExecContext(ctx, `INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, "provider.preparation.retained", string(i.Pair), string(next.At), string(event))
	if err != nil {
		return stage, err
	}
	if n, err = eventResult.RowsAffected(); err != nil || n != 1 {
		return stage, errors.Join(err, fmt.Errorf("retaining event did not insert exactly one row"))
	}
	if err = ctx.Err(); err != nil {
		return stage, err
	}
	return providerPairMarkerStage{progress: next, raw: raw, changed: true}, nil
}
