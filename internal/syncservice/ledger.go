package syncservice

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncfs"
	"github.com/yrashk/arm/internal/syncreconcile"
)

// These tables belong to the Manager's proven state schema. The Manager must
// install and prove them, and must reserve destination sync intent in the same
// transaction that admits the exact leased SSH execution. No lazy schema
// creation is permitted after an operation namespace has been used.
const OperationsSchema = `CREATE TABLE sync_operations (key TEXT NOT NULL,side TEXT NOT NULL,pair TEXT NOT NULL,generation INTEGER NOT NULL,record BLOB NOT NULL,record_sha256 TEXT NOT NULL,manifest BLOB NOT NULL,state TEXT NOT NULL,local_receipt BLOB,remote_receipt BLOB,nonexecution BLOB,PRIMARY KEY(key,side))`
const OpenGenerationSchema = `CREATE UNIQUE INDEX sync_open_generation ON sync_operations(pair,side,generation) WHERE state!='sealed'`
const EffectsSchema = `CREATE TABLE sync_effects (key TEXT NOT NULL,owner_side TEXT NOT NULL,effect_side TEXT NOT NULL,idx INTEGER NOT NULL,prepared BLOB NOT NULL,outcome BLOB,PRIMARY KEY(key,owner_side,effect_side,idx))`
const BaselinesSchema = `CREATE TABLE sync_baselines (pair TEXT PRIMARY KEY,generation INTEGER NOT NULL,seal TEXT NOT NULL,record BLOB NOT NULL)`

func InstallSchemaTx(ctx context.Context, tx *sql.Tx) error {
	if err := durableTx(ctx, tx); err != nil {
		return err
	}
	for _, q := range []string{OperationsSchema, OpenGenerationSchema, EffectsSchema, BaselinesSchema} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// ReserveTx is the sole dispatch gate. A returned error, including a commit
// uncertainty in the caller's transaction, never authorizes SSH dispatch.
func ReserveTx(ctx context.Context, tx *sql.Tx, i Intent, rows []ManifestRow, observed syncreconcile.Snapshot) error {
	if err := durableTx(ctx, tx); err != nil {
		return err
	}
	if err := i.Validate(); err != nil {
		return err
	}
	var existing int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sync_operations WHERE key=? AND side=?`, i.Key, i.Side).Scan(&existing)
	if err == nil {
		return ErrKeyExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	manifestRaw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if len(manifestRaw) > MaxManifestBytes || sha(manifestRaw) != i.ManifestDigest {
		return errors.New("selected manifest does not match reviewed digest")
	}
	if err := validateManifest(i, rows); err != nil {
		return err
	}
	if err := validateOwnObservation(i.Side, rows, observed); err != nil {
		return err
	}
	baseline, err := currentBaselineTx(ctx, tx, i.Binding, i.Side)
	if err != nil {
		return err
	}
	if _, err = ReconcileIntent(i, rows, baseline); err != nil {
		return err
	}
	var priorKey string
	err = tx.QueryRowContext(ctx, `SELECT key FROM sync_operations WHERE pair=? AND side=? AND generation=? AND state!='sealed'`, pairID(i.Binding), i.Side, i.Generation).Scan(&priorKey)
	if err == nil {
		return fmt.Errorf("%w: generation already reserved under original key %q", ErrUnknown, priorKey)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	record, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if err = validateFootprint(i, len(manifestRaw), len(record)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_operations(key,side,pair,generation,record,record_sha256,manifest,state) VALUES(?,?,?,?,?,?,?,?)`, i.Key, i.Side, pairID(i.Binding), i.Generation, record, sha(record), manifestRaw, "reserved")
	if err != nil {
		if _, lookupErr := loadTx(ctx, tx, i.Key, i.Side); lookupErr == nil {
			return ErrKeyExists
		}
		return err
	}
	return nil
}

