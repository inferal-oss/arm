package syncscope

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

// Identity names the original in-process preparation owner. A recovered SQL
// row cannot supply Live, which must be the exact original owner's context.
type Identity struct {
	Key, Side, LeaseID, Host, Owner string
}

// Record is copied durable evidence. The mutable row state is intentionally
// fixed at preparing in this first bridge; it cannot authorize handoff.
type Record struct {
	Identity
	Provider, ScopeID, MountID, ProjectRootID, SelectedRootID, HomeRootID string
	EnrollmentDigest, OwnershipDigest                                     string
	ProjectID, OwnerEpoch, ChargedHard, InitialUsed, RowRevision          int64
	PreparationRevision                                                   int64
	PreparationData                                                       string
	State                                                                 string
}

// Original binds one exact live owner, the sole Manager database pool, its
// mutex, and the shared native gate. This object cannot be reconstructed from
// a row after Manager restart. It has no cleanup or final-lease authority.
type Original struct {
	db        *sql.DB
	managerMu *sync.Mutex
	gate      *Gate
	live      context.Context
	clockRead func() (leaseclock.Sample, error)
	record    Record
	revoked   atomic.Bool
}

var ErrOriginal = errors.New("original charged sync owner unavailable")

// BindOriginal is called only after the original lease and preparing row
// commit, under Manager's owner registration. The same lease/row is checked
// again on every held verification; the first read is not a cached proof.
func BindOriginal(ctx context.Context, db *sql.DB, managerMu *sync.Mutex, gate *Gate, live context.Context, clockRead func() (leaseclock.Sample, error), id Identity) (*Original, error) {
	if ctx == nil || db == nil || managerMu == nil || gate == nil || live == nil || clockRead == nil ||
		id.Key == "" || (id.Side != "local" && id.Side != "remote") || id.LeaseID == "" || id.Host == "" || id.Owner == "" ||
		ctx.Err() != nil || live.Err() != nil {
		return nil, ErrOriginal
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if !managerMu.TryLock() {
		return nil, ErrOriginal
	}
	defer managerMu.Unlock()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ErrOriginal
	}
	defer tx.Rollback()
	if err := checkScopeCount(ctx, tx); err != nil {
		return nil, err
	}
	r, err := readRecord(ctx, tx, id)
	if err != nil {
		return nil, ErrOriginal
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrOriginal
	}
	o := &Original{db: db, managerMu: managerMu, gate: gate, live: live, clockRead: clockRead, record: r}
	if err := o.checkRecord(ctx, conn); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Original) Record() Record {
	if o == nil {
		return Record{}
	}
	return o.record
}

// Revoke permanently removes in-process verification authority after an
// uncertain native effect. It cannot be rebuilt from the durable row.
func (o *Original) Revoke() {
	if o != nil {
		o.revoked.Store(true)
	}
}

// TryHold acquires the sole SQLite connection first, then tries the native
// gate and Manager.mu without waiting. SQL is read through the held connection
// throughout the native scan, so ordinary SQLite/WAL writers cannot pass it.
func (o *Original) TryHold(ctx context.Context) (*Hold, error) {
	if o == nil || ctx == nil || ctx.Err() != nil || o.live.Err() != nil || o.revoked.Load() {
		return nil, ErrOriginal
	}
	conn, err := o.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	verifier, err := o.gate.TryVerify(ctx)
	if err != nil {
		conn.Close()
		return nil, ErrOriginal
	}
	h := &Hold{original: o, conn: conn, verifier: verifier}
	if err := h.Check(ctx); err != nil {
		h.Release()
		return nil, err
	}
	return h, nil
}

// HoldWriter rechecks the exact current original under a fresh writer token
// and retains the sole SQL connection through its following native effect.
// The caller also rechecks held roots and native quota before the effect.
func (o *Original) HoldWriter(ctx context.Context, writer *Writer) (*WriterHold, error) {
	if o == nil || ctx == nil || ctx.Err() != nil || writer == nil || writer.gate != o.gate || writer.released.Load() || o.live.Err() != nil || o.revoked.Load() {
		return nil, ErrOriginal
	}
	conn, err := o.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	h := &WriterHold{original: o, conn: conn, writer: writer}
	if err := h.Check(ctx); err != nil {
		h.Release()
		return nil, err
	}
	return h, nil
}

type WriterHold struct {
	original *Original
	conn     *sql.Conn
	writer   *Writer
	mu       sync.Mutex
	released bool
}

func (h *WriterHold) Check(ctx context.Context) error {
	if h == nil || h.original == nil || ctx == nil || ctx.Err() != nil {
		return ErrOriginal
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released || h.conn == nil || h.writer == nil || h.writer.released.Load() ||
		h.original.live.Err() != nil || !h.original.managerMu.TryLock() {
		return ErrOriginal
	}
	defer h.original.managerMu.Unlock()
	return h.original.checkRecord(ctx, h.conn)
}

// Release closes the database exclusion. The caller then releases its writer
// gate after all native workers and any result handling have joined.
func (h *WriterHold) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.released {
		h.released = true
		if h.conn != nil {
			h.conn.Close()
		}
	}
}

// Hold keeps the original gate and sole connection across native verification.
// Check may be repeated after the scan; Release is idempotent.
type Hold struct {
	original *Original
	conn     *sql.Conn
	verifier *Verifier
	mu       sync.Mutex
	released bool
}

func (h *Hold) Verifier() *Verifier {
	if h == nil {
		return nil
	}
	return h.verifier
}