func validateOwnObservation(side syncreconcile.Side, rows []ManifestRow, observed syncreconcile.Snapshot) error {
	want := syncreconcile.Snapshot{Entries: map[string]syncreconcile.Entry{}, NameKeys: map[string]string{}}
	for _, row := range rows {
		if row.Side != side {
			continue
		}
		want.NameKeys[row.Path] = row.NameKey
		if row.Entry != nil {
			want.Entries[row.Path] = *row.Entry
		}
	}
	if !reflect.DeepEqual(want, observed) {
		return errors.New("held-root selected manifest or native name keys changed")
	}
	return nil
}

func validateManifest(i Intent, rows []ManifestRow) error {
	if len(rows) > syncreconcile.MaxEntries*6 {
		return errors.New("manifest row count exceeds limit")
	}
	seen := make(map[string]ManifestRow, len(rows))
	last := ""
	for n, r := range rows {
		if r.Side != syncreconcile.Local && r.Side != syncreconcile.Remote || !manifestPath(r.Path) || !manifestPath(r.NameKey) {
			return errors.New("invalid manifest row")
		}
		key := string(r.Side) + "\x00" + r.Path
		if n > 0 && key <= last {
			return errors.New("manifest rows are not strictly sorted")
		}
		last = key
		seen[key] = r
	}
	for _, a := range i.Actions {
		target, ok := seen[string(a.Side)+"\x00"+a.Path]
		if !ok || !reflect.DeepEqual(target.Entry, a.Before) {
			return fmt.Errorf("reviewed receiver precondition absent from manifest at %q", a.Path)
		}
		if a.Kind != syncreconcile.Remove {
			source, ok := seen[string(a.Source)+"\x00"+a.SourcePath]
			if !ok || !reflect.DeepEqual(source.Entry, a.After) {
				return fmt.Errorf("reviewed source absent from manifest at %q", a.SourcePath)
			}
		}
	}
	return nil
}

func manifestPath(p string) bool {
	if p == "" || len(p) > syncreconcile.MaxPathBytes || !utf8.ValidString(p) || strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	parts := strings.Split(p, "/")
	if len(parts) > syncreconcile.MaxDepth {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > syncreconcile.MaxComponentBytes {
			return false
		}
	}
	return true
}

// Ledger is bound to one endpoint's original operation key. Its Prepare and
// Outcome methods implement syncfs.Journal with committed SQLite transactions.
// Manager must use a durable SQLite connection and rollback/WAL journal.
type Ledger struct {
	DB      *sql.DB
	Key     string
	Side    syncreconcile.Side
	cacheMu sync.Mutex
	cache   *effectIntent
}

// A journal handle is bound to an immutable reviewed intent. After its first
// verified read, effect writes only need to recheck the operation's identity
// and state. Full evidence is reread by Lookup, completion, and confirmation.
type effectIntent struct {
	db           *sql.DB
	key          string
	side         syncreconcile.Side
	intent       Intent
	actions      []syncreconcile.Action
	recordDigest string
}

var _ syncfs.Journal = (*Ledger)(nil)

type Lookup struct {
	Intent        Intent
	Manifest      []ManifestRow
	State         string
	Effects       []syncfs.Receipt
	LocalReceipt  *SideReceipt
	RemoteReceipt *SideReceipt
	Nonexecution  *model.SSHKeyFence
}

func (l *Ledger) valid() error {
	if l == nil || l.DB == nil || !validKey(l.Key) || l.Side != syncreconcile.Local && l.Side != syncreconcile.Remote {
		return errors.New("invalid sync ledger handle")
	}
	return nil
}