func (h *Hold) Check(ctx context.Context) error {
	if h == nil || h.original == nil || ctx == nil || ctx.Err() != nil {
		return ErrOriginal
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released || h.conn == nil || h.original.live.Err() != nil || !h.original.managerMu.TryLock() {
		return ErrOriginal
	}
	defer h.original.managerMu.Unlock()
	return h.original.checkRecord(ctx, h.conn)
}

func (h *Hold) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.released {
		return
	}
	h.released = true
	if h.verifier != nil {
		h.verifier.Release()
	}
	if h.conn != nil {
		h.conn.Close()
	}
}

func (o *Original) checkRecord(ctx context.Context, conn *sql.Conn) error {
	if ctx.Err() != nil || o.live.Err() != nil || o.revoked.Load() {
		return ErrOriginal
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ErrOriginal
	}
	defer tx.Rollback()
	if err := checkScopeCount(ctx, tx); err != nil {
		return err
	}
	current, err := readRecord(ctx, tx, o.record.Identity)
	if err != nil || current != o.record || current.State != "preparing" || current.RowRevision != 1 ||
		current.Provider != "xfs-project-quota" || current.ChargedHard <= 0 || current.InitialUsed < 0 || current.InitialUsed > current.ChargedHard {
		return ErrOriginal
	}
	var raw, physicalKey string
	var runNull int
	query := "SELECT " + boundedText("data", 65536) + "," + boundedText("request_key", 512) + ",CASE WHEN run IS NULL THEN 1 ELSE 0 END FROM leases WHERE id=?"
	if err := tx.QueryRowContext(ctx, query, current.LeaseID).Scan(&raw, &physicalKey, &runNull); err != nil ||
		runNull != 1 || raw != current.PreparationData || physicalKey != mesh.Key(current.Host, "sync-preparation\x00"+current.Side+"\x00"+current.Key) {
		return ErrOriginal
	}
	var lease model.Lease
	if json.Unmarshal([]byte(raw), &lease) != nil || lease.ID != current.LeaseID || lease.Host != current.Host || lease.Owner != current.Owner ||
		lease.Revision != int(current.PreparationRevision) || lease.State != "active" ||
		lease.PID != 0 || lease.Process != nil ||
		lease.Request.Kind != "sync-preparation" || lease.Request.Host != current.Host ||
		lease.Request.Key != physicalKey ||
		lease.Request.Resources.Disk != current.ChargedHard || lease.Clock == nil || lease.Expires == nil {
		return ErrOriginal
	}
	canonical, err := json.Marshal(lease)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return ErrOriginal
	}
	sample, err := o.clockRead()
	if err != nil {
		return ErrOriginal
	}
	remaining, err := lease.Clock.Remaining(sample, lease.Expires)
	if err != nil || remaining <= 0 || remaining > 7*24*time.Hour {
		return ErrOriginal
	}
	if err := tx.Commit(); err != nil {
		return ErrOriginal
	}
	return nil
}

func checkScopeCount(ctx context.Context, tx *sql.Tx) error {
	var operations, scopes int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_charged_scopes)`).Scan(&operations, &scopes); err != nil ||
		operations != 0 || scopes != 1 {
		return ErrOriginal
	}
	return nil
}

func readRecord(ctx context.Context, tx *sql.Tx, id Identity) (Record, error) {
	var r Record
	var finalNull int
	columns := []string{
		boundedText("key", 128), boundedText("side", 6), boundedText("preparation_lease", 256),
		boundedText("host", 256), boundedText("owner", 256), boundedText("provider", 32),
		boundedText("scope_id", 512), boundedText("mount_id", 512),
		boundedText("project_root_id", 512), boundedText("selected_root_id", 512), boundedText("home_root_id", 512),
		boundedText("enrollment_digest", 64), boundedText("ownership_digest", 64),
		integerCell("project_id"), integerCell("owner_epoch"), integerCell("charged_hard_bytes"), integerCell("initial_used_bytes"),
		integerCell("row_revision"), integerCell("preparation_revision"), boundedText("preparation_data", 65536),
		boundedText("state", 32), "CASE WHEN final_lease IS NULL THEN 1 ELSE 0 END",
	}
	query := "SELECT " + strings.Join(columns, ",") + " FROM sync_charged_scopes WHERE key=? AND side=?"
	err := tx.QueryRowContext(ctx, query, id.Key, id.Side).Scan(
		&r.Key, &r.Side, &r.LeaseID, &r.Host, &r.Owner, &r.Provider, &r.ScopeID, &r.MountID, &r.ProjectRootID, &r.SelectedRootID, &r.HomeRootID,
		&r.EnrollmentDigest, &r.OwnershipDigest, &r.ProjectID, &r.OwnerEpoch, &r.ChargedHard, &r.InitialUsed, &r.RowRevision, &r.PreparationRevision, &r.PreparationData, &r.State, &finalNull)
	if err != nil || r.Identity != id || finalNull != 1 || r.Provider != "xfs-project-quota" ||
		r.ProjectID <= 0 || r.ProjectID > math.MaxUint32 || r.OwnerEpoch <= 0 ||
		r.ChargedHard <= 0 || r.InitialUsed < 0 || r.InitialUsed > r.ChargedHard ||
		r.RowRevision != 1 || r.PreparationRevision <= 0 || r.State != "preparing" {
		return Record{}, ErrOriginal
	}
	return r, nil
}

func boundedText(column string, max int) string {
	return fmt.Sprintf("CASE WHEN typeof(%s)='text' AND length(CAST(%s AS BLOB)) BETWEEN 1 AND %d THEN %s END", column, column, max, column)
}

func integerCell(column string) string {
	return fmt.Sprintf("CASE WHEN typeof(%s)='integer' THEN %s END", column, column)
}