func decodeOperation(record, manifest []byte, digest, state string, localRaw, remoteRaw, sealRaw []byte) (Lookup, error) {
	if len(record) > MaxRecordBytes || len(manifest) > MaxManifestBytes || sha(record) != digest {
		return Lookup{}, errors.New("sync operation evidence missing or corrupt")
	}
	var out Lookup
	if err := json.Unmarshal(record, &out.Intent); err != nil {
		return Lookup{}, err
	}
	if err := out.Intent.Validate(); err != nil {
		return Lookup{}, err
	}
	if err := json.Unmarshal(manifest, &out.Manifest); err != nil {
		return Lookup{}, err
	}
	if sha(manifest) != out.Intent.ManifestDigest {
		return Lookup{}, errors.New("sync selected manifest changed")
	}
	if err := validateManifest(out.Intent, out.Manifest); err != nil {
		return Lookup{}, err
	}
	out.State = state
	if len(localRaw) > 0 {
		var r SideReceipt
		if err := json.Unmarshal(localRaw, &r); err != nil {
			return Lookup{}, err
		}
		if !r.matches(out.Intent, syncreconcile.Local) {
			return Lookup{}, errors.New("local receipt differs from operation")
		}
		out.LocalReceipt = &r
	}
	if len(remoteRaw) > 0 {
		var r SideReceipt
		if err := json.Unmarshal(remoteRaw, &r); err != nil {
			return Lookup{}, err
		}
		if !r.matches(out.Intent, syncreconcile.Remote) {
			return Lookup{}, errors.New("remote receipt differs from operation")
		}
		out.RemoteReceipt = &r
	}
	if state != "reserved" && state != "applying" && state != "unknown" && state != "complete" && state != "confirmed" && state != "sealed" {
		return Lookup{}, errors.New("invalid sync operation state")
	}
	if len(sealRaw) > 0 {
		var seal model.SSHKeyFence
		if err := json.Unmarshal(sealRaw, &seal); err != nil {
			return Lookup{}, err
		}
		if err := validNonexecution(out.Intent, seal); err != nil {
			return Lookup{}, err
		}
		out.Nonexecution = &seal
	}
	if (state == "sealed") != (out.Nonexecution != nil) {
		return Lookup{}, errors.New("sync nonexecution evidence differs from state")
	}
	return out, nil
}

func loadTx(ctx context.Context, tx *sql.Tx, key string, side syncreconcile.Side) (Lookup, error) {
	var record, manifest, localRaw, remoteRaw, sealRaw []byte
	var digest, state, pair string
	var generation uint64
	err := tx.QueryRowContext(ctx, `SELECT record,record_sha256,manifest,state,local_receipt,remote_receipt,nonexecution,pair,generation FROM sync_operations WHERE key=? AND side=?`, key, side).Scan(&record, &digest, &manifest, &state, &localRaw, &remoteRaw, &sealRaw, &pair, &generation)
	if err != nil {
		return Lookup{}, err
	}
	out, err := decodeOperation(record, manifest, digest, state, localRaw, remoteRaw, sealRaw)
	if err != nil {
		return Lookup{}, err
	}
	if out.Intent.Key != key || out.Intent.Side != side || pair != pairID(out.Intent.Binding) || generation != out.Intent.Generation {
		return Lookup{}, errors.New("operation identity changed")
	}
	return out, nil
}

// Lookup reads original-key evidence without starting or retrying work.
func (l *Ledger) Lookup(ctx context.Context) (Lookup, error) {
	if err := l.valid(); err != nil {
		return Lookup{}, err
	}
	tx, err := l.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Lookup{}, err
	}
	defer tx.Rollback()
	out, err := loadTx(ctx, tx, l.Key, l.Side)
	if err != nil {
		return Lookup{}, err
	}
	effects, err := readEffectsTx(ctx, tx, out)
	if err != nil {
		return Lookup{}, err
	}
	out.Effects = effects.visible
	if err = verifyOwnCompletion(out, effects); err != nil {
		return Lookup{}, err
	}
	return out, tx.Commit()
}

type effectEvidence struct {
	visible  []syncfs.Receipt
	applied  []syncfs.Receipt
	complete bool
}

func decodeEffect(raw []byte, intent Intent, acts []syncreconcile.Action, side syncreconcile.Side, index int, state string) (syncfs.Receipt, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return syncfs.Receipt{}, errors.New("effect receipt missing or exceeds bound")
	}
	var receipt syncfs.Receipt
	if err := strictJSON(raw, &receipt); err != nil {
		return syncfs.Receipt{}, err
	}
	if state == "" {
		if receipt.State != "applied" && receipt.State != "conflict" && receipt.State != "unknown" {
			return syncfs.Receipt{}, errors.New("invalid effect outcome state")
		}
	} else if receipt.State != state {
		return syncfs.Receipt{}, errors.New("effect receipt state changed")
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(raw, canonical) || receipt.Side != side || receipt.Index != index || !receiptMatchesAction(intent, acts, receipt) {
		return syncfs.Receipt{}, errors.New("effect receipt differs from original intent")
	}
	return receipt, nil
}

// Every outcome remains tied to its original prepared intent. Missing rows are
// allowed only before completion; malformed or unexpected rows are never
// interpreted as an absent effect.
func readEffectsTx(ctx context.Context, tx *sql.Tx, op Lookup) (effectEvidence, error) {
	acts := expectedActions(op.Intent, op.Intent.Side)
	evidence := effectEvidence{visible: make([]syncfs.Receipt, 0), applied: make([]syncfs.Receipt, 0, len(acts)), complete: true}
	rows, err := tx.QueryContext(ctx, `SELECT effect_side,idx,prepared,outcome FROM sync_effects WHERE key=? AND owner_side=? ORDER BY idx`, op.Intent.Key, op.Intent.Side)
	if err != nil {
		return effectEvidence{}, err
	}
	defer rows.Close()
	next := 0
	for rows.Next() {
		var side string
		var index int
		var prepared, outcome []byte
		if err = rows.Scan(&side, &index, &prepared, &outcome); err != nil {
			return effectEvidence{}, err
		}
		if side != string(op.Intent.Side) || index < next || index >= len(acts) {
			return effectEvidence{}, errors.New("unexpected or duplicate sync effect row")
		}
		if index > next {
			evidence.complete = false
		}
		preparedReceipt, e := decodeEffect(prepared, op.Intent, acts, op.Intent.Side, index, "prepared")
		if e != nil {
			return effectEvidence{}, fmt.Errorf("prepared effect %d: %w", index, e)
		}
		if outcome == nil {
			evidence.visible = append(evidence.visible, preparedReceipt)
			evidence.complete = false
		} else {
			r, err := decodeEffect(outcome, op.Intent, acts, op.Intent.Side, index, "")
			if err != nil {
				return effectEvidence{}, fmt.Errorf("outcome effect %d: %w", index, err)
			}
			evidence.visible = append(evidence.visible, r)
			if r.State != "applied" {
				evidence.complete = false
			} else {
				evidence.applied = append(evidence.applied, r)
			}
		}
		next = index + 1
	}
	if err = rows.Err(); err != nil {
		return effectEvidence{}, err
	}
	if next != len(acts) {
		evidence.complete = false
	}
	return evidence, nil
}

func verifyOwnCompletion(op Lookup, effects effectEvidence) error {
	own := ownReceipt(op, op.Intent.Side)
	if own == nil {
		if op.State == "complete" || op.State == "confirmed" {
			return errors.New("completed sync lacks own endpoint receipt")
		}
		return nil
	}
	if op.State != "complete" && op.State != "confirmed" {
		return errors.New("own endpoint receipt differs from operation state")
	}
	if !effects.complete || len(effects.applied) != own.EffectCount {
		return errors.New("own endpoint receipt lacks exact durable effects")
	}
	raw, err := json.Marshal(effects.applied)
	if err != nil || sha(raw) != own.EffectsDigest {
		return errors.New("own endpoint effect digest differs from durable journal")
	}
	return nil
}

func receiptMatchesAction(i Intent, acts []syncreconcile.Action, r syncfs.Receipt) bool {
	return r.Key == i.Key && r.PlanDigest == i.PlanDigest && r.Generation == i.Generation && r.Index >= 0 && r.Index < len(acts) && reflect.DeepEqual(r.Action, acts[r.Index])
}

func (l *Ledger) effectIntentTx(ctx context.Context, tx *sql.Tx) (Intent, []syncreconcile.Action, string, error) {
	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	if l.cache == nil || l.cache.db != l.DB || l.cache.key != l.Key || l.cache.side != l.Side {
		op, err := loadTx(ctx, tx, l.Key, l.Side)
		if err != nil {
			return Intent{}, nil, "", err
		}
		record, err := json.Marshal(op.Intent)
		if err != nil {
			return Intent{}, nil, "", err
		}
		l.cache = &effectIntent{db: l.DB, key: l.Key, side: l.Side, intent: op.Intent, actions: expectedActions(op.Intent, l.Side), recordDigest: sha(record)}
	}
	var digest, pair, state string
	var generation uint64
	err := tx.QueryRowContext(ctx, `SELECT record_sha256,pair,generation,state FROM sync_operations WHERE key=? AND side=?`, l.Key, l.Side).Scan(&digest, &pair, &generation, &state)
	if err != nil {
		return Intent{}, nil, "", err
	}
	if digest != l.cache.recordDigest || pair != pairID(l.cache.intent.Binding) || generation != l.cache.intent.Generation {
		return Intent{}, nil, "", errors.New("operation identity changed during effect journal")
	}
	return l.cache.intent, l.cache.actions, state, nil
}

func (l *Ledger) Prepare(ctx context.Context, r syncfs.Receipt) error {
	if err := l.valid(); err != nil {
		return err
	}
	if r.Side != l.Side || r.State != "prepared" || !validReceiptSize(r) {
		return errors.New("invalid prepared effect receipt")
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = durableTx(ctx, tx); err != nil {
		return err
	}
	intent, actions, state, err := l.effectIntentTx(ctx, tx)
	if err != nil {
		return err
	}
	if state != "reserved" && state != "applying" || !receiptMatchesAction(intent, actions, r) {
		return ErrUnknown
	}
	raw, _ := json.Marshal(r)
	_, err = tx.ExecContext(ctx, `INSERT INTO sync_effects(key,owner_side,effect_side,idx,prepared) VALUES(?,?,?,?,?)`, l.Key, l.Side, r.Side, r.Index, raw)
	if err != nil {
		return ErrUnknown
	}
	// The operation row carries large immutable intent/manifest blobs. Once it
	// is applying, another state UPDATE rewrites that row for every effect.
	if state == "reserved" {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE sync_operations SET state='applying' WHERE key=? AND side=? AND state='reserved'`, l.Key, l.Side)
		if err != nil {
			return err
		}
		if affected, e := result.RowsAffected(); e != nil || affected != 1 {
			return ErrUnknown
		}
	}
	return tx.Commit()
}

func (l *Ledger) Outcome(ctx context.Context, r syncfs.Receipt) error {
	if err := l.valid(); err != nil {
		return err
	}
	if r.Side != l.Side || !validReceiptSize(r) || r.State != "applied" && r.State != "conflict" && r.State != "unknown" {
		return errors.New("invalid outcome receipt")
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = durableTx(ctx, tx); err != nil {
		return err
	}
	intent, actions, state, err := l.effectIntentTx(ctx, tx)
	if err != nil {
		return err
	}
	if state != "applying" || !receiptMatchesAction(intent, actions, r) {
		return ErrUnknown
	}
	var prepared, outcome []byte
	err = tx.QueryRowContext(ctx, `SELECT prepared,outcome FROM sync_effects WHERE key=? AND owner_side=? AND effect_side=? AND idx=?`, l.Key, l.Side, r.Side, r.Index).Scan(&prepared, &outcome)
	if err != nil || outcome != nil {
		return ErrUnknown
	}
	original, err := decodeEffect(prepared, intent, actions, l.Side, r.Index, "prepared")
	if err != nil || original.State != "prepared" {
		return ErrUnknown
	}
	raw, _ := json.Marshal(r)
	result, err := tx.ExecContext(ctx, `UPDATE sync_effects SET outcome=? WHERE key=? AND owner_side=? AND effect_side=? AND idx=? AND outcome IS NULL`, raw, l.Key, l.Side, r.Side, r.Index)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return ErrUnknown
	}
	if r.State != "applied" {
		if _, err = tx.ExecContext(ctx, `UPDATE sync_operations SET state='unknown' WHERE key=? AND side=?`, l.Key, l.Side); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validReceiptSize(r syncfs.Receipt) bool {
	raw, err := json.Marshal(r)
	return err == nil && len(raw) <= 1<<20
}

// CompleteOwnSide records one exact completion only after every expected
// effect has an applied durable outcome. The caller must recheck the held root
// and resource obligations before invoking it. It never treats a process exit
// or missing effect row as successful publication.
func (l *Ledger) CompleteOwnSide(ctx context.Context) (SideReceipt, error) {
	if err := l.valid(); err != nil {
		return SideReceipt{}, err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return SideReceipt{}, err
	}
	defer tx.Rollback()
	if err = durableTx(ctx, tx); err != nil {
		return SideReceipt{}, err
	}
	op, err := loadTx(ctx, tx, l.Key, l.Side)
	if err != nil {
		return SideReceipt{}, err
	}
	if op.State == "unknown" || op.State == "confirmed" || op.State == "sealed" {
		return SideReceipt{}, ErrUnknown
	}
	effects, err := readEffectsTx(ctx, tx, op)
	if err != nil {
		return SideReceipt{}, err
	}
	if !effects.complete {
		return SideReceipt{}, ErrUnknown
	}
	if err = verifyOwnCompletion(op, effects); err != nil {
		return SideReceipt{}, err
	}
	raw, _ := json.Marshal(effects.applied)
	receipt := SideReceipt{Key: l.Key, PlanDigest: op.Intent.PlanDigest, Generation: op.Intent.Generation, Side: l.Side, RootID: expectedRoot(op.Intent, l.Side), ManifestDigest: op.Intent.ManifestDigest, BaselineSeal: op.Intent.Proposal.Seal, EffectCount: len(effects.applied), EffectsDigest: sha(raw)}
	encoded, _ := json.Marshal(receipt)
	column := "local_receipt"
	if l.Side == syncreconcile.Remote {
		column = "remote_receipt"
	}
	if existing := ownReceipt(op, l.Side); existing != nil {
		if !reflect.DeepEqual(*existing, receipt) {
			return SideReceipt{}, ErrUnknown
		}
		return receipt, nil
	}
	query := `UPDATE sync_operations SET ` + column + `=?,state='complete' WHERE key=? AND side=?`
	if _, err = tx.ExecContext(ctx, query, encoded, l.Key, l.Side); err != nil {
		return SideReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return SideReceipt{}, ErrUnknown
	}
	return receipt, nil
}

func ownReceipt(op Lookup, side syncreconcile.Side) *SideReceipt {
	if side == syncreconcile.Local {
		return op.LocalReceipt
	}
	return op.RemoteReceipt
}

func validNonexecution(i Intent, seal model.SSHKeyFence) error {
	if i.Side != syncreconcile.Local || seal.Validate() != nil || seal.State != "sealed" || seal.Control == nil || seal.ControlSHA256 != i.SSHControlSHA256 || seal.Key != i.SSHKey || seal.Control.Key != i.SSHKey || seal.Control.CommandSHA256 != i.WorkerCommandSHA256 || seal.Control.Reason != i.Reason || !i.RemoteResources.Fits(seal.Control.Resources) {
		return errors.New("exact destination SSH nonexecution seal required")
	}
	return nil
}

// RecordNonexecution preserves a destination's exact SSH seal only when this
// source operation has no native effect or endpoint receipt. It releases the
// pair's first-run generation for a separately reviewed new key, while the
// original sealed record remains durable and lookup-only.
func (l *Ledger) RecordNonexecution(ctx context.Context, seal model.SSHKeyFence) error {
	if err := l.valid(); err != nil {
		return err
	}
	if l.Side != syncreconcile.Local {
		return errors.New("only the SSH source records nonexecution")
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = durableTx(ctx, tx); err != nil {
		return err
	}
	op, err := loadTx(ctx, tx, l.Key, l.Side)
	if err != nil {
		return err
	}
	if err = validNonexecution(op.Intent, seal); err != nil {
		return err
	}
	if op.State == "sealed" {
		if reflect.DeepEqual(*op.Nonexecution, seal) {
			return nil
		}
		return ErrUnknown
	}
	if op.State != "reserved" || op.LocalReceipt != nil || op.RemoteReceipt != nil {
		return ErrUnknown
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_effects WHERE key=? AND owner_side=?`, l.Key, l.Side).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrUnknown
	}
	raw, err := json.Marshal(seal)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sync_operations SET state='sealed',nonexecution=? WHERE key=? AND side=? AND state='reserved'`, raw, l.Key, l.Side)
	if err != nil {
		return err
	}
	if affected, e := result.RowsAffected(); e != nil || affected != 1 {
		return ErrUnknown
	}
	if err = tx.Commit(); err != nil {
		return ErrUnknown
	}
	return nil
}

// RecordPeerReceipt must be called only after the Manager authenticates the
// opposite endpoint over the leased SSH control channel. A duplicate exact
// receipt is harmless; it never repeats a native effect.
func (l *Ledger) RecordPeerReceipt(ctx context.Context, r SideReceipt) error {
	if err := l.valid(); err != nil {
		return err
	}
	peer := syncreconcile.Local
	if l.Side == syncreconcile.Local {
		peer = syncreconcile.Remote
	}
	if r.Side != peer {
		return errors.New("receipt is not from peer")
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = durableTx(ctx, tx); err != nil {
		return err
	}
	op, err := loadTx(ctx, tx, l.Key, l.Side)
	if err != nil {
		return err
	}
	if !r.matches(op.Intent, peer) {
		return errors.New("peer receipt differs from reviewed operation")
	}
	if existing := ownReceipt(op, peer); existing != nil {
		if !reflect.DeepEqual(*existing, r) {
			return ErrUnknown
		}
		return nil
	}
	if op.State == "unknown" || op.State == "sealed" {
		return ErrUnknown
	}
	raw, _ := json.Marshal(r)
	column := "local_receipt"
	if peer == syncreconcile.Remote {
		column = "remote_receipt"
	}
	_, err = tx.ExecContext(ctx, `UPDATE sync_operations SET `+column+`=? WHERE key=? AND side=?`, raw, l.Key, l.Side)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return ErrUnknown
	}
	return nil
}

func pairID(b syncreconcile.Binding) string { raw, _ := json.Marshal(b); return sha(raw) }

func currentBaselineTx(ctx context.Context, tx *sql.Tx, binding syncreconcile.Binding, side syncreconcile.Side) (*syncreconcile.ConfirmedBaseline, error) {
	var gen uint64
	var seal string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT generation,seal,record FROM sync_baselines WHERE pair=?`, pairID(binding)).Scan(&gen, &seal, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		var prior int
		e := tx.QueryRowContext(ctx, `SELECT 1 FROM sync_operations WHERE pair=? AND side=? AND state!='sealed' LIMIT 1`, pairID(binding), side).Scan(&prior)
		if e == nil {
			return nil, fmt.Errorf("%w: baseline absent for previously used pair", ErrUnknown)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxRecordBytes {
		return nil, errors.New("baseline record exceeds bound")
	}
	var baseline syncreconcile.ConfirmedBaseline
	if err = json.Unmarshal(raw, &baseline); err != nil {
		return nil, err
	}
	if baseline.Generation != gen || baseline.Seal != seal || !reflect.DeepEqual(baseline.Binding, binding) {
		return nil, errors.New("baseline evidence changed")
	}
	if !baselineIntegrity(baseline) {
		return nil, errors.New("baseline seal does not match stored record")
	}
	return &baseline, nil
}

func baselineIntegrity(b syncreconcile.ConfirmedBaseline) bool {
	want := b.Seal
	b.Seal = ""
	raw, err := json.Marshal(b)
	return err == nil && hexSHA(want) && sha(raw) == want
}

// LoadBaseline is read-only and returns nil only when the exact ordered pair
// has no confirmed generation. A missing row for a previously used pair must
// be treated as lost evidence by the Manager, not declared a first run.
func LoadBaseline(ctx context.Context, db *sql.DB, binding syncreconcile.Binding, side syncreconcile.Side) (*syncreconcile.ConfirmedBaseline, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	b, err := currentBaselineTx(ctx, tx, binding, side)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return b, nil
}

// ConfirmBaselineTx must run after matching dual receipts are durable. The
// generation update is compare-and-swap against the prior baseline seal. The
// source and destination each call it locally; a lost reply is lookup-only.
func ConfirmBaselineTx(ctx context.Context, tx *sql.Tx, key string, side syncreconcile.Side) (syncreconcile.ConfirmedBaseline, error) {
	if err := durableTx(ctx, tx); err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	op, err := loadTx(ctx, tx, key, side)
	if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	if op.LocalReceipt == nil || op.RemoteReceipt == nil || (op.State != "complete" && op.State != "confirmed") {
		return syncreconcile.ConfirmedBaseline{}, ErrUnconfirmed
	}
	effects, err := readEffectsTx(ctx, tx, op)
	if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	if err = verifyOwnCompletion(op, effects); err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	i := op.Intent
	pair := pairID(i.Binding)
	var gen uint64
	var seal string
	var priorRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT generation,seal,record FROM sync_baselines WHERE pair=?`, pair).Scan(&gen, &seal, &priorRaw)
	if errors.Is(err, sql.ErrNoRows) {
		if i.Generation != 1 || i.PriorSeal != "" {
			return syncreconcile.ConfirmedBaseline{}, errors.New("prior baseline unavailable")
		}
		raw, _ := json.Marshal(i.Proposal)
		_, err = tx.ExecContext(ctx, `INSERT INTO sync_baselines(pair,generation,seal,record) VALUES(?,?,?,?)`, pair, i.Generation, i.Proposal.Seal, raw)
	} else if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	} else {
		var prior syncreconcile.ConfirmedBaseline
		if len(priorRaw) > MaxRecordBytes || json.Unmarshal(priorRaw, &prior) != nil || prior.Generation != gen || prior.Seal != seal || !reflect.DeepEqual(prior.Binding, i.Binding) || !baselineIntegrity(prior) {
			return syncreconcile.ConfirmedBaseline{}, errors.New("prior baseline evidence changed")
		}
		if gen == i.Generation && seal == i.Proposal.Seal {
			err = nil
		} else {
			if gen+1 != i.Generation || seal != i.PriorSeal {
				return syncreconcile.ConfirmedBaseline{}, errors.New("baseline generation or seal changed")
			}
			raw, _ := json.Marshal(i.Proposal)
			result, e := tx.ExecContext(ctx, `UPDATE sync_baselines SET generation=?,seal=?,record=? WHERE pair=? AND generation=? AND seal=?`, i.Generation, i.Proposal.Seal, raw, pair, gen, seal)
			if e != nil {
				return syncreconcile.ConfirmedBaseline{}, e
			}
			if affected, e := result.RowsAffected(); e != nil || affected != 1 {
				return syncreconcile.ConfirmedBaseline{}, ErrUnknown
			}
		}
	}
	if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sync_operations SET state='confirmed' WHERE key=? AND side=?`, key, side); err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	return i.Proposal, nil
}

func (l *Ledger) ConfirmBaseline(ctx context.Context) (syncreconcile.ConfirmedBaseline, error) {
	if err := l.valid(); err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	defer tx.Rollback()
	b, err := ConfirmBaselineTx(ctx, tx, l.Key, l.Side)
	if err != nil {
		return syncreconcile.ConfirmedBaseline{}, err
	}
	if err = tx.Commit(); err != nil {
		return syncreconcile.ConfirmedBaseline{}, ErrUnknown
	}
	return b, nil
}

// VerifyDurableSQLite checks the connection's durability setting before any
// intent or effect journal is trusted as a crash fence.
func VerifyDurableSQLite(ctx context.Context, db *sql.DB) error {
	var level int
	if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&level); err != nil {
		return err
	}
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	return durableSettings(level, mode)
}

func durableTx(ctx context.Context, tx *sql.Tx) error {
	var level int
	if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&level); err != nil {
		return err
	}
	var mode string
	if err := tx.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	return durableSettings(level, mode)
}

func durableSettings(level int, mode string) error {
	if level < 2 {
		return fmt.Errorf("SQLite synchronous=FULL or stronger required, got %d", level)
	}
	switch strings.ToLower(mode) {
	case "wal", "delete", "truncate", "persist":
		return nil
	}
	return fmt.Errorf("durable SQLite rollback/WAL journal required, got %q", mode)
}
